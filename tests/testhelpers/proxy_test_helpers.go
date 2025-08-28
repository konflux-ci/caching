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
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/gomega"
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
func ValidateServerHit(response *TestServerResponse, expectedRequestID float64, server *ProxyTestServer) {
	Expect(response.RequestID).To(Equal(expectedRequestID),
		"Request should have expected request ID")
	Expect(server.GetRequestCount()).To(Equal(int32(expectedRequestID)),
		"Server should have received expected number of requests")
}

// WaitForSquidDeploymentReady waits for squid deployment to be ready and only one squid pod to be present
func WaitForSquidDeploymentReady(ctx context.Context, client kubernetes.Interface) error {
	fmt.Printf("Waiting for squid deployment to be ready...\n")

	Eventually(func() error {
		deployments, err := client.AppsV1().Deployments("proxy").List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=squid,app.kubernetes.io/component=squid-proxy",
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
		pods, err := client.CoreV1().Pods("proxy").List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=squid,app.kubernetes.io/component=squid-proxy",
		})
		if err != nil {
			return fmt.Errorf("failed to get pods: %w", err)
		}

		if len(pods.Items) != 1 {
			return fmt.Errorf("expected 1 pod, got %d", len(pods.Items))
		}

		return nil
	}, 120*time.Second, 5*time.Second).Should(Succeed())

	return nil
}

type SquidHelmValues struct {
	CacheAllowList    []string
	OutgoingTLSCAFile string
}

// ConfigureSquidWithHelm configures Squid deployment using helm values
// This replaces the old SquidConfigManager approach for unified configuration management
func ConfigureSquidWithHelm(ctx context.Context, client kubernetes.Interface, customValues SquidHelmValues) error {
	values := map[string]string{
		"environment": "dev",
	}

	// Configure cache allow list
	if len(customValues.CacheAllowList) > 0 {
		values["cache.allowList"] = FormatHelmListValue(customValues.CacheAllowList)
	}

	// Configure SSL bump TLS trust
	if customValues.OutgoingTLSCAFile != "" {
		values["tlsOutgoingOptions.caFile"] = customValues.OutgoingTLSCAFile
	}

	err := UpgradeChart("squid", "./squid", values)
	if err != nil {
		return fmt.Errorf("failed to upgrade squid with helm: %w", err)
	}

	err = WaitForSquidDeploymentReady(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to wait for squid deployment to be ready: %w", err)
	}

	return nil
}

// FormatHelmListValue formats a list of strings into a comma-separated string for helm --set
func FormatHelmListValue(items []string) string {
	value := "{"
	for i, item := range items {
		if i > 0 {
			value += ","
		}
		value += item
	}
	value += "}"
	return value
}

// UpgradeChart performs a helm upgrade with the specified chart and values
func UpgradeChart(releaseName, chartName string, values map[string]string) error {
	fmt.Printf("Upgrading helm release '%s' with chart '%s'...\n", releaseName, chartName)

	// Build helm command as a shell string
	cmdParts := []string{"helm", "upgrade", releaseName, chartName, "-n=default", "--wait", "--timeout=120s"}

	// Add --set parameters for each value in the map
	for key, value := range values {
		cmdParts = append(cmdParts, "--set", fmt.Sprintf("%s=%s", key, value))
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
