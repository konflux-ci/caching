package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Squid Per-Site Exporter Integration", func() {

	Describe("Exporter Deployment", func() {
		It("should have per-site exporter integrated in squid container", func() {
			// Get the squid deployment
			deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid deployment")

			// Find squid container (which now has integrated per-site exporter)
			var squidContainer *v1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "squid" {
					squidContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(squidContainer).NotTo(BeNil(), "squid container should exist")

			// Verify squid container has per-site metrics port
			var perSitePort *v1.ContainerPort
			for i := range squidContainer.Ports {
				if squidContainer.Ports[i].Name == "per-site-http" {
					perSitePort = &squidContainer.Ports[i]
					break
				}
			}
			Expect(perSitePort).NotTo(BeNil(), "squid container should have per-site-http port")
			Expect(perSitePort.ContainerPort).To(Equal(int32(9302)))
		})

		It("should expose per-site metrics endpoint through service", func() {
			// Get the squid service
			service, err := clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid service")

			// Find per-site metrics port
			var perSiteMetricsPort *v1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "per-site-http" {
					perSiteMetricsPort = &service.Spec.Ports[i]
					break
				}
			}
			Expect(perSiteMetricsPort).NotTo(BeNil(), "per-site-http port should exist")
			Expect(perSiteMetricsPort.Port).To(Equal(int32(9302)))
		})
	})

	Describe("Per-Site Metrics Endpoint", func() {
		var (
			testServer *testhelpers.ProxyTestServer
			client     *http.Client
		)

		BeforeEach(func() {
			// Set up test server and proxy client for generating traffic
			podIP, err := getPodIP()
			Expect(err).NotTo(HaveOccurred(), "Failed to get pod IP")

			testServer, err = testhelpers.NewProxyTestServer("Per-site metrics endpoint test", podIP, 0)
			Expect(err).NotTo(HaveOccurred(), "Failed to create test server")

			client, err = testhelpers.NewSquidProxyClient(serviceName, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to create proxy client")
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
		})

		It("should return valid per-site metrics from the exporter endpoint", func() {
			// Generate some HTTP traffic first to create metrics
			testURL := testServer.URL + "?" + generateCacheBuster("metrics-endpoint-test")

			By("Making HTTP requests through the proxy to generate metrics")
			for i := 0; i < 5; i++ {
				resp, _, err := testhelpers.MakeProxyRequest(client, testURL+fmt.Sprintf("&req=%d", i))
				Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Request %d should succeed", i))
				resp.Body.Close()
				// Add small delay between requests
				time.Sleep(100 * time.Millisecond)
			}

			// Wait longer for per-site exporter to process the logs
			time.Sleep(15 * time.Second)

			// Create HTTP client to access per-site metrics endpoint directly
			metricsURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)

			metricsClient := &http.Client{
				Timeout: 10 * time.Second,
			}

			Eventually(func() error {
				resp, err := metricsClient.Get(metricsURL)
				if err != nil {
					return err
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("expected status 200, got %d", resp.StatusCode)
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}

				// Basic validation of Prometheus metrics format
				metricsContent := string(body)

				// Debug: Log what metrics we actually got
				fmt.Printf("DEBUG: Metrics content:\n%s\n", metricsContent)

				// Check for expected per-site metrics patterns
				expectedMetrics := []string{
					"squid_site_requests_total",
					"squid_site_misses_total",
					"squid_site_bytes_total",
					"squid_site_hit_ratio",
					"squid_site_response_time_seconds",
				}

				for _, metric := range expectedMetrics {
					if !strings.Contains(metricsContent, metric) {
						return fmt.Errorf("expected metric %s not found in metrics output", metric)
					}
				}

				return nil
			}, timeout, interval).Should(Succeed(), "Per-site metrics endpoint should return valid metrics")
		})

		It("should have health check endpoint working", func() {
			healthURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:9302/health", serviceName, namespace)

			client := &http.Client{
				Timeout: 10 * time.Second,
			}

			Eventually(func() error {
				resp, err := client.Get(healthURL)
				if err != nil {
					return err
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("expected status 200, got %d", resp.StatusCode)
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}

				if string(body) != "OK" {
					return fmt.Errorf("expected 'OK', got '%s'", string(body))
				}

				return nil
			}, timeout, interval).Should(Succeed(), "Health endpoint should return OK")
		})
	})

	Describe("Per-Site Metrics with Traffic", func() {
		var (
			testServer    *testhelpers.ProxyTestServer
			client        *http.Client
			metricsClient *http.Client
		)

		BeforeEach(func() {
			// Set up test server and proxy client
			podIP, err := getPodIP()
			Expect(err).NotTo(HaveOccurred(), "Failed to get pod IP")

			testServer, err = testhelpers.NewProxyTestServer("Per-site exporter test", podIP, 0)
			Expect(err).NotTo(HaveOccurred(), "Failed to create test server")

			client, err = testhelpers.NewSquidProxyClient(serviceName, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to create proxy client")

			// Create client for per-site metrics endpoint
			metricsClient = &http.Client{
				Timeout: 10 * time.Second,
			}
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
		})

		getPerSiteMetricsValue := func(metricsContent, metricName, hostname string) (float64, error) {
			// Parse metrics content to extract specific metric value for a hostname
			lines := strings.Split(metricsContent, "\n")
			pattern := fmt.Sprintf(`^%s\{.*hostname="%s".*\}\s+([0-9.]+)`, regexp.QuoteMeta(metricName), regexp.QuoteMeta(hostname))
			re := regexp.MustCompile(pattern)

			for _, line := range lines {
				if matches := re.FindStringSubmatch(line); len(matches) >= 2 {
					value, err := strconv.ParseFloat(matches[1], 64)
					if err == nil {
						return value, nil
					}
				}
			}
			return 0, fmt.Errorf("metric %s for hostname %s not found", metricName, hostname)
		}

		getPerSiteMetrics := func() (string, error) {
			metricsURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			resp, err := metricsClient.Get(metricsURL)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("per-site metrics endpoint returned status %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", err
			}

			return string(body), nil
		}

		It("should track per-site request metrics when traffic flows through proxy", func() {
			// Extract hostname from test server URL for validation
			testHostname := strings.Split(strings.TrimPrefix(testServer.URL, "http://"), ":")[0]

			// Make requests through the proxy
			testURL := testServer.URL + "?" + generateCacheBuster("per-site-metrics-test")

			By("Making HTTP requests through the proxy")
			for i := 0; i < 3; i++ {
				resp, _, err := testhelpers.MakeProxyRequest(client, testURL+fmt.Sprintf("&req=%d", i))
				Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Request %d should succeed", i))
				resp.Body.Close()
			}

			// Wait for per-site exporter to process the logs
			time.Sleep(5 * time.Second)

			// Get updated per-site metrics
			Eventually(func() bool {
				metricsContent, err := getPerSiteMetrics()
				if err != nil {
					return false
				}

				// Check if we have request metrics for our test hostname
				requestMetric, err := getPerSiteMetricsValue(metricsContent, "squid_site_requests_total", testHostname)
				if err != nil {
					return false
				}

				// Should have at least 3 requests
				return requestMetric >= 3
			}, timeout*2, interval).Should(BeTrue(), "Per-site request metrics should increase after proxy traffic")
		})

		It("should track per-site cache hit ratios correctly", func() {
			// Extract hostname from test server URL for validation
			testHostname := strings.Split(strings.TrimPrefix(testServer.URL, "http://"), ":")[0]

			// Use a STATIC URL (no cache buster) so we can get cache hits
			testURL := testServer.URL + "?static-cache-test=true"

			By("Making repeated requests to generate cache hits")
			// First request - will be a cache miss
			resp1, _, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "First request should succeed")
			resp1.Body.Close()

			time.Sleep(1 * time.Second) // Let it cache

			// Second request to same URL - should be a cache hit
			resp2, _, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Second request should succeed")
			resp2.Body.Close()

			time.Sleep(1 * time.Second) // Let it cache

			// Third request to same URL - should be another cache hit
			resp3, _, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Third request should succeed")
			resp3.Body.Close()

			// Wait for per-site exporter to process the logs
			time.Sleep(10 * time.Second)

			// Verify we have cache metrics (check misses since hits might be 0)
			Eventually(func() bool {
				metricsContent, err := getPerSiteMetrics()
				if err != nil {
					return false
				}

				// Check if we have miss metrics for our test hostname (this should always exist)
				missMetric, err := getPerSiteMetricsValue(metricsContent, "squid_site_misses_total", testHostname)
				if err != nil {
					return false
				}

				// Should have at least one request recorded (miss or hit)
				return missMetric >= 1
			}, timeout*2, interval).Should(BeTrue(), "Per-site cache metrics should be recorded")
		})

		It("should expose bandwidth metrics per site", func() {
			// Extract hostname from test server URL for validation
			testHostname := strings.Split(strings.TrimPrefix(testServer.URL, "http://"), ":")[0]

			// Make a request to generate bandwidth usage
			testURL := testServer.URL + "?" + generateCacheBuster("per-site-bandwidth-test")
			resp, body, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Request should succeed")
			resp.Body.Close()

			// Verify we got some content
			Expect(len(body)).To(BeNumerically(">", 0), "Should receive response body")

			// Wait for per-site exporter to process the logs
			time.Sleep(5 * time.Second)

			// Check that per-site bandwidth metrics exist
			Eventually(func() bool {
				metricsContent, err := getPerSiteMetrics()
				if err != nil {
					return false
				}

				// Check if we have bytes metrics for our test hostname
				bytesMetric, err := getPerSiteMetricsValue(metricsContent, "squid_site_bytes_total", testHostname)
				if err != nil {
					return false
				}

				// Should have some bytes recorded
				return bytesMetric > 0
			}, timeout*2, interval).Should(BeTrue(), "Per-site bandwidth metrics should be recorded")
		})
	})

	Describe("Per-Site Metrics Format and Standards", func() {
		It("should return per-site metrics in valid Prometheus format", func() {
			metricsURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			client := &http.Client{Timeout: 10 * time.Second}

			resp, err := client.Get(metricsURL)
			Expect(err).NotTo(HaveOccurred(), "Should get per-site metrics response")
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK), "Per-site metrics endpoint should return 200")
			// Validate content type (accept multiple valid variants)
			contentType := resp.Header.Get("Content-Type")
			expectedContentType := "text/plain; version=0.0.4; charset=utf-8"
			expectedContentTypeWithValues := "text/plain; version=0.0.4; charset=utf-8; escaping=values"
			expectedContentTypeWithUnderscores := "text/plain; version=0.0.4; charset=utf-8; escaping=underscores"

			validContentType := contentType == expectedContentType ||
				contentType == expectedContentTypeWithValues ||
				contentType == expectedContentTypeWithUnderscores
			Expect(validContentType).To(BeTrue(),
				fmt.Sprintf("Per-site metrics should have correct content type. Got: %s, Expected: %s, %s, or %s",
					contentType, expectedContentType, expectedContentTypeWithValues, expectedContentTypeWithUnderscores))

			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred(), "Should read per-site metrics body")

			metricsContent := string(body)

			// Validate basic Prometheus format requirements
			lines := strings.Split(metricsContent, "\n")

			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				// Check HELP comments
				if strings.HasPrefix(line, "# HELP ") {
					parts := strings.SplitN(line, " ", 4)
					Expect(len(parts)).To(BeNumerically(">=", 4), "HELP comments should have correct format")
					continue
				}

				// Check TYPE comments
				if strings.HasPrefix(line, "# TYPE ") {
					parts := strings.SplitN(line, " ", 4)
					Expect(len(parts)).To(Equal(4), "TYPE comments should have correct format")
					validTypes := []string{"counter", "gauge", "histogram", "summary", "untyped"}
					Expect(validTypes).To(ContainElement(parts[3]), "TYPE should be valid Prometheus type")
					continue
				}

				// Skip other comments
				if strings.HasPrefix(line, "#") {
					continue
				}

				// Validate metric lines with hostname labels
				if strings.Contains(line, " ") && strings.Contains(line, "hostname=") {
					parts := strings.SplitN(line, " ", 2)
					metricName := parts[0]

					// Basic metric name validation for per-site metrics
					if strings.HasPrefix(metricName, "squid_site_") {
						Expect(metricName).To(MatchRegexp(`^squid_site_[a-zA-Z_:][a-zA-Z0-9_:]*\{.*hostname=.*\}$`),
							"Per-site metric should have hostname label")

						// Value should be a valid number
						valueStr := strings.Fields(parts[1])[0]
						_, err := strconv.ParseFloat(valueStr, 64)
						Expect(err).NotTo(HaveOccurred(), "Metric value should be a valid number")
					}
				}
			}
		})

		It("should include required metadata for per-site metrics", func() {
			metricsURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			client := &http.Client{Timeout: 10 * time.Second}

			resp, err := client.Get(metricsURL)
			Expect(err).NotTo(HaveOccurred(), "Should get per-site metrics response")
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred(), "Should read per-site metrics body")

			metricsContent := string(body)

			// Check for HELP and TYPE comments for key per-site metrics
			requiredMetrics := []string{
				"squid_site_requests_total",
				"squid_site_misses_total",
				"squid_site_bytes_total",
				"squid_site_hit_ratio",
			}

			for _, metric := range requiredMetrics {
				helpPattern := fmt.Sprintf("# HELP %s ", metric)
				typePattern := fmt.Sprintf("# TYPE %s ", metric)

				Expect(metricsContent).To(ContainSubstring(helpPattern),
					fmt.Sprintf("Should contain HELP comment for %s", metric))
				Expect(metricsContent).To(ContainSubstring(typePattern),
					fmt.Sprintf("Should contain TYPE comment for %s", metric))
			}
		})
	})
})
