package testhelpers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/yaml"

	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
)

// TestServerResponse represents the standard JSON response from test servers
type TestServerResponse struct {
	Message    string  `json:"message"`
	RequestID  float64 `json:"request_id"`
	Timestamp  float64 `json:"timestamp"`
	ServerHits float64 `json:"server_hits"`
	SquidPod   string  `json:"squid_pod"` // extracted from Via header
}

// CachingTestServer wraps an HTTP test server with request counting and caching-friendly configuration
type CachingTestServer struct {
	*httptest.Server
	RequestCount *int32
	PodIP        string
	URL          string
}

// ExtractSquidPodFromViaHeader extracts the Squid pod name from the Via response header
// Via header format: "1.1 squid-<pod-name> (squid/<version>)"
func ExtractSquidPodFromViaHeader(resp *http.Response) string {
	viaHeader := resp.Header.Get("Via")
	if viaHeader == "" {
		return ""
	}

	// Parse "1.1 hostname (squid/version)" format and return the hostname (second field)
	parts := strings.Fields(viaHeader)
	if len(parts) >= 2 {
		return parts[1]
	}

	return ""
}

// CacheHitResult contains the results of finding a cache hit from a pod
type CacheHitResult struct {
	CacheHitFound    bool
	CachedResponse   *TestServerResponse
	CacheHitPod      string
	OriginalResponse *TestServerResponse
	PodFirstHits     map[string]*TestServerResponse
}

// FindCacheHitFromAnyPod makes requests until finding a cache hit from any pod
func FindCacheHitFromAnyPod(client *http.Client, testURL string, replicaCount int32) (*CacheHitResult, error) {
	maxAttempts := int(replicaCount) + 1
	fmt.Printf("🔍 DEBUG: Replica count: %d, max attempts: %d\n", replicaCount, maxAttempts)

	// Maximum attempts needed: replicas + 1 (pigeonhole principle)
	// With N pods, we need at most N+1 requests to guarantee hitting the same pod twice
	podFirstHits := make(map[string]*TestServerResponse)

	// Making requests until we get a cache hit from any pod
	for i := range maxAttempts {
		fmt.Printf("\n🔍 DEBUG: === REQUEST %d/%d ===\n", i+1, maxAttempts)

		resp, body, err := MakeCachingRequest(client, testURL)
		Expect(err).NotTo(HaveOccurred(), "Request should succeed")

		currentPod := ExtractSquidPodFromViaHeader(resp)
		Expect(currentPod).NotTo(BeEmpty(), "Via header should contain pod name")

		response, err := ParseTestServerResponse(body)
		Expect(err).NotTo(HaveOccurred(), "Should parse response JSON")

		// Debug logging
		fmt.Printf("🔍 DEBUG: Full response details:\n")
		fmt.Printf("  Status: %s\n", resp.Status)
		for key, values := range resp.Header {
			for _, value := range values {
				fmt.Printf("    %s: %s\n", key, value)
			}
		}

		resp.Body.Close()
		fmt.Printf("🔍 DEBUG: Request %d: pod=%s, request_id=%v\n", i+1, currentPod, response.RequestID)

		if firstHit, seen := podFirstHits[currentPod]; seen {
			fmt.Printf("🔍 DEBUG: Pod %s seen before (first hit: request_id=%v)\n", currentPod, firstHit.RequestID)
			// We've hit this pod before - check if it's a cache hit
			if response.RequestID == firstHit.RequestID {
				fmt.Printf("✅ CACHE HIT DETECTED on request %d from pod %s!\n", i+1, currentPod)
				fmt.Printf("🔍 DEBUG: Original request_id: %v, Cached request_id: %v\n",
					firstHit.RequestID, response.RequestID)
				fmt.Printf("🔍 DEBUG: Original timestamp: %f, Cached timestamp: %f\n",
					firstHit.Timestamp, response.Timestamp)

				return &CacheHitResult{
					CacheHitFound:    true,
					CachedResponse:   response,
					CacheHitPod:      currentPod,
					OriginalResponse: firstHit,
					PodFirstHits:     podFirstHits,
				}, nil

			}
		} else {
			// First time seeing this pod - record its first hit
			podFirstHits[currentPod] = response
			fmt.Printf("🔍 DEBUG: First hit from pod %s with request_id=%v\n", currentPod, response.RequestID)
		}
	}

	return nil, fmt.Errorf("no cache hit found from any pod within %d attempts", maxAttempts)

}

// ValidateCacheHitSamePod verifies that a cached response came from the same pod
// and has the same request_id as the original
func ValidateCacheHitSamePod(originalResponse, cachedResponse *TestServerResponse, originalPod, cachedPod string) {
	// Verify both requests went through the same pod
	Expect(cachedPod).To(Equal(originalPod),
		"Cached request should be handled by the same pod as original")

	// Both responses should have the same request ID (indicating cache hit)
	Expect(cachedResponse.RequestID).To(Equal(originalResponse.RequestID),
		"Cached response should have same request_id as original")

	// Cache should preserve the original timestamp
	Expect(cachedResponse.Timestamp).To(Equal(originalResponse.Timestamp),
		"Cached response should preserve original timestamp")
}

// NewCachingTestServer creates a new test server configured for cross-pod communication
func NewCachingTestServer(message string, podIP string, port int) (*CachingTestServer, error) {
	var requestCount int32

	// Create HTTP server with request tracking
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		// Add cache headers to make content cacheable
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")

		// Return JSON response with request count
		response := TestServerResponse{
			Message:    message,
			RequestID:  float64(count),
			Timestamp:  float64(time.Now().Unix()),
			ServerHits: float64(count),
		}

		jsonResponse, _ := json.Marshal(response)
		w.Write(jsonResponse)
	}))

	// Disable keep-alives to ensure port reuse between tests
	server.Config.SetKeepAlivesEnabled(false)

	// Configure server to listen on all interfaces with the specified port
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to create listener on port %d: %w", port, err)
	}
	server.Listener = listener
	server.Start()

	// Get the actual port that was assigned (important when port=0 for random port)
	_, actualPortStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	serverURL := fmt.Sprintf("http://%s:%s", podIP, actualPortStr)

	return &CachingTestServer{
		Server:       server,
		RequestCount: &requestCount,
		PodIP:        podIP,
		URL:          serverURL,
	}, nil
}

// GetRequestCount returns the current request count
func (pts *CachingTestServer) GetRequestCount() int32 {
	return atomic.LoadInt32(pts.RequestCount)
}

// ResetRequestCount resets the request counter to zero
func (pts *CachingTestServer) ResetRequestCount() {
	atomic.StoreInt32(pts.RequestCount, 0)
}

// NewSquidCachingClient creates an HTTP client configured to use the Squid caching
func NewSquidCachingClient(serviceName, namespace string) (*http.Client, error) {
	// Set up caching URL to squid service
	cachingURL, err := url.Parse(fmt.Sprintf("http://%s.%s.svc.cluster.local:3128", serviceName, namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to parse caching URL: %w", err)
	}

	// Create HTTP client with caching configuration
	transport := &http.Transport{
		Proxy: http.ProxyURL(cachingURL),
		// Disable keep-alive to ensure fresh connections for cache testing
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

// NewTrustedSquidCachingClient creates an HTTP client configured to use the Squid caching and trust both the Squid CA and test-server CA
func NewTrustedSquidCachingClient(serviceName, namespace string, squidCACertPEM []byte, testServerCACertPEM []byte) (*http.Client, error) {
	// Set up caching URL to squid service
	cachingURL, err := url.Parse(fmt.Sprintf("http://%s.%s.svc.cluster.local:3128", serviceName, namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to parse caching URL: %w", err)
	}

	// Create a combined certificate pool with both CAs
	caCertPool := x509.NewCertPool()

	// Add the Squid CA certificate
	if len(squidCACertPEM) > 0 {
		if !caCertPool.AppendCertsFromPEM(squidCACertPEM) {
			return nil, fmt.Errorf("failed to append Squid CA certificate to pool")
		}
	}

	// Add the test-server CA certificate
	if len(testServerCACertPEM) > 0 {
		if !caCertPool.AppendCertsFromPEM(testServerCACertPEM) {
			return nil, fmt.Errorf("failed to append test-server CA certificate to pool")
		}
	}

	// Create TLS config that trusts both CAs
	tlsConfig := &tls.Config{
		RootCAs: caCertPool,
	}

	// Create HTTP client with caching configuration and trusted TLS
	transport := &http.Transport{
		Proxy:           http.ProxyURL(cachingURL),
		TLSClientConfig: tlsConfig,
		// Disable keep-alive to ensure fresh connections for cache testing
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

// MakeCachingRequest makes an HTTP request through the Squid caching and returns the response
func MakeCachingRequest(client *http.Client, url string) (*http.Response, []byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return resp, body, nil
}

// ParseTestServerResponse parses a JSON response from a test server
func ParseTestServerResponse(body []byte) (*TestServerResponse, error) {
	fmt.Printf("DEBUG: Raw response body: %s\n", string(body))
	var response TestServerResponse
	err := json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("Failed to parse JSON response: ", string(body))
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}
	return &response, nil
}

// ValidateCacheHit verifies that a response was served from cache
func ValidateCacheHit(originalResponse, cachedResponse *TestServerResponse, expectedRequestID float64) {
	// Both responses should have the same request ID (indicating cache hit)
	Expect(cachedResponse.RequestID).To(Equal(expectedRequestID),
		"Cached response should have same request_id as original")

	// Cache should preserve the original timestamp
	Expect(cachedResponse.Timestamp).To(Equal(originalResponse.Timestamp),
		"Cached response should preserve original timestamp")

	// Server hits should remain the same (no additional server requests)
	Expect(cachedResponse.ServerHits).To(Equal(originalResponse.ServerHits),
		"Cached response should show same server hit count")
}

// ValidateCacheHeaders verifies that appropriate cache headers are present
func ValidateCacheHeaders(resp *http.Response) {
	Expect(resp.Header.Get("Cache-Control")).To(ContainSubstring("max-age=300"),
		"Response should have cache control headers")
	Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"),
		"Response should have correct content type")
}

// ValidateServerHit verifies that a request actually hit the server
func ValidateServerHit(response *TestServerResponse, expectedRequestID float64, server *CachingTestServer) {
	Expect(response.RequestID).To(Equal(expectedRequestID),
		"Request should have expected request ID")
	Expect(server.GetRequestCount()).To(Equal(int32(expectedRequestID)),
		"Server should have received expected number of requests")
}

// WaitForStatefulSetReady waits for a statefulset to be ready and all replica pods to be present
func WaitForStatefulSetReady(ctx context.Context, client kubernetes.Interface, name string) (*v1.StatefulSet, error) {
	fmt.Printf("Waiting for %s statefulset to be ready...\n", name)

	var expectedReplicas int32
	var statefulSet *v1.StatefulSet
	Eventually(func() error {
		var err error
		statefulSet, err = client.AppsV1().StatefulSets(Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get statefulsets: %w", err)
		}

		expectedReplicas = *statefulSet.Spec.Replicas
		fmt.Printf("StatefulSet status: %d/%d replicas ready (expected: %d)\n",
			statefulSet.Status.ReadyReplicas, expectedReplicas, expectedReplicas)

		if statefulSet.Status.ReadyReplicas != expectedReplicas {
			return fmt.Errorf("statefulset not ready: %d/%d replicas ready",
				statefulSet.Status.ReadyReplicas, expectedReplicas)
		}

		return nil
	}, 120*time.Second, 5*time.Second).Should(Succeed())

	fmt.Printf("Waiting for %d pod(s) to be present and ready...\n", expectedReplicas)
	pods, err := GetPods(ctx, client, Namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}
	fmt.Printf("Successfully found %d pod(s) ready\n", len(pods))

	return statefulSet, nil
}

type CacheValues struct {
	AllowList []string `json:"allowList"`
}

type TLSOutgoingOptionsValues struct {
	CAFile string `json:"caFile,omitempty"`
}

type SquidHelmValues struct {
	Cache              *CacheValues              `json:"cache,omitempty"`
	Environment        string                    `json:"environment,omitempty"`
	ReplicaCount       int                       `json:"replicaCount,omitempty"`
	TLSOutgoingOptions *TLSOutgoingOptionsValues `json:"tlsOutgoingOptions,omitempty"`
	Affinity           json.RawMessage           `json:"affinity,omitempty"`
	Volumes            []corev1.Volume           `json:"volumes,omitempty"`
	VolumeMounts       []corev1.VolumeMount      `json:"volumeMounts,omitempty"`
	Nginx              *NginxValues              `json:"nginx,omitempty"`
}

// parseImageReference extracts repository and tag from an image reference
// Handles both digest (@sha256:xxx) and tag (:vX.Y.Z) formats
func parseImageReference(image string) (repo, tag string) {
	if strings.Contains(image, "@sha256:") {
		// Digest format: repo@sha256:xxx
		parts := strings.SplitN(image, "@", 2)
		return parts[0], parts[1]
	} else if strings.Contains(image, ":") {
		// Tag format: repo:tag
		lastColon := strings.LastIndex(image, ":")
		return image[:lastColon], image[lastColon+1:]
	}
	return image, "latest"
}

// IsOpenShiftPlatform detects if the Kubernetes cluster is OpenShift by checking
// for OpenShift-specific API groups using the discovery client
func IsOpenShiftPlatform(client kubernetes.Interface) (bool, error) {
	groupList, err := client.Discovery().ServerGroups()
	if err != nil {
		return false, fmt.Errorf("failed to discover server groups: %v", err)
	}

	for _, group := range groupList.Groups {
		if group.Name == "security.openshift.io" {
			return true, nil
		}
	}

	return false, nil
}

// buildPrereleaseHelmArgs builds Helm arguments for prerelease environment
// This preserves pipeline-deployed images and disables mirrord (not available in EaaS)
func buildPrereleaseHelmArgs(environment string, statefulSet *v1.StatefulSet) []string {
	// In prerelease (OpenShift EaaS), disable mirrord which is not available
	// Skip managing cert-manager during test reconfigurations to avoid reconciliation timeouts
	extraArgs := []string{
		"--set", "mirrord.enabled=false",
		"--set", "installCertManagerComponents=false",
	}

	// Preserve pipeline-deployed image tags to prevent reverting to :latest
	if statefulSet != nil && len(statefulSet.Spec.Template.Spec.Containers) > 0 {
		currentImage := statefulSet.Spec.Template.Spec.Containers[0].Image
		imageRepo, imageTag := parseImageReference(currentImage)

		// Derive test image (squid → squid-tester in prerelease)
		testImageRepo := strings.Replace(imageRepo, "/squid", "/squid-tester", 1)

		// Pass to helm to prevent reverting to :latest defaults
		extraArgs = append(extraArgs,
			"--set", fmt.Sprintf("envSettings.%s.squid.image.repository=%s", environment, imageRepo),
			"--set", fmt.Sprintf("envSettings.%s.squid.image.tag=%s", environment, imageTag),
			"--set", fmt.Sprintf("envSettings.%s.test.image.repository=%s", environment, testImageRepo),
			"--set", fmt.Sprintf("envSettings.%s.test.image.tag=%s", environment, imageTag),
		)
	}

	return extraArgs
}

// ConfigureSquidWithHelm configures Squid statefulset using helm values
func ConfigureSquidWithHelm(ctx context.Context, client kubernetes.Interface, values SquidHelmValues) error {
	// Environment is passed from test pod via SQUID_ENVIRONMENT env var
	// This is set by test-pod.yaml from .Values.environment
	// Default is "dev" for local testing
	environment := os.Getenv("SQUID_ENVIRONMENT")
	if environment == "" {
		environment = "dev"
	}

	values.Environment = environment

	// Always enable nginx (disabled by default in values.yaml)
	if values.Nginx == nil {
		values.Nginx = &NginxValues{Enabled: true}
	} else {
		values.Nginx.Enabled = true
	}

	openshift, err := IsOpenShiftPlatform(client)
	if err != nil {
		return fmt.Errorf("failed to check if OpenShift platform: %w", err)
	}

	// Get current statefulset for image preservation logic
	statefulSet, err := client.AppsV1().StatefulSets(Namespace).Get(ctx, SquidStatefulSetName, metav1.GetOptions{})

	// Handle replica count logic:
	// 1. If SQUID_REPLICA_COUNT env var does not exist or equals 0 -> use value from values.yaml (don't set ReplicaCount)
	// 2. If SQUID_REPLICA_COUNT exists and > 0 -> use the env var value
	envReplicas := os.Getenv("SQUID_REPLICA_COUNT")
	if envReplicas != "" {
		if count, err := strconv.Atoi(envReplicas); err == nil && count > 0 {
			// Case 2: Environment variable > 0 -> use the env var value
			values.ReplicaCount = count
			fmt.Printf("DEBUG: Using replica count from SQUID_REPLICA_COUNT env var: %d\n", count)
		} else {
			// Case 1: Environment variable equals 0 or invalid -> use values.yaml default
			fmt.Printf("DEBUG: SQUID_REPLICA_COUNT=%s, using default from values.yaml\n", envReplicas)
			// Don't set values.ReplicaCount, let Helm use the default from values.yaml
		}
	} else {
		// Case 1: Environment variable does not exist -> use values.yaml default
		fmt.Printf("DEBUG: SQUID_REPLICA_COUNT not set, using default from values.yaml\n")
		// Don't set values.ReplicaCount, let Helm use the default from values.yaml
	}

	valuesFile, err := writeValuesToFile(&values)
	if err != nil {
		return fmt.Errorf("failed to write values to file: %w", err)
	}
	defer os.Remove(valuesFile)

	// Use the temporary values file with helm
	// Allow overriding chart path via SQUID_CHART_PATH env var (defaults to ./squid)
	chartPath := os.Getenv("SQUID_CHART_PATH")
	if chartPath == "" {
		chartPath = "./squid"
	}

	// Build helm arguments based on environment
	var extraArgs []string
	if environment == "prerelease" {
		extraArgs = buildPrereleaseHelmArgs(environment, statefulSet)
	}

	// Allow OpenShift SCC to manage the nexus user ID
	if openshift {
		extraArgs = append(extraArgs, "--set", "nexus.securityContext.runAsUser=null")
	}

	err = UpgradeChartWithArgs("squid", chartPath, valuesFile, extraArgs)
	if err != nil {
		return fmt.Errorf("failed to upgrade squid with helm: %w", err)
	}

	_, err = WaitForStatefulSetReady(ctx, client, SquidStatefulSetName)
	if err != nil {
		return fmt.Errorf("failed to wait for squid statefulset to be ready: %w", err)
	}

	_, err = WaitForStatefulSetReady(ctx, client, NginxStatefulSetName)
	if err != nil {
		return fmt.Errorf("failed to wait for nginx statefulset to be ready: %w", err)
	}

	return nil
}

// UpgradeChart performs a helm upgrade with the specified chart and values file
func UpgradeChart(releaseName, chartName string, valuesFile string) error {
	return UpgradeChartWithArgs(releaseName, chartName, valuesFile, nil)
}

// UpgradeChartWithArgs performs a helm upgrade with additional --set arguments
func UpgradeChartWithArgs(releaseName, chartName string, valuesFile string, extraArgs []string) error {
	fmt.Printf("Upgrading helm release '%s' with chart '%s'...\n", releaseName, chartName)

	// Build helm command as a shell string
	// Use default namespace for Helm release metadata (matches magefile.go)
	// Resources created in caching namespace (from chart's namespace templates)
	cmdParts := []string{"helm", "upgrade", "--install", releaseName, chartName, "-n=default", "--wait", "--timeout=300s", "--debug"}

	// Values file is provided by callers
	cmdParts = append(cmdParts, "--values", valuesFile)

	// Append any extra arguments (e.g., --set flags)
	if len(extraArgs) > 0 {
		cmdParts = append(cmdParts, extraArgs...)
	}

	// Join into single shell command string
	shellCmd := strings.Join(cmdParts, " ")
	fmt.Printf("Running helm upgrade command: %s\n", shellCmd)

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run helm upgrade command: %w\n%s", err, string(output))
	}
	return nil
}

// RenderHelmTemplate renders the Helm template with the given values and returns the YAML output
func RenderHelmTemplate(chartPath string, values SquidHelmValues) (string, error) {
	// Environment is passed from test pod via SQUID_ENVIRONMENT env var
	environment := os.Getenv("SQUID_ENVIRONMENT")
	if environment == "" {
		environment = "dev" // Fallback for local testing
	}

	values.Environment = environment
	valuesFile, err := writeValuesToFile(&values)
	if err != nil {
		return "", fmt.Errorf("failed to write values to file: %w", err)
	}
	defer os.Remove(valuesFile)

	cmdParts := []string{"helm", "template", "test-release", chartPath, "--values", valuesFile}

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	// Set working directory to chart parent directory to ensure relative paths work
	chartParentDir, err := FindChartDirectory()
	if err != nil {
		return "", fmt.Errorf("failed to find chart directory: %w", err)
	}
	cmd.Dir = chartParentDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("helm template failed: %w\n%s", err, string(output))
	}

	return string(output), nil
}

// writeValuesToFile writes the given values in YAML format to a temp file and returns the path to the file
func writeValuesToFile(values *SquidHelmValues) (string, error) {
	data, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("failed to marshal values to YAML: %w", err)
	}

	f, err := os.CreateTemp("", "values-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temp values file: %w", err)
	}

	if _, err := f.WriteString(string(data)); err != nil {
		f.Close()
		return "", fmt.Errorf("failed to write temp values file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp values file: %w", err)
	}

	return f.Name(), nil
}

// FindChartDirectory finds the directory containing any Helm chart
// It starts from the directory containing this source file and walks up the tree
// looking for any directory that contains Chart.yaml (indicating a Helm chart)
func FindChartDirectory() (string, error) {
	// Get the directory containing this source file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to determine caller information")
	}

	// Start from the directory containing this file
	dir := filepath.Dir(filename)

	// Walk up the directory tree looking for any Chart.yaml file
	for {
		// Search for Chart.yaml files in this directory and its subdirectories
		chartYamlPath, err := findChartYamlInDirectory(dir)
		if err == nil {
			// Found Chart.yaml, return the directory that contains the chart directory
			// For example, if Chart.yaml is at /project/squid/Chart.yaml,
			// we return /project so that "./squid" works as a relative path
			chartDir := filepath.Dir(chartYamlPath)
			return filepath.Dir(chartDir), nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory without finding any Chart.yaml
			return "", fmt.Errorf("no Chart.yaml found in any subdirectory")
		}
		dir = parent
	}
}

// findChartYamlInDirectory searches for Chart.yaml files in the given directory and its subdirectories
func findChartYamlInDirectory(rootDir string) (string, error) {
	var chartYamlPath string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check if this is a Chart.yaml file
		if info.Name() == "Chart.yaml" {
			chartYamlPath = path
			return filepath.SkipDir // Stop walking once we find the first Chart.yaml
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if chartYamlPath == "" {
		return "", fmt.Errorf("no Chart.yaml found")
	}

	return chartYamlPath, nil
}

// GetSquidPods queries for squid pods and verifies the count matches deployment replicas.
// Uses Eventually pattern to keep retrying until all active pods are running and ready.
// During rolling updates, excludes terminating pods from the count.
func GetPods(ctx context.Context, client kubernetes.Interface, namespace string, statefulSetName string) ([]*corev1.Pod, error) {
	statefulSet, err := client.AppsV1().StatefulSets(namespace).Get(ctx, statefulSetName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get statefulset: %w", err)
	}

	expectedReplicas := *statefulSet.Spec.Replicas
	fmt.Printf("Checking for %s statefulset pods: expecting %d replicas\n", statefulSetName, expectedReplicas)
	labelSelector := fmt.Sprintf(
		"app.kubernetes.io/name=%s,app.kubernetes.io/component=%s",
		statefulSet.GetObjectMeta().GetLabels()["app.kubernetes.io/name"],
		statefulSet.GetObjectMeta().GetLabels()["app.kubernetes.io/component"],
	)

	var result []*corev1.Pod

	Eventually(func() error {
		pods, listErr := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if listErr != nil {
			fmt.Printf("Failed to list pods: %v\n", listErr)
			return fmt.Errorf("failed to list pods: %w", listErr)
		}

		fmt.Printf("Found %d pod(s) in namespace %s\n", len(pods.Items), namespace)

		if len(pods.Items) == 0 {
			fmt.Printf("No pods found, waiting...\n")
			return fmt.Errorf("no pods found")
		}

		// Filter out pods that are terminating (during rolling updates)
		activePods := make([]corev1.Pod, 0, len(pods.Items))
		terminatingCount := 0
		for _, pod := range pods.Items {
			// Skip pods that are terminating (have deletion timestamp)
			if pod.DeletionTimestamp == nil {
				activePods = append(activePods, pod)
			} else {
				terminatingCount++
				fmt.Printf("Pod %s is terminating, excluding from count\n", pod.Name)
			}
		}

		fmt.Printf("Found %d active pod(s) (excluding terminating pods)\n", len(activePods))

		// Wait for all terminating pods to be fully deleted before proceeding
		// This ensures the service won't route traffic to pods with old configuration
		if terminatingCount > 0 {
			fmt.Printf("Found %d terminating pod(s), waiting for them to be fully deleted...\n", terminatingCount)
			return fmt.Errorf("waiting for %d terminating pods to be deleted", terminatingCount)
		}

		if int32(len(activePods)) != expectedReplicas {
			fmt.Printf("Active pod count mismatch: expected %d, found %d, waiting...\n", expectedReplicas, len(activePods))
			return fmt.Errorf("expected %d active pods, found %d", expectedReplicas, len(activePods))
		}

		// Verify all active pods are running and ready
		result = make([]*corev1.Pod, 0, len(activePods))
		for i := range activePods {
			pod := &activePods[i]

			fmt.Printf("Checking pod %s: phase=%s, containers=%d\n",
				pod.Name, pod.Status.Phase, len(pod.Status.ContainerStatuses))

			// Check if pod is in Running phase using Eventually pattern
			if pod.Status.Phase != corev1.PodRunning {
				fmt.Printf("Pod %s is not running: phase=%s, waiting...\n", pod.Name, pod.Status.Phase)
				return fmt.Errorf("pod %s is not running: phase=%s", pod.Name, pod.Status.Phase)
			}

			// Check if all containers in the pod are ready
			allContainersReady := true
			readyContainers := 0
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.Ready {
					readyContainers++
				} else {
					allContainersReady = false
					fmt.Printf("Container %s in pod %s is not ready, waiting...\n", containerStatus.Name, pod.Name)
				}
			}

			fmt.Printf("Pod %s: %d/%d containers ready\n", pod.Name, readyContainers, len(pod.Status.ContainerStatuses))

			if !allContainersReady {
				return fmt.Errorf("pod %s has containers that are not ready", pod.Name)
			}

			result = append(result, pod)
		}

		fmt.Printf("All %d pod(s) for statefulset %s are running and ready\n", len(result), statefulSetName)
		return nil
	}, 120*time.Second, 5*time.Second).Should(Succeed())

	return result, err
}

// GetPodLogsSince retrieves logs from a pod container since a specific timestamp
func GetPodLogsSince(ctx context.Context, client kubernetes.Interface, namespace, podName, containerName string, since *metav1.Time) ([]byte, error) {
	logOptions := &corev1.PodLogOptions{
		Container: containerName,
		SinceTime: since,
	}

	return client.CoreV1().Pods(namespace).GetLogs(podName, logOptions).Do(ctx).Raw()
}

// PullContainerImage pulls a container image and all its layers while discarding the content
// Note: Does NOT support image references pointing to manifest lists
func PullContainerImage(t *http.RoundTripper, imageRef string) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return err
	}

	desc, err := remote.Get(ref, remote.WithTransport(*t))
	if err != nil {
		return err
	}

	img, err := desc.Image()
	if err != nil {
		return err
	}
	layers, err := img.Layers()
	if err != nil {
		return err
	}
	if len(layers) == 0 {
		return fmt.Errorf("no layers found in image")
	}

	for _, layer := range layers {
		cr, err := layer.Compressed()
		if err != nil {
			return err
		}
		defer cr.Close()
		written, err := io.Copy(io.Discard, cr)
		if err != nil {
			return err
		}
		if written == 0 {
			return fmt.Errorf("no bytes written")
		}
	}

	return nil
}

// GetPerSiteMetricsValue extracts a metric value from Prometheus metrics content for a specific hostname.
// It parses the Prometheus text format and returns the numeric value for the given metric and hostname.
//
// Example usage:
//
//	metricsContent := "squid_site_requests_total{hostname=\"example.com\",job=\"squid\"} 42"
//	value, err := GetPerSiteMetricsValue(metricsContent, "squid_site_requests_total", "example.com")
//	// value will be 42
func GetPerSiteMetricsValue(metricsContent, metricName, hostname string) (float64, error) {
	// Parse the metrics using expfmt
	parser := expfmt.NewTextParser(model.LegacyValidation)
	metricFamilies, err := parser.TextToMetricFamilies(strings.NewReader(metricsContent))
	if err != nil {
		return 0, fmt.Errorf("failed to parse metrics: %w", err)
	}

	// Find the metric family with the requested name
	metricFamily, found := metricFamilies[metricName]
	if !found {
		return 0, fmt.Errorf("metric %s not found", metricName)
	}

	// Iterate through metrics in the family to find the one with matching hostname label
	for _, metric := range metricFamily.Metric {
		// Check if this metric has the hostname label matching our target
		for _, label := range metric.Label {
			if label.GetName() == "hostname" && label.GetValue() == hostname {
				// Found the metric with matching hostname, extract the value
				switch metricFamily.GetType() {
				case dto.MetricType_COUNTER:
					return metric.Counter.GetValue(), nil
				case dto.MetricType_GAUGE:
					return metric.Gauge.GetValue(), nil
				case dto.MetricType_UNTYPED:
					return metric.Untyped.GetValue(), nil
				default:
					return 0, fmt.Errorf("unsupported metric type: %s", metricFamily.GetType())
				}
			}
		}
	}

	return 0, fmt.Errorf("metric %s for hostname %s not found", metricName, hostname)
}

// GetAggregatedMetrics retrieves and aggregates metrics from all squid pods by querying each pod's metrics endpoint.
// It returns the total sum of the specified metric across all pods.
//
// Example usage:
//
//	totalRequests := GetAggregatedMetrics(ctx, clientset, metricsClient, namespace, 3, "squid_site_requests_total", "example.com")
func GetAggregatedMetrics(ctx context.Context, client kubernetes.Interface, metricsHTTPClient *http.Client, namespace string, expectedReplicas int32, metricName, hostname string) (float64, error) {
	var totalValue float64
	pods, err := GetPods(ctx, client, namespace, SquidStatefulSetName)
	if err != nil {
		fmt.Printf("DEBUG: Error getting pods: %v\n", err)
		return 0, fmt.Errorf("error getting pods: %w", err)
	}

	for _, pod := range pods {
		podIP := pod.Status.PodIP
		metricsURL := fmt.Sprintf("https://%s:9302/metrics", podIP)

		fmt.Printf("DEBUG: Querying metrics from pod %s (%s) at %s\n", pod.Name, podIP, metricsURL)
		resp, err := metricsHTTPClient.Get(metricsURL)
		if err != nil {
			fmt.Printf("DEBUG: Error querying pod %s: %v\n", pod.Name, err)
			continue
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("DEBUG: Error reading response from %s: %v\n", pod.Name, err)
			continue
		}

		// Parse metrics for this pod
		bodyString := string(bodyBytes)
		podValue, err := GetPerSiteMetricsValue(bodyString, metricName, hostname)
		if err != nil {
			fmt.Printf("DEBUG: Error parsing metric %s for hostname %s from pod %s: %v\n", metricName, hostname, pod.Name, err)
			continue
		}

		totalValue += podValue
		fmt.Printf("DEBUG: Pod %s %s for %s: %.0f\n", pod.Name, metricName, hostname, podValue)
	}

	fmt.Printf("DEBUG: Total aggregated %s for %s: %.0f\n", metricName, hostname, totalValue)
	return totalValue, nil
}

// GetPerPodMetrics retrieves metrics from all squid pods and returns a map of pod names to their metric values.
// Unlike GetAggregatedMetrics, this method does NOT aggregate values - it returns individual pod metrics.
//
// Example usage:
//
//	podMetrics := GetPerPodMetrics(ctx, clientset, metricsClient, namespace, 3, "squid_site_bytes_total", "example.com")
//	podMetrics will be: map[string]float64{"squid-xxx-pod1": 1234.5, "squid-xxx-pod2": 5678.9}
func GetPerPodMetrics(ctx context.Context, client kubernetes.Interface, metricsHTTPClient *http.Client, namespace string, expectedReplicas int32, metricName, hostname string) (map[string]float64, error) {
	podMetrics := make(map[string]float64)
	pods, err := GetPods(ctx, client, namespace, SquidStatefulSetName)
	if err != nil {
		fmt.Printf("DEBUG: Error getting pods: %v\n", err)
		return podMetrics, fmt.Errorf("error getting pods: %w", err)
	}

	for _, pod := range pods {
		podIP := pod.Status.PodIP
		metricsURL := fmt.Sprintf("https://%s:9302/metrics", podIP)

		fmt.Printf("DEBUG: Querying metrics from pod %s (%s) at %s\n", pod.Name, podIP, metricsURL)
		resp, err := metricsHTTPClient.Get(metricsURL)
		if err != nil {
			fmt.Printf("DEBUG: Error querying pod %s: %v\n", pod.Name, err)
			continue
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("DEBUG: Error reading response from %s: %v\n", pod.Name, err)
			continue
		}

		// Parse metrics for this pod
		bodyString := string(bodyBytes)
		podValue, err := GetPerSiteMetricsValue(bodyString, metricName, hostname)
		if err != nil {
			fmt.Printf("DEBUG: Error parsing metric %s for hostname %s from pod %s: %v\n", metricName, hostname, pod.Name, err)
			continue
		}

		podMetrics[pod.Name] = podValue
		fmt.Printf("DEBUG: Pod %s %s for %s: %.0f\n", pod.Name, metricName, hostname, podValue)
	}

	return podMetrics, nil
}

// FindContainerByName finds a container by name in a pod's container spec
// Returns the container if found, or nil if not found
func FindContainerByName(pod *corev1.Pod, containerName string) (*corev1.Container, error) {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == containerName {
			return &pod.Spec.Containers[i], nil
		}
	}
	return nil, fmt.Errorf("container %s not found in pod %s", containerName, pod.Name)
}

// GetRESTConfig returns a Kubernetes REST config, trying in-cluster config first,
// then falling back to kubeconfig file (from KUBECONFIG env var or ~/.kube/config).
// This is useful for both in-cluster and local testing scenarios.
func GetRESTConfig() (*rest.Config, error) {
	// Try in-cluster config first (when running in a pod)
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig file (when running locally)
	var kubeconfig string
	if os.Getenv("KUBECONFIG") != "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	} else if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	} else {
		return nil, fmt.Errorf("failed to get in-cluster config and cannot determine kubeconfig path (KUBECONFIG not set and HOME not available)")
	}

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig %s: %w", kubeconfig, err)
	}

	return config, nil
}

// ExecCommandInPod executes a command in a container within a pod using kubectl exec.
// It returns the stdout and stderr output as strings, or an error if execution fails.
// This is a convenience wrapper around ExecCommandInPodWithWriters that captures output.
func ExecCommandInPod(ctx context.Context, client kubernetes.Interface, restConfig *rest.Config,
	namespace, podName, containerName string, command []string) (stdout, stderr string, err error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	err = ExecCommandInPodWithWriters(
		ctx, client, restConfig, namespace, podName, containerName,
		command, &stdoutBuf, &stderrBuf,
	)
	return stdoutBuf.String(), stderrBuf.String(), err
}

// ExecCommandInPodWithWriters executes a command in a container within a pod using kubectl exec.
// stdout and stderr writers can be nil, in which case output will be discarded.
// This function uses the Kubernetes client-go remotecommand API to execute commands.
func ExecCommandInPodWithWriters(ctx context.Context, client kubernetes.Interface, restConfig *rest.Config,
	namespace, podName, containerName string, command []string, stdout, stderr io.Writer) error {
	// Build the exec request
	execReq := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		Param("container", containerName).
		Param("stdout", "true").
		Param("stderr", "true")

	// Add each command part as a separate "command" parameter
	for _, cmd := range command {
		execReq = execReq.Param("command", cmd)
	}

	// Create the executor
	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", execReq.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	// Set up output writers (use io.Discard if nil)
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	// Execute the command
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	return nil
}
