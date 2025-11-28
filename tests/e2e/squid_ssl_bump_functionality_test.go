package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var _ = Describe("Squid SSL-Bump Functionality", Ordered, Serial, func() {
	var (
		k8sClient     *kubernetes.Clientset
		config        *rest.Config
		trustedClient *http.Client
		squidPods     []*corev1.Pod
		deployment    *appsv1.Deployment
	)

	const testServerURL = "https://test-server." + namespace + ".svc.cluster.local:443"

	BeforeAll(func() {
		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			TLSOutgoingOptions: &testhelpers.TLSOutgoingOptionsValues{
				CAFile: "/etc/squid/trust/test-server/ca.crt",
			},
			ReplicaCount: int(suiteReplicaCount),
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to configure squid for SSL bump tests")

		DeferCleanup(func() {
			err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
				ReplicaCount: int(suiteReplicaCount),
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to restore squid cache defaults")
		})
	})

	BeforeEach(func() {
		var err error
		config, err = rest.InClusterConfig()
		Expect(err).NotTo(HaveOccurred(), "Failed to get in-cluster config")

		k8sClient, err = kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "Failed to create k8s client")

		// Get the Squid CA certificate from the ConfigMap created by trust-manager
		By("Getting Squid CA certificate from trust-manager ConfigMap")
		fmt.Printf("DEBUG: Retrieving caching CA bundle from ConfigMap\n")
		cachingCAConfigMap, err := k8sClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), namespace+"-ca-bundle", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get "+namespace+"-ca-bundle ConfigMap")
		Expect(cachingCAConfigMap.Data).To(HaveKey("ca-bundle.crt"), "CA ConfigMap should contain 'ca-bundle.crt'")
		fmt.Printf("DEBUG: Caching CA bundle retrieved successfully\n")

		// Get the test-server CA certificate from the ConfigMap created by trust-manager
		By("Getting test-server CA certificate from trust-manager ConfigMap")
		fmt.Printf("DEBUG: Retrieving test-server CA bundle from ConfigMap\n")
		testServerCAConfigMap, err := k8sClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), "test-server-bundle", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get test-server-bundle ConfigMap")
		Expect(testServerCAConfigMap.Data).To(HaveKey("ca.crt"), "Test-server CA ConfigMap should contain 'ca.crt'")
		fmt.Printf("DEBUG: Test-server CA bundle retrieved successfully\n")

		// Create trusted client with both CA bundles (same as test-client combined approach)
		By("Creating trusted client with both CA bundles")
		fmt.Printf("DEBUG: Creating trusted client with caching CA and test-server CA\n")
		trustedClient, err = testhelpers.NewTrustedSquidCachingClient(
			serviceName,
			namespace,
			[]byte(cachingCAConfigMap.Data["ca-bundle.crt"]),
			[]byte(testServerCAConfigMap.Data["ca.crt"]),
		)
		Expect(err).NotTo(HaveOccurred(), "Failed to create trusted caching client with both CA bundles")
		fmt.Printf("DEBUG: Trusted client created successfully\n")
		deployment, err = clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get squid deployment")
		squidPods, err = testhelpers.GetSquidPods(ctx, clientset, namespace, *deployment.Spec.Replicas)
		Expect(err).NotTo(HaveOccurred(), "Failed to get Squid pods")
		podNames := make([]string, len(squidPods))
		for i, pod := range squidPods {
			podNames[i] = pod.Name
		}
		fmt.Printf("DEBUG: Available Squid pods: %v\n", podNames)

	})

	Describe("SSL-Bump Certificate Inspection", func() {
		It("should successfully make HTTPS request through Squid caching with trusted client", func() {
			fmt.Printf("DEBUG: Testing HTTPS request to: %s\n", testServerURL)
			By("Making HTTPS request through Squid caching with trusted client (with retries)")
			var resp *http.Response
			var body []byte

			Eventually(func() error {
				var err error
				fmt.Printf("DEBUG: Attempting HTTPS request...\n")
				resp, err = trustedClient.Get(testServerURL)
				if err != nil {
					fmt.Printf("DEBUG: Request failed: %v\n", err)
					return fmt.Errorf("network error: %w", err)
				}
				defer resp.Body.Close()

				fmt.Printf("DEBUG: Request successful, status: %s\n", resp.Status)
				body, err = io.ReadAll(resp.Body)
				if err != nil {
					fmt.Printf("DEBUG: Failed to read response body: %v\n", err)
					return fmt.Errorf("read body error: %w", err)
				}

				fmt.Printf("DEBUG: Response body length: %d bytes\n", len(body))
				if resp.StatusCode != 200 {
					fmt.Printf("DEBUG: Unexpected status code: %d\n", resp.StatusCode)
					return fmt.Errorf("expected status 200, got %d (retrying transient errors)", resp.StatusCode)
				}

				return nil
			}, timeout, interval).Should(Succeed(), "HTTPS request should succeed with trusted caching client (retries handle transient HTTP errors)")

			// Add debug output
			By(fmt.Sprintf("Response Status: %s", resp.Status))
			By(fmt.Sprintf("Response Headers: %v", resp.Header))
			By(fmt.Sprintf("Response Body: %s", string(body)))

			// Validate response
			Expect(resp.StatusCode).To(Equal(200), "HTTPS request should return 200 OK")
			Expect(body).NotTo(BeEmpty(), "Response body should not be empty")
			fmt.Printf("DEBUG: Test completed successfully!\n")
		})

		// Add a test that uses the regular client for comparison
		It("should fail HTTPS request with untrusted caching client", func() {

			fmt.Printf("DEBUG: Testing untrusted client with URL: %s\n", testServerURL)

			// Create regular client for comparison
			client, err := testhelpers.NewSquidCachingClient(serviceName, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to create caching client")

			fmt.Printf("DEBUG: Making request with untrusted client...\n")
			_, err = client.Get(testServerURL)
			fmt.Printf("DEBUG: Untrusted client request result: %v\n", err)
			Expect(err).To(HaveOccurred(), "Untrusted client should fail HTTPS request")
		})
	})

	Describe("SSL-Bump Log Verification", func() {
		It("should show decrypted HTTPS requests in Squid access logs", func() {
			// Use the local test server with a unique ID for SSL-Bump verification
			var resp *http.Response
			var err error
			timestamp := time.Now().Unix()
			testURL := fmt.Sprintf("%s/ssl-bump-test/%d", testServerURL, timestamp)

			fmt.Printf("DEBUG: Using test URL for SSL-Bump verification: %s\n", testURL)

			By("Getting logs before making HTTPS request")
			beforeRequest := metav1.Now()

			By("Making HTTPS request to generate SSL-Bump log entries (with retries)")
			Eventually(func() error {
				resp, err = trustedClient.Get(testURL)
				if err != nil {
					return fmt.Errorf("network error: %w", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != 200 {
					return fmt.Errorf(
						"expected status 200, got %d (retrying transient errors like 503) %s",
						resp.StatusCode,
						resp.Status,
					)
				}

				return nil
			}, timeout, interval).Should(Succeed(), "HTTPS request should succeed (retries handle transient HTTP errors)")
			fmt.Printf("DEBUG: Full response details:\n")
			fmt.Printf("  Status: %s\n", resp.Status)
			fmt.Printf("  Status Code: %d\n", resp.StatusCode)
			fmt.Printf("  Proto: %s\n", resp.Proto)
			fmt.Printf("  Headers:\n")
			for key, values := range resp.Header {
				for _, value := range values {
					fmt.Printf("    %s: %s\n", key, value)
				}
			}
			By("Extracting Squid pod name from Via header")
			actualPodName := testhelpers.ExtractSquidPodFromViaHeader(resp)
			Expect(actualPodName).NotTo(BeEmpty(), "Via header should contain pod name")
			fmt.Printf("DEBUG: Request handled by Squid pod: %s\n", actualPodName)
			// Wait a moment to ensure the request is logged
			time.Sleep(1 * time.Second)

			By("Getting logs since before the request")
			requestLogs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, actualPodName, squidContainerName, &beforeRequest)
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs")

			By("Verifying logs show SSL-Bump evidence")
			logOutput := string(requestLogs)

			// Debug: Print the actual logs we received
			fmt.Printf("DEBUG: Squid logs for SSL-Bump verification from pod %s:\n", actualPodName)
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", logOutput)
			fmt.Printf("==========================================\n")

			// Should show CONNECT tunnel establishment for HTTPS
			Expect(logOutput).To(ContainSubstring("CONNECT"), "Should show CONNECT tunnel establishment")

			// Should show decrypted HTTPS GET requests
			Expect(logOutput).To(ContainSubstring("GET https://"), "Should show decrypted HTTPS GET requests")

			// Verify the specific test server URL was requested
			Expect(logOutput).To(ContainSubstring("test-server."+namespace+".svc.cluster.local"), "Should show the test server URL in logs")
			Expect(logOutput).To(ContainSubstring("ssl-bump-test"), "Should show the SSL-Bump test endpoint in logs")

			fmt.Printf("DEBUG: SSL-Bump verification successful - CONNECT tunnel and decrypted GET request detected!\n")
		})
	})

	Describe("SSL-Bump HTTPS Caching", func() {
		It("should cache HTTPS content proving SSL-Bump decryption and caching work", func() {
			// Use the local test server's cacheable SSL-Bump endpoint
			timestamp := time.Now().Unix()
			testURL := fmt.Sprintf("%s/ssl-bump-cache-test/%d", testServerURL, timestamp)

			fmt.Printf("DEBUG: Using unique test URL for HTTPS caching: %s\n", testURL)
			// fmt.Printf("DEBUG: Using Squid pod: %s\n", squidPod.Name)

			// Get logs before all requests to capture the complete sequence
			beforeSequence := metav1.Now()

			cacheHitResult, err := testhelpers.FindCacheHitFromAnyPod(trustedClient, testURL, *deployment.Spec.Replicas)
			Expect(err).NotTo(HaveOccurred(), "Should find a cache hit from any pod")
			Expect(cacheHitResult.CacheHitFound).To(BeTrue(), "Should find a cache hit from any pod")
			fmt.Printf("DEBUG: Cache hit result: %v\n", cacheHitResult)

			// Wait a moment to ensure all requests are logged
			time.Sleep(1 * time.Second)

			// Verify the complete caching sequence in logs
			By("Getting logs since before the sequence")
			allLogs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, cacheHitResult.CacheHitPod, squidContainerName, &beforeSequence)
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs")

			By("Verifying caching behavior: at least one TCP_MISS and at least one TCP_HIT (RAM cache hit)")
			logOutput := string(allLogs)
			fmt.Printf("DEBUG: Complete caching sequence logs:\n")
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", logOutput)
			fmt.Printf("==========================================\n")

			Expect(logOutput).To(ContainSubstring("TCP_MISS"), "Should show at least one TCP_MISS for the test URL")
			Expect(logOutput).To(ContainSubstring("test-server."+namespace+".svc.cluster.local"), "Should show the test server URL in logs")
			Expect(logOutput).To(ContainSubstring("ssl-bump-cache-test"), "Should show the SSL-Bump cache test endpoint in logs")
			Expect(logOutput).To(ContainSubstring("TCP_HIT"), "Should show at least one TCP_HIT (RAM cache hit) for the test URL")

			fmt.Printf("DEBUG: Caching verification successful - found both TCP_MISS and TCP_HIT!\n")
		})
	})
})
