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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NOTE: This file assumes helper functions like 'getPodIP' and 'generateCacheBuster'
// are available from other test files in the package.

var _ = Describe("Squid SSL-Bump Functionality", Ordered, func() {
	var (
		k8sClient     *kubernetes.Clientset
		config        *rest.Config
		trustedClient *http.Client
		squidPod      corev1.Pod
	)

	const testServerURL = "https://test-server.proxy.svc.cluster.local:443"

	BeforeEach(func() {
		var err error

		config, err = rest.InClusterConfig()
		Expect(err).NotTo(HaveOccurred(), "Failed to get in-cluster config")

		k8sClient, err = kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "Failed to create k8s client")

		// Get the Squid CA certificate from the ConfigMap created by trust-manager
		By("Getting Squid CA certificate from trust-manager ConfigMap")
		fmt.Printf("DEBUG: Retrieving proxy CA bundle from ConfigMap\n")
		proxyCAConfigMap, err := k8sClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), "proxy-ca-bundle", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get proxy-ca-bundle ConfigMap")
		Expect(proxyCAConfigMap.Data).To(HaveKey("ca-bundle.crt"), "CA ConfigMap should contain 'ca-bundle.crt'")
		fmt.Printf("DEBUG: Proxy CA bundle retrieved successfully\n")

		// Get the test-server CA certificate from the ConfigMap created by trust-manager
		By("Getting test-server CA certificate from trust-manager ConfigMap")
		fmt.Printf("DEBUG: Retrieving test-server CA bundle from ConfigMap\n")
		testServerCAConfigMap, err := k8sClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), "test-server-bundle", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get test-server-bundle ConfigMap")
		Expect(testServerCAConfigMap.Data).To(HaveKey("ca.crt"), "Test-server CA ConfigMap should contain 'ca.crt'")
		fmt.Printf("DEBUG: Test-server CA bundle retrieved successfully\n")

		// Create trusted client with both CA bundles (same as test-client combined approach)
		By("Creating trusted client with both CA bundles")
		fmt.Printf("DEBUG: Creating trusted client with proxy CA and test-server CA")
		trustedClient, err = testhelpers.NewTrustedSquidProxyClient(
			serviceName,
			namespace,
			[]byte(proxyCAConfigMap.Data["ca-bundle.crt"]),
			[]byte(testServerCAConfigMap.Data["ca.crt"]),
		)
		Expect(err).NotTo(HaveOccurred(), "Failed to create trusted proxy client with both CA bundles")
		fmt.Printf("DEBUG: Trusted client created successfully\n")
		By("Getting Squid pod name")
		pods, err := k8sClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=squid",
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to list Squid pods")
		Expect(pods.Items).NotTo(BeEmpty(), "Should have at least one Squid pod")
		squidPod = pods.Items[0]
	})

	Describe("SSL-Bump Certificate Inspection", func() {
		It("should successfully make HTTPS request through Squid proxy with trusted client", func() {
			fmt.Printf("DEBUG: Testing HTTPS request to: %s\n", testServerURL)
			By("Making HTTPS request through Squid proxy with trusted client (with retries)")
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
			}, timeout, interval).Should(Succeed(), "HTTPS request should succeed with trusted client (retries handle transient HTTP errors)")

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
		It("should fail HTTPS request with untrusted client", func() {

			fmt.Printf("DEBUG: Testing untrusted client with URL: %s\n", testServerURL)

			// Create regular client for comparison
			client, err := testhelpers.NewSquidProxyClient(serviceName, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to create proxy client")

			fmt.Printf("DEBUG: Making request with untrusted client...\n")
			_, err = client.Get(testServerURL)
			fmt.Printf("DEBUG: Untrusted client request result: %v\n", err)
			Expect(err).To(HaveOccurred(), "Untrusted client should fail HTTPS request")
		})
	})

	Describe("SSL-Bump Log Verification", func() {
		It("should show decrypted HTTPS requests in Squid access logs", func() {
			// By("Getting Squid pod name")
			// pods, err := k8sClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			// 	LabelSelector: "app.kubernetes.io/name=squid",
			// })
			// Expect(err).NotTo(HaveOccurred(), "Failed to list Squid pods")
			// Expect(pods.Items).NotTo(BeEmpty(), "Should have at least one Squid pod")
			// squidPod := pods.Items[0]
			// Use the local test server with a unique ID for SSL-Bump verification
			timestamp := time.Now().Unix()
			testURL := fmt.Sprintf("%s/ssl-bump-test/%d", testServerURL, timestamp)

			fmt.Printf("DEBUG: Using test URL for SSL-Bump verification: %s\n", testURL)
			fmt.Printf("DEBUG: Using Squid pod: %s\n", squidPod.Name)

			By("Getting logs before making HTTPS request")
			beforeRequest := metav1.Now()

			By("Making HTTPS request to generate SSL-Bump log entries (with retries)")
			Eventually(func() error {
				resp, err := trustedClient.Get(testURL)
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

			// Wait a moment to ensure the request is logged
			time.Sleep(1 * time.Second)

			By("Getting logs since before the request")
			requestLogs, err := k8sClient.CoreV1().Pods(namespace).GetLogs(squidPod.Name, &corev1.PodLogOptions{
				Container: "squid",
				SinceTime: &beforeRequest,
			}).Do(context.Background()).Raw()
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs")

			By("Verifying logs show SSL-Bump evidence")
			logOutput := string(requestLogs)

			// Debug: Print the actual logs we received
			fmt.Printf("DEBUG: Squid logs for SSL-Bump verification:\n")
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", logOutput)
			fmt.Printf("==========================================\n")

			// Should show CONNECT tunnel establishment for HTTPS
			Expect(logOutput).To(ContainSubstring("CONNECT"), "Should show CONNECT tunnel establishment")

			// Should show decrypted HTTPS GET requests
			Expect(logOutput).To(ContainSubstring("GET https://"), "Should show decrypted HTTPS GET requests")

			// Verify the specific test server URL was requested
			Expect(logOutput).To(ContainSubstring("test-server.proxy.svc.cluster.local"), "Should show the test server URL in logs")
			Expect(logOutput).To(ContainSubstring("ssl-bump-test"), "Should show the SSL-Bump test endpoint in logs")

			fmt.Printf("DEBUG: SSL-Bump verification successful - CONNECT tunnel and decrypted GET request detected!\n")
		})
	})

	Describe("SSL-Bump HTTPS RAM Caching", func() {
		It("should cache HTTPS content in RAM proving SSL-Bump decryption and caching work", func() {
			// By("Getting Squid pod name")
			// pods, err := k8sClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			// 	LabelSelector: "app.kubernetes.io/name=squid",
			// })
			// Expect(err).NotTo(HaveOccurred(), "Failed to list Squid pods")
			// Expect(pods.Items).NotTo(BeEmpty(), "Should have at least one Squid pod")
			// squidPod := pods.Items[0]
			// Use the local test server's cacheable SSL-Bump endpoint
			timestamp := time.Now().Unix()
			testURL := fmt.Sprintf("%s/ssl-bump-cache-test/%d", testServerURL, timestamp)

			fmt.Printf("DEBUG: Using unique test URL for HTTPS caching: %s\n", testURL)
			fmt.Printf("DEBUG: Using Squid pod: %s\n", squidPod.Name)

			// Get logs before all requests to capture the complete sequence
			beforeSequence := metav1.Now()

			By("Making first HTTPS request until successful (will be TCP_MISS)")
			Eventually(func() error {
				resp1, err := trustedClient.Get(testURL)
				if err != nil {
					return fmt.Errorf("network error: %w", err)
				}
				defer resp1.Body.Close()

				if resp1.StatusCode != 200 {
					return fmt.Errorf("expected status 200, got %d (retrying transient errors like 503)", resp1.StatusCode)
				}

				return nil
			}, timeout, interval).Should(Succeed(), "First HTTPS request should eventually succeed")

			By("Making second HTTPS request until successful (should be TCP_MEM_HIT)")
			Eventually(func() error {
				resp2, err := trustedClient.Get(testURL)
				if err != nil {
					return fmt.Errorf("network error: %w", err)
				}
				defer resp2.Body.Close()

				if resp2.StatusCode != 200 {
					return fmt.Errorf("expected status 200, got %d (retrying transient errors like 503)", resp2.StatusCode)
				}

				return nil
			}, timeout, interval).Should(Succeed(), "Second HTTPS request should eventually succeed")

			// Wait a moment to ensure all requests are logged
			time.Sleep(1 * time.Second)

			// Verify the complete caching sequence in logs
			By("Getting logs since before the sequence")
			allLogs, err := k8sClient.CoreV1().Pods(namespace).GetLogs(squidPod.Name, &corev1.PodLogOptions{
				Container: "squid",
				SinceTime: &beforeSequence,
			}).Do(context.Background()).Raw()
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs")

			By("Verifying caching behavior: at least one TCP_MISS and at least one TCP_MEM_HIT (RAM cache hit)")
			logOutput := string(allLogs)
			fmt.Printf("DEBUG: Complete caching sequence logs:\n")
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", logOutput)
			fmt.Printf("==========================================\n")

			Expect(logOutput).To(ContainSubstring("TCP_MISS"), "Should show at least one TCP_MISS for the test URL")
			Expect(logOutput).To(ContainSubstring("test-server.proxy.svc.cluster.local"), "Should show the test server URL in logs")
			Expect(logOutput).To(ContainSubstring("ssl-bump-cache-test"), "Should show the SSL-Bump cache test endpoint in logs")
			Expect(logOutput).To(ContainSubstring("TCP_MEM_HIT"), "Should show at least one TCP_MEM_HIT (RAM cache hit) for the test URL")

			fmt.Printf("DEBUG: Caching verification successful - found both TCP_MISS and TCP_MEM_HIT!\n")
		})
	})
})
