package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
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
		It("should have the caching namespace created", func() {
			ns, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get caching namespace")
			Expect(ns.Name).To(Equal(namespace))
			Expect(ns.Status.Phase).To(Equal(corev1.NamespaceActive))
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
			Expect(deployment.Name).To(Equal(deploymentName))
			Expect(deployment.Namespace).To(Equal(namespace))

			// Check deployment spec
			Expect(deployment.Spec.Replicas).NotTo(BeNil())
			Expect(*deployment.Spec.Replicas).To(BeNumerically(">=", 1))

			// Check selector and labels
			Expect(deployment.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", deploymentName))
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
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(3))

			// Find squid container
			var squidContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == deploymentName {
					squidContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(squidContainer).NotTo(BeNil(), "squid container should exist")
			Expect(squidContainer.Image).To(ContainSubstring(deploymentName))

			// Squid container should expose caching and per-site-http ports
			Expect(squidContainer.Ports).To(HaveLen(2))
			Expect(squidContainer.Ports[0].Name).To(Equal("http"))
			Expect(squidContainer.Ports[0].ContainerPort).To(Equal(int32(3128)))
			Expect(squidContainer.Ports[1].Name).To(Equal("per-site-http"))
			Expect(squidContainer.Ports[1].ContainerPort).To(Equal(int32(9302)))

			// Find squid-exporter container
			var exporterContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == deploymentName+"-exporter" {
					exporterContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(exporterContainer).NotTo(BeNil(), "squid-exporter container should exist")

			// Check squid-exporter port configuration
			Expect(exporterContainer.Ports).To(HaveLen(1))
			Expect(exporterContainer.Ports[0].ContainerPort).To(Equal(int32(9301)))
			Expect(exporterContainer.Ports[0].Name).To(Equal("metrics"))

			// Find icap-server container
			var icapContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "icap-server" {
					icapContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(icapContainer).NotTo(BeNil(), "icap-server container should exist")
			Expect(icapContainer.Image).To(ContainSubstring(deploymentName))
			// icap-server should expose the icap port
			Expect(icapContainer.Ports[0].ContainerPort).To(Equal(int32(1344)))
			Expect(icapContainer.Ports[0].Name).To(Equal("icap"))
		})

		It("should have correct anti-affinity configuration in deployed resources", func() {
			// Verify the actual deployed deployment has expected affinity rules

			// Check affinity configuration in the deployment
			affinity := deployment.Spec.Template.Spec.Affinity
			Expect(affinity).NotTo(BeNil(), "Deployed deployment should have affinity rules")
			Expect(affinity.PodAntiAffinity).NotTo(BeNil(), "Should have pod anti-affinity in deployed resources")

			// Verify the anti-affinity configuration matches our template defaults
			preferred := affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
			Expect(preferred).To(HaveLen(1), "Should have exactly one anti-affinity rule")

			rule := preferred[0]
			Expect(rule.Weight).To(Equal(int32(100)), "Should have maximum weight preference")
			Expect(rule.PodAffinityTerm.TopologyKey).To(Equal("kubernetes.io/hostname"), "Should spread by hostname")

			// Verify label selector targets correct pods
			labels := rule.PodAffinityTerm.LabelSelector.MatchLabels
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/name", deploymentName))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", deploymentName+"-"+namespace))
			// Note: instance label will be "squid" in actual deployment vs "test-release" in template tests
			Expect(labels).To(HaveKey("app.kubernetes.io/instance"))
		})

		It("should successfully schedule multiple replicas despite anti-affinity on single node", func() {
			// This tests that preferred anti-affinity doesn't prevent scheduling
			// when constraints can't be satisfied (single node scenario)

			// Verify all replicas are ready despite anti-affinity constraints
			Eventually(func() bool {
				dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return dep.Status.ReadyReplicas == *dep.Spec.Replicas
			}, timeout, interval).Should(BeTrue(), "All replicas should be ready despite anti-affinity constraints")

			// Verify pods are actually running (not stuck in pending due to anti-affinity)
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=" + deploymentName + ",app.kubernetes.io/component=" + deploymentName + "-" + namespace,
			})
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range pods.Items {
				Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), "Pod %s should be running", pod.Name)
			}

			// On single node, all pods will be on the same node, but that's expected
			// The important thing is that preferred anti-affinity didn't prevent scheduling
			if len(pods.Items) > 1 {
				GinkgoWriter.Printf("ℹ️  Multiple pods scheduled successfully despite anti-affinity (single-node cluster)\n")
				for _, pod := range pods.Items {
					GinkgoWriter.Printf("   Pod %s on node %s\n", pod.Name, pod.Spec.NodeName)
				}
			}
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
			Expect(service.Name).To(Equal(deploymentName))
			Expect(service.Namespace).To(Equal(namespace))

			// Check service type and selector
			Expect(service.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/name", deploymentName))
		})

		It("should have the correct port configuration", func() {
			Expect(service.Spec.Ports).To(HaveLen(3))

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

	Describe("Pods", func() {
		var pods []*corev1.Pod

		BeforeEach(func() {
			var err error
			deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid deployment")
			// GetSquidPods also checks that the all defined replicas are running and ready
			// It returns the list of pods
			pods, err = testhelpers.GetSquidPods(ctx, clientset, namespace, *deployment.Spec.Replicas)
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid pod")
		})

		It("should have correct resource configuration", func() {
			for _, pod := range pods {
				fmt.Printf("[DEBUG] Checking resource configuration for pod: %s\n", pod.Name)
				Expect(pod.Spec.Containers).To(HaveLen(3))

				containerNames := make([]string, 0, len(pod.Spec.Containers))
				for _, c := range pod.Spec.Containers {
					containerNames = append(containerNames, c.Name)
				}
				fmt.Printf("[DEBUG] Containers: %v\n", containerNames)

				// Find the squid container by name instead of assuming it's first
				squidContainer, err := testhelpers.FindContainerByName(pod, squidContainerName)
				Expect(err).NotTo(HaveOccurred(), "Failed to find squid container in pod")
				Expect(squidContainer).NotTo(BeNil(), "squid container should exist in pod")
				fmt.Printf("[DEBUG] Found squid container: %s\n", squidContainer.Name)

				// Check squid security context (should run as non-root)
				if squidContainer.SecurityContext != nil {
					fmt.Printf("[DEBUG] SecurityContext present. RunAsNonRoot: %v\n", squidContainer.SecurityContext.RunAsNonRoot)
					Expect(squidContainer.SecurityContext.RunAsNonRoot).NotTo(BeNil())
					if squidContainer.SecurityContext.RunAsNonRoot != nil {
						fmt.Printf("[DEBUG] RunAsNonRoot value: %v\n", *squidContainer.SecurityContext.RunAsNonRoot)
						Expect(*squidContainer.SecurityContext.RunAsNonRoot).To(BeTrue())
					}
				} else {
					fmt.Printf("[DEBUG] No SecurityContext set for container '%s'\n", squidContainer.Name)
				}
			}
		})

		It("should have the squid configuration mounted", func() {
			for _, pod := range pods {
				fmt.Printf("[DEBUG] Checking config mount for pod: %s\n", pod.Name)

				// Find the squid container by name
				squidContainer, err := testhelpers.FindContainerByName(pod, squidContainerName)
				Expect(err).NotTo(HaveOccurred(), "Failed to find squid container in pod")
				Expect(squidContainer).NotTo(BeNil(), "squid container should exist in pod")

				// Check for volume mounts
				var foundConfigMount bool
				for _, mount := range squidContainer.VolumeMounts {
					fmt.Printf("[DEBUG] Found mount: name=%s path=%s\n", mount.Name, mount.MountPath)
					if mount.Name == "squid-config" || mount.MountPath == "/etc/squid/squid.conf" {
						foundConfigMount = true
						break
					}
				}
				fmt.Printf("[DEBUG] Pod %s config mount present: %v\n", pod.Name, foundConfigMount)
				Expect(foundConfigMount).To(BeTrue(), "Pod should have squid configuration mounted")
			}
		})
	})

	Describe("ConfigMap", func() {
		It("should exist and contain squid configuration", func() {
			configMap, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, deploymentName+"-config", metav1.GetOptions{})
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
			testServer *testhelpers.CachingTestServer
			client     *http.Client
		)

		BeforeEach(func() {
			testServer = setupHTTPTestServer("HTTP caching test server")
			client = setupHTTPTestClient()
		})
		It("should cache HTTP responses and serve subsequent requests from cache", func() {
			testURL := testServer.URL + "?" + generateCacheBuster("cache-basic")

			// Get the number of replicas from the deployment
			deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should get squid deployment")
			replicaCount := *deployment.Spec.Replicas

			By("Making requests until we get a cache hit from any pod")
			// Track first server hit from each pod
			initialServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Initial server hits: %d\n", initialServerHits)

			cacheHitResult, err := testhelpers.FindCacheHitFromAnyPod(client, testURL, replicaCount)
			Expect(err).NotTo(HaveOccurred(), "Should find a cache hit from any pod")
			Expect(cacheHitResult.CacheHitFound).To(BeTrue(), "Should find a cache hit from any pod")

			testhelpers.ValidateCacheHitSamePod(cacheHitResult.OriginalResponse, cacheHitResult.CachedResponse, cacheHitResult.CacheHitPod, cacheHitResult.CacheHitPod)

			By("Verifying server received expected number of requests")
			// Server should have received one request per unique pod that was hit
			expectedServerHits := initialServerHits + int32(len(cacheHitResult.PodFirstHits))
			actualServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Server hits summary:\n")
			fmt.Printf("  Initial: %d\n", initialServerHits)
			fmt.Printf("  Expected: %d\n", expectedServerHits)
			fmt.Printf("  Actual: %d\n", actualServerHits)
			fmt.Printf("  Unique pods hit: %d\n", len(cacheHitResult.PodFirstHits))
			fmt.Printf("  Pods: %v\n", func() []string {
				pods := make([]string, 0, len(cacheHitResult.PodFirstHits))
				for pod := range cacheHitResult.PodFirstHits {
					pods = append(pods, pod)
				}
				return pods
			}())

			Expect(actualServerHits).To(Equal(expectedServerHits),
				"Server should have received one request per unique pod")
		})

		Describe("Caching Verification", func() {
			It("should verify configuration is set for disk caching", func() {
				configMap, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, deploymentName+"-config", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to get squid-config ConfigMap")

				squidConf := configMap.Data["squid.conf"]

				// Verify RAM caching configuration
				Expect(squidConf).To(ContainSubstring("cache_mem 0"), "Should set cache_mem to 0")
				// Verify disk cache directory is configured
				Expect(squidConf).To(ContainSubstring("cache_dir aufs /var/spool/squid/cache 204 16 256"), "Should have disk cache configured")
				// Verify maximum object size is configured
				Expect(squidConf).To(ContainSubstring("maximum_object_size 192 MB"), "Should have maximum object size configured")
				// Verify cache replacement policy
				Expect(squidConf).To(ContainSubstring("cache_replacement_policy heap LFUDA"), "Should use LFUDA replacement policy")
			})
		})

		Describe("Resources verification", func() {
			It("should have the self-signed cluster issuer created", func() {
				clusterIssuer, err := certManagerClient.CertmanagerV1().ClusterIssuers().Get(ctx, namespace+"-self-signed-cluster-issuer", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to get self-signed cluster issuer")
				Expect(clusterIssuer).NotTo(BeNil(), "ClusterIssuer should not be nil")
				Expect(clusterIssuer.Name).To(Equal(namespace + "-self-signed-cluster-issuer"))
				Expect(clusterIssuer.Spec.SelfSigned).NotTo(BeNil(), "SelfSigned spec should not be nil")
			})

			It("should have the CA certificate created in cert-manager namespace", func() {
				// Get the CA certificate from the cert-manager namespace
				caCert, err := certManagerClient.CertmanagerV1().Certificates("cert-manager").Get(ctx, namespace+"-self-signed-ca", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to get CA certificate")
				Expect(caCert).NotTo(BeNil(), "CA Certificate should not be nil")
				Expect(caCert.Name).To(Equal(namespace + "-self-signed-ca"))

				// Verify the certificate spec
				Expect(caCert.Spec.SecretName).To(Equal(namespace + "-root-ca-secret"))
				Expect(caCert.Spec.IssuerRef.Name).To(Equal(namespace + "-self-signed-cluster-issuer"))
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
				caSecret, err := clientset.CoreV1().Secrets("cert-manager").Get(ctx, namespace+"-root-ca-secret", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to get CA secret")
				Expect(caSecret).NotTo(BeNil(), "CA Secret should not be nil")
				Expect(caSecret.Name).To(Equal(namespace + "-root-ca-secret"))
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
				caIssuer, err := certManagerClient.CertmanagerV1().ClusterIssuers().Get(ctx, namespace+"-ca-issuer", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to get CA cluster issuer")
				Expect(caIssuer).NotTo(BeNil(), "CA ClusterIssuer should not be nil")
				Expect(caIssuer.Name).To(Equal(namespace + "-ca-issuer"))

				// Verify the issuer spec
				Expect(caIssuer.Spec.CA).NotTo(BeNil(), "CA spec should not be nil")
				Expect(caIssuer.Spec.CA.SecretName).To(Equal(namespace+"-root-ca-secret"), "CA issuer should reference the "+namespace+"-root-ca-secret")
			})

			It("should have the caching certificate created in caching namespace", func() {
				// Get the caching certificate from the caching namespace
				cachingCert, err := certManagerClient.CertmanagerV1().Certificates(namespace).Get(ctx, namespace+"-cert", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "Failed to get caching certificate")
				Expect(cachingCert).NotTo(BeNil(), "Caching Certificate should not be nil")
				Expect(cachingCert.Name).To(Equal(namespace + "-cert"))

				// Verify the certificate spec
				Expect(cachingCert.Spec.SecretName).To(Equal(namespace + "-tls"))
				Expect(cachingCert.Spec.IssuerRef.Name).To(Equal(namespace + "-ca-issuer"))
				Expect(cachingCert.Spec.IssuerRef.Kind).To(Equal("ClusterIssuer"))
				Expect(cachingCert.Spec.IsCA).To(BeTrue(), "Caching certificate should have isCA set to true")

				// Verify DNS names
				Expect(cachingCert.Spec.DNSNames).To(ContainElement("localhost"))
				Expect(cachingCert.Spec.DNSNames).To(ContainElement(deploymentName))
				Expect(cachingCert.Spec.DNSNames).To(ContainElement(deploymentName + "." + namespace + ".svc"))
				Expect(cachingCert.Spec.DNSNames).To(ContainElement(deploymentName + "." + namespace + ".svc.cluster.local"))

				// Verify the certificate status
				Expect(cachingCert.Status.Conditions).NotTo(BeEmpty(), "Caching certificate should have status conditions")
				var readyCondition *certmanagerv1.CertificateCondition
				for _, condition := range cachingCert.Status.Conditions {
					if condition.Type == "Ready" {
						readyCondition = &condition
						break
					}
				}
				Expect(readyCondition).NotTo(BeNil(), "Caching certificate should have Ready condition")
				Expect(string(readyCondition.Status)).To(Equal("True"), "Caching certificate should be ready")
			})

			It("should have the TLS secret created with certificate data", func() {
				// Get the TLS secret from the caching namespace
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
})
