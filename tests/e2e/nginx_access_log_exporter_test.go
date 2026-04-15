package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("NGINX Access-Log-Exporter Integration", Label("nginx", "monitoring"), Ordered, Serial, func() {

	var (
		nginxClient   *http.Client
		metricsClient *http.Client
		metricsURL    string
	)

	// Shared setup: Helm + HTTP clients once for all nested Describes
	BeforeAll(func() {
		nexusConfig := testhelpers.NewNexusConfig()

		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			ReplicaCount: int(suiteReplicaCount),
			Nginx: &testhelpers.NginxValues{
				Enabled: true,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: nexusConfig.URL,
				},
			},
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to configure squid with nginx for access-log-exporter tests")

		nginxClient = testhelpers.NewNginxClient()
		metricsClient = &http.Client{
			Timeout: 10 * time.Second,
		}
		metricsURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:9113/metrics",
			testhelpers.NginxServiceName, namespace)
	})

	Describe("Deployment Configuration", func() {
		It("should deploy NGINX StatefulSet with access-log-exporter sidecar", func() {
			statefulSet, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, testhelpers.NginxStatefulSetName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get NGINX statefulset")

			// Verify access-log-exporter container exists
			var exporterContainer *v1.Container
			for i := range statefulSet.Spec.Template.Spec.Containers {
				if statefulSet.Spec.Template.Spec.Containers[i].Name == "access-log-exporter" {
					exporterContainer = &statefulSet.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(exporterContainer).NotTo(BeNil(), "access-log-exporter sidecar container should exist")

			// Verify container configuration
			Expect(exporterContainer.Ports).To(HaveLen(1), "access-log-exporter should have metrics port")
			Expect(exporterContainer.Ports[0].ContainerPort).To(Equal(int32(9113)), "metrics port should be 9113")
			Expect(exporterContainer.Ports[0].Name).To(Equal("metrics"), "port should be named 'metrics'")

			// Verify volume mounts for config (TLS mount is only present when nginx.tls.enabled=true)
			volumeMounts := make(map[string]string)
			for _, vm := range exporterContainer.VolumeMounts {
				volumeMounts[vm.Name] = vm.MountPath
			}
			Expect(volumeMounts).To(HaveKey("exporter-config"), "should mount exporter config")
			Expect(volumeMounts["exporter-config"]).To(Equal("/etc/exporter"), "config should be at /etc/exporter")
		})

		It("should expose metrics endpoint through service", func() {
			service, err := clientset.CoreV1().Services(namespace).Get(ctx, testhelpers.NginxServiceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get NGINX service")

			// Verify service has metrics port
			var metricsPort *v1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "metrics" {
					metricsPort = &service.Spec.Ports[i]
					break
				}
			}
			Expect(metricsPort).NotTo(BeNil(), "Service should have metrics port")
			Expect(metricsPort.Port).To(Equal(int32(9113)), "metrics port should be 9113")
			Expect(metricsPort.TargetPort).To(Equal(intstr.FromString("metrics")), "should target metrics port")
			Expect(metricsPort.Protocol).To(Equal(v1.ProtocolTCP), "should use TCP")
		})
	})

	Describe("Syslog Message Reception", func() {
		It("should receive syslog messages from NGINX access logs", func() {
			By("Making requests through NGINX to generate access logs")
			// Make multiple requests to ensure metrics are populated
			for i := 0; i < 3; i++ {
				url := testhelpers.GetNginxURL() + fmt.Sprintf("/service/rest/v1/status?iteration=%d", i)
				resp, err := nginxClient.Get(url)
				Expect(err).NotTo(HaveOccurred())
				resp.Body.Close()
			}

			By("Verifying access-log-exporter received and processed syslog messages by checking metrics")
			// The presence of http_requests_total with values proves syslog messages
			// were received from NGINX and processed by access-log-exporter
			Eventually(func() bool {
				resp, err := metricsClient.Get(metricsURL)
				if err != nil {
					GinkgoWriter.Printf("Failed to get metrics: %v\n", err)
					return false
				}
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					GinkgoWriter.Printf("Failed to read metrics: %v\n", err)
					return false
				}

				metrics := string(body)

				// Look for http_requests_total metric with actual values
				// This metric is only populated when syslog messages are received and parsed
				value, err := testhelpers.GetMetricValue(metrics, "http_requests_total", nil)
				if err == nil && value > 0 {
					GinkgoWriter.Printf("Found http_requests_total metric with value: %.0f\n", value)
					return true
				}

				return false
			}, testhelpers.Timeout, testhelpers.Interval).Should(BeTrue(),
				"access-log-exporter should receive and process syslog messages from NGINX (evidenced by http_requests_total metric)")
		})
	})

	Describe("Metrics Endpoint", func() {
		It("should return valid Prometheus format metrics", func() {
			Eventually(func() error {
				resp, err := metricsClient.Get(metricsURL)
				if err != nil {
					return fmt.Errorf("failed to get metrics: %w", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("expected status 200, got %d", resp.StatusCode)
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to read response: %w", err)
				}

				metricsContent := string(body)

				// Verify Prometheus content type
				contentType := resp.Header.Get("Content-Type")
				if !strings.Contains(contentType, "text/plain") {
					return fmt.Errorf("expected text/plain content type, got %s", contentType)
				}

				// Validate basic Prometheus format
				lines := strings.Split(metricsContent, "\n")
				hasMetrics := false
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}

					// Metric line should have format: metric_name{labels} value
					if strings.Contains(line, " ") {
						hasMetrics = true
						lastSpaceIdx := strings.LastIndex(line, " ")
						valueStr := strings.TrimSpace(line[lastSpaceIdx+1:])
						if _, err := strconv.ParseFloat(valueStr, 64); err != nil {
							return fmt.Errorf("invalid metric value in line: %s", line)
						}
					}
				}

				if !hasMetrics {
					return fmt.Errorf("no valid metric lines found")
				}

				return nil
			}, testhelpers.Timeout, testhelpers.Interval).Should(Succeed(),
				"Metrics endpoint should return valid Prometheus format")
		})

		It("should expose HTTP request metrics", func() {
			Eventually(func() bool {
				resp, err := metricsClient.Get(metricsURL)
				if err != nil {
					return false
				}
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return false
				}

				metricsContent := string(body)

				// Check for expected nginx/access-log-exporter metrics
				// These metrics come from parsing NGINX access logs and stub_status
				expectedMetrics := []string{
					"nginx_up",                            // Availability (from stub_status)
					"http_requests_total",                 // Cache hit/miss ratio (cache_status label)
					"http_request_duration_seconds",       // Request latency (status, method labels)
					"http_upstream_response_time_seconds", // Upstream health (status, method labels)
				}

				foundCount := 0
				for _, metric := range expectedMetrics {
					if strings.Contains(metricsContent, metric) {
						foundCount++
					}
				}

				// Expect all standard metrics to be present
				return foundCount == len(expectedMetrics)
			}, testhelpers.Timeout, testhelpers.Interval).Should(BeTrue(),
				"Should expose HTTP request metrics from access logs")
		})
	})

	Describe("Metrics Accuracy", func() {
		getMetrics := func() (string, error) {
			resp, err := metricsClient.Get(metricsURL)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("metrics endpoint returned status %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", err
			}

			return string(body), nil
		}

		It("should accurately track request counts", func() {
			By("Getting initial metrics")
			initialMetrics, err := getMetrics()
			Expect(err).NotTo(HaveOccurred())

			// Try to find initial request count (may not exist yet)
			initialCount := 0.0
			val, err := testhelpers.GetMetricValue(initialMetrics, "http_requests_total", map[string]string{"status": "200"})
			if err == nil {
				initialCount = val
			}

			By("Making 5 requests through NGINX")
			successCount := 0
			for i := 0; i < 5; i++ {
				url := testhelpers.GetNginxURL() + fmt.Sprintf("/service/rest/v1/status?req=%d", i)
				resp, err := nginxClient.Get(url)
				if err == nil {
					if resp.StatusCode == http.StatusOK {
						successCount++
					}
					resp.Body.Close()
				}
			}
			Expect(successCount).To(Equal(5), "All requests should succeed")

			By("Verifying metrics reflect the actual request count")
			Eventually(func() bool {
				updatedMetrics, err := getMetrics()
				if err != nil {
					GinkgoWriter.Printf("Failed to get metrics: %v\n", err)
					return false
				}

				updatedCount, err := testhelpers.GetMetricValue(updatedMetrics, "http_requests_total", map[string]string{"status": "200"})
				if err != nil {
					GinkgoWriter.Printf("Failed to parse metric: %v\n", err)
					return false
				}

				expectedIncrease := float64(successCount)
				actualIncrease := updatedCount - initialCount

				GinkgoWriter.Printf("Initial count: %.0f, Updated count: %.0f, Expected increase: %.0f, Actual increase: %.0f\n",
					initialCount, updatedCount, expectedIncrease, actualIncrease)

				// Allow some tolerance for other background requests
				return actualIncrease >= expectedIncrease
			}, testhelpers.Timeout, testhelpers.Interval).Should(BeTrue(),
				"Request count metrics should increase by at least the number of requests made")
		})

		It("should track different HTTP status codes separately", func() {
			By("Making requests that produce different status codes")

			// Successful request (200)
			url200 := testhelpers.GetNginxURL() + "/service/rest/v1/status"
			resp, err := nginxClient.Get(url200)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			resp.Body.Close()

			// Not found request (404)
			url404 := testhelpers.GetNginxURL() + "/nonexistent-path-12345"
			resp, err = nginxClient.Get(url404)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
			resp.Body.Close()

			By("Verifying metrics contain both status codes")
			Eventually(func() bool {
				metrics, err := getMetrics()
				if err != nil {
					return false
				}

				// Look for metrics with both status codes
				has200 := strings.Contains(metrics, `status="200"`)
				has404 := strings.Contains(metrics, `status="404"`)

				GinkgoWriter.Printf("Metrics contain status=\"200\": %v, status=\"404\": %v\n", has200, has404)

				return has200 && has404
			}, testhelpers.Timeout, testhelpers.Interval).Should(BeTrue(),
				"Metrics should track different HTTP status codes separately")
		})

		It("should track request duration metrics", func() {
			By("Making a request through NGINX")
			url := testhelpers.GetNginxURL() + "/service/rest/v1/status"
			resp, err := nginxClient.Get(url)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			By("Verifying duration metrics are present")
			Eventually(func() error {
				metrics, err := getMetrics()
				if err != nil {
					return fmt.Errorf("failed to get metrics: %w", err)
				}

				// Use proper metric parsing to check for http_request_duration_seconds
				_, err = testhelpers.GetMetricValue(metrics, "http_request_duration_seconds", nil)
				if err != nil {
					return fmt.Errorf("http_request_duration_seconds metric not found: %w", err)
				}

				GinkgoWriter.Printf("Found http_request_duration_seconds metric\n")
				return nil
			}, testhelpers.Timeout, testhelpers.Interval).Should(Succeed(),
				"Should expose request duration metrics")
		})
	})

	Describe("Metrics Format Compliance", func() {
		It("should include HELP and TYPE metadata for metrics", func() {
			resp, err := metricsClient.Get(metricsURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())

			metricsContent := string(body)

			// Check for proper Prometheus metadata
			hasHelp := strings.Contains(metricsContent, "# HELP ")
			hasType := strings.Contains(metricsContent, "# TYPE ")

			Expect(hasHelp).To(BeTrue(), "Metrics should include HELP comments")
			Expect(hasType).To(BeTrue(), "Metrics should include TYPE comments")

			// Verify TYPE values are valid
			lines := strings.Split(metricsContent, "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "# TYPE ") {
					parts := strings.SplitN(line, " ", 4)
					Expect(len(parts)).To(Equal(4), "TYPE comment should have correct format")

					validTypes := []string{"counter", "gauge", "histogram", "summary", "untyped"}
					Expect(validTypes).To(ContainElement(parts[3]),
						fmt.Sprintf("TYPE %s should be valid Prometheus type", parts[3]))
				}
			}
		})
	})
})
