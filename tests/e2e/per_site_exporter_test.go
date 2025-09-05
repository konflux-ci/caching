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
	"k8s.io/apimachinery/pkg/util/intstr"
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

		It("should expose per-site HTTP port on squid container, with HTTPS probes", func() {
			var squid *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "squid" {
					squid = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(squid).NotTo(BeNil(), "squid container should exist")

			// Ports should include http and per-site-http
			var hasPerSitePort bool
			for _, p := range squid.Ports {
				if p.Name == "per-site-http" && p.ContainerPort == 9302 {
					hasPerSitePort = true
				}
			}
			Expect(hasPerSitePort).To(BeTrue(), "squid container should expose per-site-http:9302")

			// Probes on squid should target per-site-http over HTTPS
			Expect(squid.ReadinessProbe).NotTo(BeNil())
			if squid.ReadinessProbe.HTTPGet != nil {
				Expect(squid.ReadinessProbe.HTTPGet.Path).To(Equal("/health"))
				Expect(squid.ReadinessProbe.HTTPGet.Port.StrVal).To(Equal("per-site-http"))
				Expect(string(squid.ReadinessProbe.HTTPGet.Scheme)).To(Equal("HTTPS"))
			}
			Expect(squid.LivenessProbe).NotTo(BeNil())
			if squid.LivenessProbe.HTTPGet != nil {
				Expect(squid.LivenessProbe.HTTPGet.Path).To(Equal("/health"))
				Expect(squid.LivenessProbe.HTTPGet.Port.StrVal).To(Equal("per-site-http"))
				Expect(string(squid.LivenessProbe.HTTPGet.Scheme)).To(Equal("HTTPS"))
			}
		})
	})

	Context("Service", func() {
		var service *corev1.Service

		BeforeEach(func() {
			var err error
			service, err = clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid service")
		})

		It("should expose per-site-http similar to squid-exporter", func() {
			var perSite *corev1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "per-site-http" {
					perSite = &service.Spec.Ports[i]
					break
				}
			}
			Expect(perSite).NotTo(BeNil(), "per-site-http port should exist")
			Expect(perSite.Port).To(Equal(int32(9302)))
			Expect(perSite.TargetPort).To(Equal(intstr.FromString("per-site-http")))
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

			time.Sleep(10 * time.Second)

			metricsURL := fmt.Sprintf("https://%s.%s.svc.cluster.local:9302/metrics", serviceName, namespace)
			c := newHTTPSClient(10 * time.Second)

			Eventually(func() error {
				resp, err := c.Get(metricsURL)
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
				expected := []string{
					"squid_site_requests_total",
					"squid_site_misses_total",
					"squid_site_bytes_total",
					"squid_site_hit_ratio",
					"squid_site_response_time_seconds",
				}
				for _, m := range expected {
					if !strings.Contains(metricsContent, m) {
						return fmt.Errorf("expected metric %s not found", m)
					}
				}
				return nil
			}, timeout, interval).Should(Succeed())
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
			}, timeout, interval).Should(Succeed())
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
			// Build a regex that matches a single Prometheus text-format sample line for the
			// specific metric and hostname label, and captures the numeric value.
			//
			// Example line matched:
			//   squid_site_requests_total{hostname="example.com",job="squid"} 42
			pattern := fmt.Sprintf(`^%s\{.*hostname="%s".*\}\s+([0-9.]+)`, regexp.QuoteMeta(metricName), regexp.QuoteMeta(hostname))
			re := regexp.MustCompile(pattern)
			for _, line := range lines {
				// Match this line against the regex. FindStringSubmatch returns a slice
				// where index 0 is the full match and index 1 is the captured numeric value.
				// len(matches) >= 2 verifies that the metric value exists
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

			// Capture baseline request count for this hostname before generating traffic.
			// If the metric doesn't exist yet, treat baseline as 0.
			metricsContentBefore, _ := getPerSiteMetrics()
			baselineRequests, _ := getPerSiteMetricsValue(metricsContentBefore, "squid_site_requests_total", testHostname)

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
				return (requestMetric - baselineRequests) >= 3
			}, timeout*2, interval).Should(BeTrue(), "Per-site request metrics delta should reflect generated proxy traffic (>= 3)")
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
})
