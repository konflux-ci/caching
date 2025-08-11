package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
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
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(2))

			// Find squid container
			var squidContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "squid" {
					squidContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(squidContainer).NotTo(BeNil(), "squid container should exist")
			Expect(squidContainer.Image).To(ContainSubstring("squid"))

			// Check squid port configuration
			Expect(squidContainer.Ports).To(HaveLen(1))
			Expect(squidContainer.Ports[0].ContainerPort).To(Equal(int32(3128)))
			Expect(squidContainer.Ports[0].Name).To(Equal("http"))

			// Find squid-exporter container
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
				Expect(pod.Spec.Containers).To(HaveLen(2))

				// Find squid container
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

				// Find squid-exporter container
				var exporterContainer *corev1.Container
				for i := range pod.Spec.Containers {
					if pod.Spec.Containers[i].Name == "squid-exporter" {
						exporterContainer = &pod.Spec.Containers[i]
						break
					}
				}
				Expect(exporterContainer).NotTo(BeNil(), "squid-exporter container should exist")
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

			// SSL-Bump configuration checks
			Expect(squidConf).To(ContainSubstring("ssl-bump"), "Squid should be configured for SSL-Bump on HTTP port")
			Expect(squidConf).To(ContainSubstring("generate-host-certificates=on"), "SSL-Bump should be configured to generate host certificates dynamically")
			Expect(squidConf).To(ContainSubstring("ssl_bump peek step1"), "SSL-Bump should peek at SSL connections in step1")
			Expect(squidConf).To(ContainSubstring("ssl_bump bump all"), "SSL-Bump should bump all SSL connections")
			Expect(squidConf).To(ContainSubstring("sslcrtd_program"), "SSL-Bump should have certificate generation daemon configured")
			Expect(squidConf).To(ContainSubstring("sslcrtd_children 8"), "SSL-Bump should have 8 certificate daemon children configured")

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

	Describe("Resources verification", func() {
		It("should have the self-signed cluster issuer created", func() {
			clusterIssuer, err := certManagerClient.CertmanagerV1().ClusterIssuers().Get(ctx, "proxy-self-signed-cluster-issuer", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get self-signed cluster issuer")
			Expect(clusterIssuer).NotTo(BeNil(), "ClusterIssuer should not be nil")
			Expect(clusterIssuer.Name).To(Equal("proxy-self-signed-cluster-issuer"))
			Expect(clusterIssuer.Spec.SelfSigned).NotTo(BeNil(), "SelfSigned spec should not be nil")
		})

		It("should have the CA certificate created in cert-manager namespace", func() {
			// Get the CA certificate from the cert-manager namespace
			caCert, err := certManagerClient.CertmanagerV1().Certificates("cert-manager").Get(ctx, "proxy-self-signed-ca", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get CA certificate")
			Expect(caCert).NotTo(BeNil(), "CA Certificate should not be nil")
			Expect(caCert.Name).To(Equal("proxy-self-signed-ca"))

			// Verify the certificate spec
			Expect(caCert.Spec.SecretName).To(Equal("proxy-root-ca-secret"))
			Expect(caCert.Spec.IssuerRef.Name).To(Equal("proxy-self-signed-cluster-issuer"))
			Expect(caCert.Spec.IssuerRef.Kind).To(Equal("ClusterIssuer"))
			Expect(caCert.Spec.IsCA).To(BeTrue(), "CA certificate should have isCA set to true")

			// Verify the certificate status
			Expect(caCert.Status.Conditions).NotTo(BeEmpty(), "CA certificate should have status conditions")
			var readyCondition *certmanagerv1.CertificateCondition
			for _, condition := range caCert.Status.Conditions {
				if condition.Type == "Ready" {
					readyCondition = &condition
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil(), "CA certificate should have Ready condition")
			Expect(string(readyCondition.Status)).To(Equal("True"), "CA certificate should be ready")
		})

		It("should have the CA secret created in cert-manager namespace", func() {
			// Get the CA secret from the cert-manager namespace
			caSecret, err := clientset.CoreV1().Secrets("cert-manager").Get(ctx, "proxy-root-ca-secret", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get CA secret")
			Expect(caSecret).NotTo(BeNil(), "CA Secret should not be nil")
			Expect(caSecret.Name).To(Equal("proxy-root-ca-secret"))
			Expect(caSecret.Namespace).To(Equal("cert-manager"))
			Expect(caSecret.Type).To(Equal(corev1.SecretTypeTLS), "CA secret should be of type TLS")

			// Verify the secret contains the required data
			Expect(caSecret.Data).To(HaveKey("tls.crt"), "CA secret should contain tls.crt")
			Expect(caSecret.Data).To(HaveKey("tls.key"), "CA secret should contain tls.key")
			Expect(caSecret.Data["tls.crt"]).NotTo(BeEmpty(), "CA certificate should not be empty")
			Expect(caSecret.Data["tls.key"]).NotTo(BeEmpty(), "CA private key should not be empty")
		})

		It("should have the CA cluster issuer created", func() {
			// Get the CA cluster issuer
			caIssuer, err := certManagerClient.CertmanagerV1().ClusterIssuers().Get(ctx, "proxy-ca-issuer", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get CA cluster issuer")
			Expect(caIssuer).NotTo(BeNil(), "CA ClusterIssuer should not be nil")
			Expect(caIssuer.Name).To(Equal("proxy-ca-issuer"))

			// Verify the issuer spec
			Expect(caIssuer.Spec.CA).NotTo(BeNil(), "CA spec should not be nil")
			Expect(caIssuer.Spec.CA.SecretName).To(Equal("proxy-root-ca-secret"), "CA issuer should reference the proxy-root-ca-secret")
		})

		It("should have the proxy certificate created in proxy namespace", func() {
			// Get the proxy certificate from the proxy namespace
			proxyCert, err := certManagerClient.CertmanagerV1().Certificates(namespace).Get(ctx, namespace+"-cert", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get proxy certificate")
			Expect(proxyCert).NotTo(BeNil(), "Proxy Certificate should not be nil")
			Expect(proxyCert.Name).To(Equal(namespace + "-cert"))

			// Verify the certificate spec
			Expect(proxyCert.Spec.SecretName).To(Equal(namespace + "-tls"))
			Expect(proxyCert.Spec.IssuerRef.Name).To(Equal("proxy-ca-issuer"))
			Expect(proxyCert.Spec.IssuerRef.Kind).To(Equal("ClusterIssuer"))
			Expect(proxyCert.Spec.IsCA).To(BeTrue(), "Proxy certificate should have isCA set to true")

			// Verify DNS names
			Expect(proxyCert.Spec.DNSNames).To(ContainElement("localhost"))
			Expect(proxyCert.Spec.DNSNames).To(ContainElement(namespace + "." + namespace + ".svc"))

			// Verify the certificate status
			Expect(proxyCert.Status.Conditions).NotTo(BeEmpty(), "Proxy certificate should have status conditions")
			var readyCondition *certmanagerv1.CertificateCondition
			for _, condition := range proxyCert.Status.Conditions {
				if condition.Type == "Ready" {
					readyCondition = &condition
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil(), "Proxy certificate should have Ready condition")
			Expect(string(readyCondition.Status)).To(Equal("True"), "Proxy certificate should be ready")
		})

		It("should have the TLS secret created with certificate data", func() {
			// Get the TLS secret from the proxy namespace
			tlsSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, namespace+"-tls", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get TLS secret")
			Expect(tlsSecret).NotTo(BeNil(), "TLS Secret should not be nil")
			Expect(tlsSecret.Name).To(Equal(namespace + "-tls"))
			Expect(tlsSecret.Type).To(Equal(corev1.SecretTypeTLS), "Secret should be of type TLS")

			// Verify the secret contains the required data
			Expect(tlsSecret.Data).To(HaveKey("tls.crt"), "TLS secret should contain tls.crt")
			Expect(tlsSecret.Data).To(HaveKey("tls.key"), "TLS secret should contain tls.key")
			Expect(tlsSecret.Data["tls.crt"]).NotTo(BeEmpty(), "TLS certificate should not be empty")
			Expect(tlsSecret.Data["tls.key"]).NotTo(BeEmpty(), "TLS private key should not be empty")
		})
	})
})
