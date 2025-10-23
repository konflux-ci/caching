package testhelpers

import (
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
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/yaml"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// TestServerResponse represents the standard JSON response from test servers
type TestServerResponse struct {
	Message    string  `json:"message"`
	RequestID  float64 `json:"request_id"`
	Timestamp  int64   `json:"timestamp"`
	ServerHits float64 `json:"server_hits"`
}

// CachingTestServer wraps an HTTP test server with request counting and caching-friendly configuration
type CachingTestServer struct {
	*httptest.Server
	RequestCount *int32
	PodIP        string
	URL          string
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
			Timestamp:  time.Now().Unix(),
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

// WaitForSquidDeploymentReady waits for squid deployment to be ready and only one squid pod to be present
func WaitForSquidDeploymentReady(ctx context.Context, client kubernetes.Interface) error {
	fmt.Printf("Waiting for squid deployment to be ready...\n")

	Eventually(func() error {
		deployments, err := client.AppsV1().Deployments("caching").List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=squid,app.kubernetes.io/component=squid-caching",
		})
		if err != nil {
			return fmt.Errorf("failed to get deployments: %w", err)
		}

		if len(deployments.Items) != 1 {
			return fmt.Errorf("expected 1 deployment, got %d", len(deployments.Items))
		}

		deployment := deployments.Items[0]
		if deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
			return fmt.Errorf("deployment not ready: %d/%d replicas ready",
				deployment.Status.ReadyReplicas, *deployment.Spec.Replicas)
		}

		return nil
	}, 120*time.Second, 5*time.Second).Should(Succeed())

	fmt.Printf("Waiting for only one squid pod to be present...\n")
	Eventually(func() error {
		_, err := GetSquidPod(ctx, client, "caching")
		return err
	}, 120*time.Second, 5*time.Second).Should(Succeed())

	return nil
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
}

// ConfigureSquidWithHelm configures Squid deployment using helm values
func ConfigureSquidWithHelm(ctx context.Context, client kubernetes.Interface, values SquidHelmValues) error {
	values.Environment = "dev"
	valuesFile, err := writeValuesToFile(&values)
	if err != nil {
		return fmt.Errorf("failed to write values to file: %w", err)
	}
	defer os.Remove(valuesFile)

	// Use the temporary values file with helm
	err = UpgradeChart("squid", "./squid", valuesFile)
	if err != nil {
		return fmt.Errorf("failed to upgrade squid with helm: %w", err)
	}

	err = WaitForSquidDeploymentReady(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to wait for squid deployment to be ready: %w", err)
	}

	return nil
}

// UpgradeChart performs a helm upgrade with the specified chart and values file
func UpgradeChart(releaseName, chartName string, valuesFile string) error {
	fmt.Printf("Upgrading helm release '%s' with chart '%s'...\n", releaseName, chartName)

	// Build helm command as a shell string
	cmdParts := []string{"helm", "upgrade", releaseName, chartName, "-n=default", "--wait", "--timeout=120s", "--values", valuesFile}

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
	values.Environment = "dev"
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

// GetSquidPod queries for the active squid pod. Returns an error if no or multiple pods are found.
func GetSquidPod(ctx context.Context, client kubernetes.Interface, namespace string) (*corev1.Pod, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=squid,app.kubernetes.io/component=squid-caching",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list squid pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no squid pods found")
	}

	if len(pods.Items) > 1 {
		return nil, fmt.Errorf("%d squid pods found", len(pods.Items))
	}

	return &pods.Items[0], nil
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
