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
	"strings"
	"sync/atomic"

	"time"

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

// ProxyTestServer wraps an HTTP test server with request counting and proxy-friendly configuration
type ProxyTestServer struct {
	*httptest.Server
	RequestCount *int32
	PodIP        string
	URL          string
}

// NewProxyTestServer creates a new test server configured for cross-pod communication
func NewProxyTestServer(message string, podIP string, port int) (*ProxyTestServer, error) {
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

	return &ProxyTestServer{
		Server:       server,
		RequestCount: &requestCount,
		PodIP:        podIP,
		URL:          serverURL,
	}, nil
}

// GetRequestCount returns the current request count
func (pts *ProxyTestServer) GetRequestCount() int32 {
	return atomic.LoadInt32(pts.RequestCount)
}

// ResetRequestCount resets the request counter to zero
func (pts *ProxyTestServer) ResetRequestCount() {
	atomic.StoreInt32(pts.RequestCount, 0)
}

// NewSquidProxyClient creates an HTTP client configured to use the Squid proxy
func NewSquidProxyClient(serviceName, namespace string) (*http.Client, error) {
	// Set up proxy URL to squid service
	proxyURL, err := url.Parse(fmt.Sprintf("http://%s.%s.svc.cluster.local:3128", serviceName, namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy URL: %w", err)
	}

	// Create HTTP client with proxy configuration
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		// Disable keep-alive to ensure fresh connections for cache testing
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

// NewTrustedSquidProxyClient creates an HTTP client configured to use the Squid proxy and trust both the Squid CA and test-server CA
func NewTrustedSquidProxyClient(serviceName, namespace string, squidCACertPEM []byte, testServerCACertPEM []byte) (*http.Client, error) {
	// Set up proxy URL to squid service
	proxyURL, err := url.Parse(fmt.Sprintf("http://%s.%s.svc.cluster.local:3128", serviceName, namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy URL: %w", err)
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

	// Create HTTP client with proxy configuration and trusted TLS
	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: tlsConfig,
		// Disable keep-alive to ensure fresh connections for cache testing
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

// MakeProxyRequest makes an HTTP request through the Squid proxy and returns the response
func MakeProxyRequest(client *http.Client, url string) (*http.Response, []byte, error) {
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
func ValidateServerHit(response *TestServerResponse, expectedRequestID float64, server *ProxyTestServer) {
	Expect(response.RequestID).To(Equal(expectedRequestID),
		"Request should have expected request ID")
	Expect(server.GetRequestCount()).To(Equal(int32(expectedRequestID)),
		"Server should have received expected number of requests")
}

// SquidConfigManager handles squid configuration management for tests
type SquidConfigManager struct {
	k8sClient          kubernetes.Interface
	namespace          string
	configMapName      string
	deploymentName     string
	originalConfigData map[string]string
	configModified     bool
	requiredTLSLine    string
	anchorLine         string
}

// NewSquidConfigManager creates a new squid configuration manager
func NewSquidConfigManager(k8sClient kubernetes.Interface, namespace, configMapName, deploymentName string) *SquidConfigManager {
	return &SquidConfigManager{
		k8sClient:       k8sClient,
		namespace:       namespace,
		configMapName:   configMapName,
		deploymentName:  deploymentName,
		requiredTLSLine: "tls_outgoing_options cafile=/etc/squid/trust/test-server/ca.crt",
		anchorLine:      "ssl_bump bump all",
		configModified:  false,
	}
}

// GetSquidConfigMap retrieves the current squid ConfigMap
func (scm *SquidConfigManager) GetSquidConfigMap(ctx context.Context) (*corev1.ConfigMap, error) {
	fmt.Printf("DEBUG: Getting squid ConfigMap: %s/%s\n", scm.namespace, scm.configMapName)
	configMap, err := scm.k8sClient.CoreV1().ConfigMaps(scm.namespace).Get(ctx, scm.configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get squid ConfigMap %s/%s: %w", scm.namespace, scm.configMapName, err)
	}
	fmt.Printf("DEBUG: Successfully retrieved squid ConfigMap\n")
	return configMap, nil
}

// HasRequiredTLSConfig checks if the required TLS configuration line exists in squid.conf
func (scm *SquidConfigManager) HasRequiredTLSConfig(ctx context.Context) (bool, error) {
	configMap, err := scm.GetSquidConfigMap(ctx)
	if err != nil {
		return false, err
	}

	squidConf, exists := configMap.Data["squid.conf"]
	if !exists {
		return false, fmt.Errorf("squid.conf not found in ConfigMap %s", scm.configMapName)
	}

	hasConfig := strings.Contains(squidConf, scm.requiredTLSLine)
	fmt.Printf("DEBUG: Required TLS config present: %t\n", hasConfig)
	return hasConfig, nil
}

// EnsureRequiredTLSConfig ensures the required TLS configuration is present, adding it if necessary
func (scm *SquidConfigManager) EnsureRequiredTLSConfig(ctx context.Context) error {
	// Always store original config first for reliable cleanup, regardless of whether we modify it
	if scm.originalConfigData == nil {
		configMap, err := scm.GetSquidConfigMap(ctx)
		if err != nil {
			return err
		}
		scm.originalConfigData = make(map[string]string)
		for k, v := range configMap.Data {
			scm.originalConfigData[k] = v
		}
		fmt.Printf("DEBUG: Stored original ConfigMap data for potential restoration\n")
	}

	hasConfig, err := scm.HasRequiredTLSConfig(ctx)
	if err != nil {
		return err
	}

	if hasConfig {
		fmt.Printf("DEBUG: Required TLS configuration already present, no changes needed\n")
		return nil
	}

	fmt.Printf("DEBUG: Required TLS configuration missing, adding it\n")
	return scm.addTLSConfigToSquidConf(ctx)
}

// addTLSConfigToSquidConf adds the required TLS configuration line after the anchor line
func (scm *SquidConfigManager) addTLSConfigToSquidConf(ctx context.Context) error {
	configMap, err := scm.GetSquidConfigMap(ctx)
	if err != nil {
		return err
	}

	// Original config should already be stored by EnsureRequiredTLSConfig

	squidConf, exists := configMap.Data["squid.conf"]
	if !exists {
		return fmt.Errorf("squid.conf not found in ConfigMap %s", scm.configMapName)
	}

	// Print original configmap content
	fmt.Printf("DEBUG: Original squid.conf content BEFORE modification:\n")
	fmt.Printf("==========================================\n")
	fmt.Printf("%s\n", squidConf)
	fmt.Printf("==========================================\n")

	// Find the anchor line and add the TLS config after it
	lines := strings.Split(squidConf, "\n")
	var newLines []string
	anchorFound := false

	for _, line := range lines {
		newLines = append(newLines, line)
		if strings.TrimSpace(line) == scm.anchorLine {
			anchorFound = true
			newLines = append(newLines, scm.requiredTLSLine)
			fmt.Printf("DEBUG: Added TLS config line after anchor: %s\n", scm.anchorLine)
		}
	}

	if !anchorFound {
		return fmt.Errorf("anchor line '%s' not found in squid.conf", scm.anchorLine)
	}

	// Update the ConfigMap with modified content
	modifiedSquidConf := strings.Join(newLines, "\n")
	configMap.Data["squid.conf"] = modifiedSquidConf

	// Print modified configmap content
	fmt.Printf("DEBUG: Modified squid.conf content AFTER modification:\n")
	fmt.Printf("==========================================\n")
	fmt.Printf("%s\n", modifiedSquidConf)
	fmt.Printf("==========================================\n")

	_, err = scm.k8sClient.CoreV1().ConfigMaps(scm.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update squid ConfigMap: %w", err)
	}

	scm.configModified = true
	fmt.Printf("DEBUG: Successfully updated squid ConfigMap with TLS configuration\n")
	return nil
}

// RestartSquidDeployment restarts the squid deployment to pick up configuration changes
func (scm *SquidConfigManager) RestartSquidDeployment(ctx context.Context) error {
	fmt.Printf("DEBUG: Restarting squid deployment: %s/%s\n", scm.namespace, scm.deploymentName)

	// Get current deployment
	deployment, err := scm.k8sClient.AppsV1().Deployments(scm.namespace).Get(ctx, scm.deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", scm.namespace, scm.deploymentName, err)
	}

	// Store original replica count
	originalReplicas := *deployment.Spec.Replicas
	fmt.Printf("DEBUG: Step 1/4 - Original replica count: %d\n", originalReplicas)

	// Step 1: Scale down to 0
	deployment.Spec.Replicas = &[]int32{0}[0]
	_, err = scm.k8sClient.AppsV1().Deployments(scm.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale down deployment: %w", err)
	}
	fmt.Printf("DEBUG: Step 1/4 - Scaled deployment down to 0 replicas\n")

	// Step 2: Wait for all squid pods to be deleted
	fmt.Printf("DEBUG: Step 2/4 - Waiting for all squid pods to be deleted...\n")
	Eventually(func() error {
		pods, err := scm.k8sClient.CoreV1().Pods(scm.namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=squid,app.kubernetes.io/component!=test",
		})
		if err != nil {
			return fmt.Errorf("failed to list squid pods: %w", err)
		}

		if len(pods.Items) > 0 {
			fmt.Printf("DEBUG: Still have %d squid pods, waiting for deletion...\n", len(pods.Items))
			for i, pod := range pods.Items {
				fmt.Printf("DEBUG:   Pod %d: %s (Status: %s)\n", i+1, pod.Name, pod.Status.Phase)
			}
			return fmt.Errorf("still have %d squid pods", len(pods.Items))
		}
		return nil
	}, 120*time.Second, 3*time.Second).Should(Succeed())
	fmt.Printf("DEBUG: Step 2/4 - All squid pods have been deleted\n")

	// Step 3: Scale back up to original count
	deployment, err = scm.k8sClient.AppsV1().Deployments(scm.namespace).Get(ctx, scm.deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment for scale up: %w", err)
	}
	deployment.Spec.Replicas = &originalReplicas
	_, err = scm.k8sClient.AppsV1().Deployments(scm.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale up deployment: %w", err)
	}
	fmt.Printf("DEBUG: Step 3/4 - Scaled deployment back up to %d replicas\n", originalReplicas)

	// Step 4: Wait for deployment status to be available
	fmt.Printf("DEBUG: Step 4/4 - Waiting for deployment to be ready...\n")
	Eventually(func() error {
		deployment, err = scm.k8sClient.AppsV1().Deployments(scm.namespace).Get(ctx, scm.deploymentName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %w", err)
		}

		if deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
			return fmt.Errorf("deployment not ready: %d/%d replicas ready",
				deployment.Status.ReadyReplicas, *deployment.Spec.Replicas)
		}
		return nil
	}, 180*time.Second, 5*time.Second).Should(Succeed())
	fmt.Printf("DEBUG: Step 4/4 - Deployment is ready\n")
	fmt.Printf("DEBUG: Deployment restart complete!\n")
	return nil
}

// WasConfigModified returns true if the configuration was modified
func (scm *SquidConfigManager) WasConfigModified() bool {
	return scm.configModified
}

// RestoreOriginalConfig restores the original squid configuration if we have a backup
// Returns (wasRestored, error) where wasRestored indicates if ConfigMap was actually updated
func (scm *SquidConfigManager) RestoreOriginalConfig(ctx context.Context) (bool, error) {
	if scm.originalConfigData == nil {
		fmt.Printf("DEBUG: No original configuration backup available\n")
		return false, nil
	}

	fmt.Printf("DEBUG: Restoring original squid configuration (configModified=%t)\n", scm.configModified)

	configMap, err := scm.GetSquidConfigMap(ctx)
	if err != nil {
		return false, err
	}

	// Print current configmap content before restoration
	currentSquidConf, exists := configMap.Data["squid.conf"]
	if exists {
		fmt.Printf("DEBUG: Current squid.conf content BEFORE restoration:\n")
		fmt.Printf("==========================================\n")
		fmt.Printf("%s\n", currentSquidConf)
		fmt.Printf("==========================================\n")
	}

	// Print original configmap content that will be restored
	originalSquidConf, exists := scm.originalConfigData["squid.conf"]
	if exists {
		fmt.Printf("DEBUG: Original squid.conf content TO BE restored:\n")
		fmt.Printf("==========================================\n")
		fmt.Printf("%s\n", originalSquidConf)
		fmt.Printf("==========================================\n")
	}

	// Restore original data
	configMap.Data = scm.originalConfigData

	_, err = scm.k8sClient.CoreV1().ConfigMaps(scm.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to restore original ConfigMap: %w", err)
	}

	fmt.Printf("DEBUG: Successfully restored original squid configuration\n")
	return true, nil
}
