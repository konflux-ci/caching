package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// generateCacheBuster creates a unique string for cache-busting that's safe for parallel test execution
func generateCacheBuster(testName string) string {
	// Generate 8 random bytes for true uniqueness across containers
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to timestamp if crypto/rand fails
		randomBytes = []byte(fmt.Sprintf("%016x", time.Now().UnixNano()))
	}
	randomHex := hex.EncodeToString(randomBytes)

	// Get hostname (unique per container/pod)
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Combine multiple sources of uniqueness:
	// - Test name for context
	// - Current nanosecond timestamp
	// - Container hostname (unique per pod)
	// - Cryptographically random bytes
	// - Ginkgo's random seed
	return fmt.Sprintf("test=%s&t=%d&host=%s&rand=%s&seed=%d",
		testName,
		time.Now().UnixNano(),
		hostname,
		randomHex,
		GinkgoRandomSeed())
}

var _ = Describe("Squid Helm Chart Deployment", func() {

	Describe("Namespace", func() {
		It("should have the proxy namespace created", func() {
			namespace, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get proxy namespace")
			Expect(namespace.Name).To(Equal("proxy"))
			Expect(namespace.Status.Phase).To(Equal(corev1.NamespaceActive))
		})
	})

	Describe("Deployment", func() {
		var deployment *appsv1.Deployment

		BeforeEach(func() {
			var err error
			deployment, err = clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid deployment")
		})

		It("should exist and be properly configured", func() {
			Expect(deployment.Name).To(Equal("squid"))
			Expect(deployment.Namespace).To(Equal("proxy"))

			// Check deployment spec
			Expect(deployment.Spec.Replicas).NotTo(BeNil())
			Expect(*deployment.Spec.Replicas).To(BeNumerically(">=", 1))

			// Check selector and labels
			Expect(deployment.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", "squid"))
		})

		It("should be ready and available", func() {
			Eventually(func() bool {
				dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return dep.Status.ReadyReplicas == *dep.Spec.Replicas &&
					dep.Status.AvailableReplicas == *dep.Spec.Replicas
			}, timeout, interval).Should(BeTrue(), "Deployment should be ready and available")
		})

		It("should have the correct container image and configuration", func() {
			// With consolidated architecture, we expect exactly 2 containers:
			// 1. squid (with integrated per-site exporter), 2. squid-exporter
			containerCount := len(deployment.Spec.Template.Spec.Containers)
			Expect(containerCount).To(Equal(2)) // Exactly 2 containers: squid (with integrated per-site exporter) + squid-exporter

			// Find squid container (always present)
			var squidContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "squid" {
					squidContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(squidContainer).NotTo(BeNil(), "squid container should exist")
			Expect(squidContainer.Image).To(ContainSubstring("squid"))

			// Check squid port configuration (now includes per-site exporter port)
			Expect(squidContainer.Ports).To(HaveLen(2))

			// Check that both required ports are present
			var httpPort, perSiteHttpPort *corev1.ContainerPort
			for i := range squidContainer.Ports {
				if squidContainer.Ports[i].Name == "http" {
					httpPort = &squidContainer.Ports[i]
				} else if squidContainer.Ports[i].Name == "per-site-http" {
					perSiteHttpPort = &squidContainer.Ports[i]
				}
			}

			Expect(httpPort).NotTo(BeNil(), "squid container should have http port")
			Expect(httpPort.ContainerPort).To(Equal(int32(3128)))

			Expect(perSiteHttpPort).NotTo(BeNil(), "squid container should have per-site-http port")
			Expect(perSiteHttpPort.ContainerPort).To(Equal(int32(9302)))

			// Find squid-exporter container (should be enabled by default)
			var exporterContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "squid-exporter" {
					exporterContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(exporterContainer).NotTo(BeNil(), "squid-exporter container should exist")
			Expect(exporterContainer.Image).To(ContainSubstring("squid-exporter"))
			// Check squid-exporter port configuration
			Expect(exporterContainer.Ports).To(HaveLen(1))
			Expect(exporterContainer.Ports[0].ContainerPort).To(Equal(int32(9301)))
			Expect(exporterContainer.Ports[0].Name).To(Equal("metrics"))

			// Note: per-site-exporter is now integrated into the squid container
			// The per-site metrics port is already verified above as part of squid container ports
		})
	})

	Describe("Service", func() {
		var service *corev1.Service

		BeforeEach(func() {
			var err error
			service, err = clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid service")
		})

		It("should exist and be properly configured", func() {
			Expect(service.Name).To(Equal("squid"))
			Expect(service.Namespace).To(Equal("proxy"))

			// Check service type and selector
			Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/name", "squid"))
		})

		It("should have the correct port configuration", func() {
			// Per-site exporter is always enabled, so we expect exactly 3 ports:
			// 1. http (always), 2. metrics (if squidExporter enabled), 3. per-site-metrics (always)
			portCount := len(service.Spec.Ports)
			Expect(portCount).To(Equal(3)) // Exactly 3 ports: http + metrics + per-site-metrics

			// Find http port (squid) - always present
			var httpPort *corev1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "http" {
					httpPort = &service.Spec.Ports[i]
					break
				}
			}
			Expect(httpPort).NotTo(BeNil(), "http port should exist")
			Expect(httpPort.Port).To(Equal(int32(3128)))
			Expect(httpPort.TargetPort.StrVal).To(Equal("http"))
			Expect(httpPort.Protocol).To(Equal(corev1.ProtocolTCP))

			// Find metrics port (squid-exporter) - should be enabled by default
			var metricsPort *corev1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "metrics" {
					metricsPort = &service.Spec.Ports[i]
					break
				}
			}
			Expect(metricsPort).NotTo(BeNil(), "metrics port should exist")
			Expect(metricsPort.Port).To(Equal(int32(9301)))
			Expect(metricsPort.TargetPort.StrVal).To(Equal("metrics"))
			Expect(metricsPort.Protocol).To(Equal(corev1.ProtocolTCP))

			// Find per-site-metrics port (per-site-exporter) - always present
			var perSiteMetricsPort *corev1.ServicePort
			for i := range service.Spec.Ports {
				if service.Spec.Ports[i].Name == "per-site-http" {
					perSiteMetricsPort = &service.Spec.Ports[i]
					break
				}
			}
			Expect(perSiteMetricsPort).NotTo(BeNil(), "per-site-http port should always exist")
			Expect(perSiteMetricsPort.Port).To(Equal(int32(9302)))
			Expect(perSiteMetricsPort.TargetPort.StrVal).To(Equal("per-site-http"))
			Expect(perSiteMetricsPort.Protocol).To(Equal(corev1.ProtocolTCP))
		})

		It("should have endpoints ready", func() {
			Eventually(func() bool {
				endpoints, err := clientset.CoreV1().Endpoints(namespace).Get(ctx, serviceName, metav1.GetOptions{})
				if err != nil {
					return false
				}

				for _, subset := range endpoints.Subsets {
					if len(subset.Addresses) > 0 {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "Service should have ready endpoints")
		})
	})

	Describe("Pod", func() {
		var pods *corev1.PodList

		BeforeEach(func() {
			var err error
			// Select only squid deployment pods (exclude test and mirrord target pods)
			labelSelector := "app.kubernetes.io/name=squid,app.kubernetes.io/component notin (test,mirrord-target)"
			pods, err = clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to list squid pods")
			Expect(pods.Items).NotTo(BeEmpty(), "No squid pods found")
		})

		It("should be running and ready", func() {
			for _, pod := range pods.Items {
				Eventually(func() corev1.PodPhase {
					currentPod, err := clientset.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					return currentPod.Status.Phase
				}, timeout, interval).Should(Equal(corev1.PodRunning), fmt.Sprintf("Pod %s should be running", pod.Name))

				// Check readiness
				Eventually(func() bool {
					currentPod, err := clientset.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
					if err != nil {
						return false
					}

					for _, condition := range currentPod.Status.Conditions {
						if condition.Type == corev1.PodReady {
							return condition.Status == corev1.ConditionTrue
						}
					}
					return false
				}, timeout, interval).Should(BeTrue(), fmt.Sprintf("Pod %s should be ready", pod.Name))
			}
		})

		It("should have correct resource configuration", func() {
			for _, pod := range pods.Items {
				// With consolidated architecture, we expect exactly 2 containers
				containerCount := len(pod.Spec.Containers)
				Expect(containerCount).To(Equal(2)) // Exactly 2 containers: squid (with integrated per-site exporter) + squid-exporter

				// Find squid container (always present)
				var squidContainer *corev1.Container
				for i := range pod.Spec.Containers {
					if pod.Spec.Containers[i].Name == "squid" {
						squidContainer = &pod.Spec.Containers[i]
						break
					}
				}
				Expect(squidContainer).NotTo(BeNil(), "squid container should exist")

				// Check squid security context (should run as non-root)
				if squidContainer.SecurityContext != nil {
					Expect(squidContainer.SecurityContext.RunAsNonRoot).NotTo(BeNil())
					if squidContainer.SecurityContext.RunAsNonRoot != nil {
						Expect(*squidContainer.SecurityContext.RunAsNonRoot).To(BeTrue())
					}
				}

				// Find squid-exporter container (should be enabled by default)
				var exporterContainer *corev1.Container
				for i := range pod.Spec.Containers {
					if pod.Spec.Containers[i].Name == "squid-exporter" {
						exporterContainer = &pod.Spec.Containers[i]
						break
					}
				}
				Expect(exporterContainer).NotTo(BeNil(), "squid-exporter container should exist")

				// Verify squid container has per-site exporter port (integrated architecture)
				var perSitePort *corev1.ContainerPort
				for i := range squidContainer.Ports {
					if squidContainer.Ports[i].Name == "per-site-http" {
						perSitePort = &squidContainer.Ports[i]
						break
					}
				}
				Expect(perSitePort).NotTo(BeNil(), "squid container should have per-site-http port")
				Expect(perSitePort.ContainerPort).To(Equal(int32(9302)))
			}
		})

		It("should have the squid configuration mounted", func() {
			for _, pod := range pods.Items {
				container := pod.Spec.Containers[0]

				// Check for volume mounts
				var foundConfigMount bool
				for _, mount := range container.VolumeMounts {
					if mount.Name == "squid-config" || mount.MountPath == "/etc/squid/squid.conf" {
						foundConfigMount = true
						break
					}
				}
				Expect(foundConfigMount).To(BeTrue(), "Pod should have squid configuration mounted")
			}
		})
	})

	Describe("ConfigMap", func() {
		It("should exist and contain squid configuration", func() {
			configMap, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, "squid-config", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid-config ConfigMap")

			Expect(configMap.Data).To(HaveKey("squid.conf"))
			squidConf := configMap.Data["squid.conf"]

			// Basic configuration checks
			Expect(squidConf).To(ContainSubstring("http_port 3128"))
			Expect(squidConf).To(ContainSubstring("acl localnet src"))
		})
	})

	Describe("HTTP Caching Functionality", func() {
		var (
			testServer *testhelpers.ProxyTestServer
			client     *http.Client
		)

		BeforeEach(func() {
			// Get the pod's IP address for cross-pod communication
			podIP, err := getPodIP()
			Expect(err).NotTo(HaveOccurred(), "Failed to get pod IP")

			// Get test server port from environment, fallback to 0 (random port)
			testPort := 0
			if testPortStr := os.Getenv("TEST_SERVER_PORT"); testPortStr != "" {
				if port, parseErr := strconv.Atoi(testPortStr); parseErr == nil {
					testPort = port
				}
			}

			// Create test server using helpers
			testServer, err = testhelpers.NewProxyTestServer("Hello from test server", podIP, testPort)
			Expect(err).NotTo(HaveOccurred(), "Failed to create test server")

			// Create HTTP client configured for Squid proxy using helpers
			client, err = testhelpers.NewSquidProxyClient(serviceName, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to create proxy client")
		})

		AfterEach(func() {
			if testServer != nil {
				testServer.Close()
			}
		})

		It("should cache HTTP responses and serve subsequent requests from cache", func() {
			// Add cache-busting parameter to ensure this test gets fresh responses
			// and doesn't interfere with cache pollution from other tests
			// Use multiple entropy sources for parallel test safety
			testURL := testServer.URL + "?" + generateCacheBuster("cache-basic")

			By("Making the first HTTP request through Squid proxy")
			resp1, body1, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "First request should succeed")
			defer resp1.Body.Close()

			// Debug: print the actual response for troubleshooting
			fmt.Printf("DEBUG: Response status: %s\n", resp1.Status)
			fmt.Printf("DEBUG: Response body: %s\n", string(body1))
			fmt.Printf("DEBUG: Test server URL: %s\n", testURL)

			response1, err := testhelpers.ParseTestServerResponse(body1)
			Expect(err).NotTo(HaveOccurred(), "Should parse first response JSON")

			// Verify first request reached the server using helpers
			testhelpers.ValidateServerHit(response1, 1, testServer)

			By("Making the second HTTP request for the same URL")
			// Wait a moment to ensure any timing-related caching issues are avoided
			time.Sleep(100 * time.Millisecond)

			resp2, body2, err := testhelpers.MakeProxyRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred(), "Second request should succeed")
			defer resp2.Body.Close()

			response2, err := testhelpers.ParseTestServerResponse(body2)
			Expect(err).NotTo(HaveOccurred(), "Should parse second response JSON")

			By("Verifying the second request was served from cache")
			// Use helper to validate cache hit
			testhelpers.ValidateCacheHit(response1, response2, 1)

			// Server should still have received only 1 request
			Expect(testServer.GetRequestCount()).To(Equal(int32(1)), "Server should still have received only 1 request")

			// Response bodies should be identical (served from cache)
			Expect(string(body2)).To(Equal(string(body1)), "Cached response should be identical to original")

			By("Verifying cache headers are present")
			testhelpers.ValidateCacheHeaders(resp1)
			testhelpers.ValidateCacheHeaders(resp2)
		})

		It("should handle different URLs independently", func() {
			By("Making requests to different endpoints")

			// Add cache-busting to prevent interference from other tests
			// Use multiple entropy sources for parallel test safety
			baseBuster := generateCacheBuster("urls")

			// First URL
			url1 := testServer.URL + "/endpoint1?" + baseBuster + "&endpoint=1"
			resp1, _, err := testhelpers.MakeProxyRequest(client, url1)
			Expect(err).NotTo(HaveOccurred())
			defer resp1.Body.Close()

			initialCount := testServer.GetRequestCount()

			// Second URL (different from first)
			url2 := testServer.URL + "/endpoint2?" + baseBuster + "&endpoint=2"
			resp2, _, err := testhelpers.MakeProxyRequest(client, url2)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close()

			// Both requests should hit the server (different URLs)
			Expect(testServer.GetRequestCount()).To(Equal(initialCount+1), "Different URLs should not be cached together")
		})
	})
})
