package e2e_test

import (
	"crypto/tls"
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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newHTTPSClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	return &http.Client{Transport: tr, Timeout: timeout}
}

var _ = Describe("Per-Site Exporter", func() {
	Context("Deployment", func() {
		var deployment *appsv1.Deployment

		BeforeEach(func() {
			var err error
			deployment, err = clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid deployment")
		})

		It("should include the per-site-exporter container with TLS and probes", func() {
			// Ensure we have three containers: squid, squid-exporter, per-site-exporter
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(3))

			var perSite *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "per-site-exporter" {
					perSite = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(perSite).NotTo(BeNil(), "per-site-exporter container should exist")

			// Readiness/Liveness probes should target /metrics on the named port
			Expect(perSite.ReadinessProbe).NotTo(BeNil())
			if perSite.ReadinessProbe.HTTPGet != nil {
				Expect(perSite.ReadinessProbe.HTTPGet.Path).To(Equal("/metrics"))
				Expect(perSite.ReadinessProbe.HTTPGet.Port.StrVal).To(Equal("per-site-http"))
			}
			Expect(perSite.LivenessProbe).NotTo(BeNil())
			if perSite.LivenessProbe.HTTPGet != nil {
				Expect(perSite.LivenessProbe.HTTPGet.Path).To(Equal("/metrics"))
				Expect(perSite.LivenessProbe.HTTPGet.Port.StrVal).To(Equal("per-site-http"))
			}

			// It should be configured for HTTPS with cert and key args
			var hasCertArg, hasKeyArg bool
			for _, a := range perSite.Args {
				if strings.Contains(a, "-web.tls-cert-file") {
					hasCertArg = true
				}
				if strings.Contains(a, "-web.tls-key-file") {
					hasKeyArg = true
				}
			}
			Expect(hasCertArg && hasKeyArg).To(BeTrue(), "per-site-exporter should be started with TLS cert and key flags")

			// Ensure certs are mounted
			var hasCertsMount bool
			for _, vm := range perSite.VolumeMounts {
				if vm.MountPath == "/etc/squid/certs" {
					hasCertsMount = true
					break
				}
			}
			Expect(hasCertsMount).To(BeTrue(), "per-site-exporter should mount /etc/squid/certs")
		})
	})

	Context("Service", func() {
		var service *corev1.Service

		BeforeEach(func() {
			var err error
			service, err = clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid service")
		})

		It("should expose the per-site exporter port similar to squid-exporter", func() {
			// Expect 3 ports: http (3128), metrics (9301), per-site-http (9302)
			Expect(service.Spec.Ports).To(HaveLen(3))

			// Validate per-site-http port
			var perSite *corev1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "per-site-http" {
					perSite = &service.Spec.Ports[i]
					break
				}
			}
			Expect(perSite).NotTo(BeNil(), "per-site-http port should exist")
			Expect(perSite.Port).To(Equal(int32(9302)))
			Expect(perSite.TargetPort.StrVal).To(Equal("per-site-http"))
			Expect(perSite.Protocol).To(Equal(corev1.ProtocolTCP))
		})
	})
})

var _ = Describe("Squid Per-Site Exporter Integration", func() {
	Describe("Per-Site Metrics Endpoint", func() {
		var (
			testServer *testhelpers.ProxyTestServer
			client     *http.Client
		)

		BeforeEach(func() {
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
			testURL := testServer.URL + "?" + generateCacheBuster("metrics-endpoint-test")

			By("Making HTTP requests through the proxy to generate metrics")
			for i := 0; i < 5; i++ {
				resp, _, err := testhelpers.MakeProxyRequest(client, testURL+fmt.Sprintf("&req=%d", i))
				Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Request %d should succeed", i))
				resp.Body.Close()
				time.Sleep(100 * time.Millisecond)
			}

			time.Sleep(15 * time.Second)

			metricsURL := fmt.Sprintf("https://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			metricsClient := newHTTPSClient(10 * time.Second)

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
				metricsContent := string(body)

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
			healthURL := fmt.Sprintf("https://%s.%s.svc.cluster.local:9302/health", serviceName, namespace)
			c := newHTTPSClient(10 * time.Second)

			Eventually(func() error {
				resp, err := c.Get(healthURL)
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
			podIP, err := getPodIP()
			Expect(err).NotTo(HaveOccurred(), "Failed to get pod IP")

			var err2 error
			testServer, err2 = testhelpers.NewProxyTestServer("Per-site exporter test", podIP, 0)
			Expect(err2).NotTo(HaveOccurred(), "Failed to create test server")

			client, err2 = testhelpers.NewSquidProxyClient(serviceName, namespace)
			Expect(err2).NotTo(HaveOccurred(), "Failed to create proxy client")

			metricsClient = newHTTPSClient(10 * time.Second)
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
		})

		getPerSiteMetricsValue := func(metricsContent, metricName, hostname string) (float64, error) {
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
			metricsURL := fmt.Sprintf("https://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
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
			testHostname := strings.Split(strings.TrimPrefix(testServer.URL, "http://"), ":")[0]
			testURL := testServer.URL + "?" + generateCacheBuster("per-site-metrics-test")

			By("Making HTTP requests through the proxy")
			for i := 0; i < 3; i++ {
				resp, _, err := testhelpers.MakeProxyRequest(client, testURL+fmt.Sprintf("&req=%d", i))
				Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Request %d should succeed", i))
				resp.Body.Close()
			}

			time.Sleep(5 * time.Second)

			Eventually(func() bool {
				metricsContent, err := getPerSiteMetrics()
				if err != nil {
					return false
				}
				requestMetric, err := getPerSiteMetricsValue(metricsContent, "squid_site_requests_total", testHostname)
				if err != nil {
					return false
				}
				return requestMetric >= 3
			}, timeout*2, interval).Should(BeTrue(), "Per-site request metrics should increase after proxy traffic")
		})

		It("should track per-site cache hit ratios correctly", func() {
			testHostname := strings.Split(strings.TrimPrefix(testServer.URL, "http://"), ":")[0]
			testURL := testServer.URL + "?static-cache-test=true"

			resp1, _, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "First request should succeed")
			resp1.Body.Close()
			time.Sleep(1 * time.Second)

			resp2, _, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Second request should succeed")
			resp2.Body.Close()
			time.Sleep(1 * time.Second)

			resp3, _, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Third request should succeed")
			resp3.Body.Close()

			time.Sleep(10 * time.Second)

			Eventually(func() bool {
				metricsContent, err := getPerSiteMetrics()
				if err != nil {
					return false
				}
				missMetric, err := getPerSiteMetricsValue(metricsContent, "squid_site_misses_total", testHostname)
				if err != nil {
					return false
				}
				return missMetric >= 1
			}, timeout*2, interval).Should(BeTrue(), "Per-site cache metrics should be recorded")
		})

		It("should expose bandwidth metrics per site", func() {
			testHostname := strings.Split(strings.TrimPrefix(testServer.URL, "http://"), ":")[0]
			testURL := testServer.URL + "?" + generateCacheBuster("per-site-bandwidth-test")
			resp, body, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Request should succeed")
			resp.Body.Close()
			Expect(len(body)).To(BeNumerically(">", 0), "Should receive response body")

			time.Sleep(5 * time.Second)

			Eventually(func() bool {
				metricsContent, err := getPerSiteMetrics()
				if err != nil {
					return false
				}
				bytesMetric, err := getPerSiteMetricsValue(metricsContent, "squid_site_bytes_total", testHostname)
				if err != nil {
					return false
				}
				return bytesMetric > 0
			}, timeout*2, interval).Should(BeTrue(), "Per-site bandwidth metrics should be recorded")
		})
	})

	Describe("Per-Site Metrics Format and Standards", func() {
		It("should return per-site metrics in valid Prometheus format", func() {
			metricsURL := fmt.Sprintf("https://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			c := newHTTPSClient(10 * time.Second)

			resp, err := c.Get(metricsURL)
			Expect(err).NotTo(HaveOccurred(), "Should get per-site metrics response")
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "Per-site metrics endpoint should return 200")

			contentType := resp.Header.Get("Content-Type")
			expected := []string{
				"text/plain; version=0.0.4; charset=utf-8",
				"text/plain; version=0.0.4; charset=utf-8; escaping=values",
				"text/plain; version=0.0.4; charset=utf-8; escaping=underscores",
			}
			Expect(expected).To(ContainElement(contentType))

			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred(), "Should read per-site metrics body")
			metricsContent := string(body)

			lines := strings.Split(metricsContent, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "# HELP ") {
					parts := strings.SplitN(line, " ", 4)
					Expect(len(parts)).To(BeNumerically(">=", 4))
					continue
				}
				if strings.HasPrefix(line, "# TYPE ") {
					parts := strings.SplitN(line, " ", 4)
					Expect(len(parts)).To(Equal(4))
					validTypes := []string{"counter", "gauge", "histogram", "summary", "untyped"}
					Expect(validTypes).To(ContainElement(parts[3]))
					continue
				}
				if strings.HasPrefix(line, "#") {
					continue
				}
				if strings.Contains(line, " ") && strings.Contains(line, "hostname=") {
					parts := strings.SplitN(line, " ", 2)
					metricName := parts[0]
					if strings.HasPrefix(metricName, "squid_site_") {
						Expect(metricName).To(MatchRegexp(`^squid_site_[a-zA-Z_:][a-zA-Z0-9_:]*\{.*hostname=.*\}$`))
						valueStr := strings.Fields(parts[1])[0]
						_, err := strconv.ParseFloat(valueStr, 64)
						Expect(err).NotTo(HaveOccurred())
					}
				}
			}
		})

		It("should include required metadata for per-site metrics", func() {
			metricsURL := fmt.Sprintf("https://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			c := newHTTPSClient(10 * time.Second)
			resp, err := c.Get(metricsURL)
			Expect(err).NotTo(HaveOccurred(), "Should get per-site metrics response")
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred(), "Should read per-site metrics body")
			metricsContent := string(body)

			required := []string{
				"squid_site_requests_total",
				"squid_site_misses_total",
				"squid_site_bytes_total",
				"squid_site_hit_ratio",
			}
			for _, metric := range required {
				Expect(metricsContent).To(ContainSubstring("# HELP " + metric + " "))
				Expect(metricsContent).To(ContainSubstring("# TYPE " + metric + " "))
			}
		})
	})
})
