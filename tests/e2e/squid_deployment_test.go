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
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))

			// Check consolidated squid container
			squidContainer := deployment.Spec.Template.Spec.Containers[0]
			Expect(squidContainer.Name).To(Equal("squid"))
			Expect(squidContainer.Image).To(ContainSubstring("squid"))

			// Check that squid container has both proxy and metrics ports
			Expect(squidContainer.Ports).To(HaveLen(2))

			// Find http port (squid proxy)
			var httpPort *corev1.ContainerPort
			var metricsPort *corev1.ContainerPort
			for i := range squidContainer.Ports {
				if squidContainer.Ports[i].Name == "http" {
					httpPort = &squidContainer.Ports[i]
				} else if squidContainer.Ports[i].Name == "metrics" {
					metricsPort = &squidContainer.Ports[i]
				}
			}

			Expect(httpPort).NotTo(BeNil(), "http port should exist")
			Expect(httpPort.ContainerPort).To(Equal(int32(3128)))

			Expect(metricsPort).NotTo(BeNil(), "metrics port should exist")
			Expect(metricsPort.ContainerPort).To(Equal(int32(9301)))
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
			Expect(service.Spec.Ports).To(HaveLen(2))

			// Find http port (squid)
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

			// Find metrics port (squid-exporter)
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
				Expect(pod.Spec.Containers).To(HaveLen(1))

				// Check consolidated squid container
				squidContainer := pod.Spec.Containers[0]
				Expect(squidContainer.Name).To(Equal("squid"))

				// Check squid security context (should run as non-root)
				if squidContainer.SecurityContext != nil {
					Expect(squidContainer.SecurityContext.RunAsNonRoot).NotTo(BeNil())
					if squidContainer.SecurityContext.RunAsNonRoot != nil {
						Expect(*squidContainer.SecurityContext.RunAsNonRoot).To(BeTrue())
					}
				}
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
