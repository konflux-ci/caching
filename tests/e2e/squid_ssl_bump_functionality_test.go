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

var _ = Describe("Squid SSL-Bump Functionality", func() {
	var (
		k8sClient     *kubernetes.Clientset
		config        *rest.Config
		client        *http.Client
		trustedClient *http.Client
	)

	BeforeEach(func() {
		var err error
		config, err = rest.InClusterConfig()
		Expect(err).NotTo(HaveOccurred(), "Failed to get in-cluster config")

		k8sClient, err = kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "Failed to create k8s client")

		// Get the Squid CA certificate from the ConfigMap created by trust-manager
		By("Getting Squid CA certificate from trust-manager ConfigMap")
		caConfigMap, err := k8sClient.CoreV1().ConfigMaps(namespace).Get(context.Background(), "proxy-ca-bundle", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get proxy-ca-bundle ConfigMap")
		Expect(caConfigMap.Data).To(HaveKey("ca-bundle.crt"), "CA ConfigMap should contain 'ca-bundle.crt'")

		// Create trusted client with Squid CA
		trustedClient, err = testhelpers.NewTrustedSquidProxyClient(serviceName, namespace, []byte(caConfigMap.Data["ca-bundle.crt"]))
		Expect(err).NotTo(HaveOccurred(), "Failed to create trusted proxy client")

		// Also create regular client for comparison
		client, err = testhelpers.NewSquidProxyClient(serviceName, namespace)
		Expect(err).NotTo(HaveOccurred(), "Failed to create proxy client")
	})

	Describe("SSL-Bump Certificate Inspection", func() {
		It("should present Squid CA certificate when accessing HTTPS sites", func() {
			By("Making HTTPS request through Squid proxy with trusted client (with retries)")
			var resp *http.Response
			var body []byte
			Eventually(func() error {
				var err error
				resp, err = trustedClient.Get("https://www.google.com")
				if err != nil {
					return fmt.Errorf("network error: %w", err)
				}
				defer resp.Body.Close()

				body, err = io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("read body error: %w", err)
				}

				if resp.StatusCode != 200 {
					return fmt.Errorf("expected status 200, got %d (retrying transient errors)", resp.StatusCode)
				}

				return nil
			}, timeout, interval).Should(Succeed(), "HTTPS request should succeed with trusted client (retries handle transient HTTP errors)")

			By("Verifying the certificate issuer is Squid CA")
			Expect(resp.TLS).NotTo(BeNil(), "TLS connection details must be available")
			Expect(resp.TLS.PeerCertificates).NotTo(BeEmpty(), "Should have at least one peer certificate")

			// Check the certificate issuer
			issuer := resp.TLS.PeerCertificates[0].Issuer
			Expect(issuer.Organization[0]).To(ContainSubstring("konflux"), "Certificate issuer organization should be konflux")

			// Verify response body content
			Expect(len(body)).To(BeNumerically(">", 0), "Response should have content")

			// Verify that untrusted client fails (proves SSL-Bump is working)
			By("Verifying untrusted client fails (proves SSL-Bump is working)")
			_, err := client.Get("https://www.google.com")
			Expect(err).To(HaveOccurred(), "Untrusted client should fail with certificate error")
			Expect(err.Error()).To(ContainSubstring("certificate signed by unknown authority"),
				"Error should indicate certificate verification failure")
		})
	})

	Describe("SSL-Bump Log Verification", func() {
		It("should show decrypted HTTPS requests in Squid access logs", func() {
			By("Getting Squid pod name")
			pods, err := k8sClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=squid",
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to list Squid pods")
			Expect(pods.Items).NotTo(BeEmpty(), "Should have at least one Squid pod")
			squidPod := pods.Items[0]

			// Use a unique URL to ensure fresh request
			timestamp := time.Now().Unix()
			testURL := fmt.Sprintf("https://httpbin.org/get?ssl-bump-test=%d", timestamp)

			fmt.Printf("DEBUG: Using test URL for SSL-Bump verification: %s\n", testURL)

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

			// Verify the specific URL was requested
			Expect(logOutput).To(ContainSubstring("httpbin.org/get"), "Should show the specific URL in logs")

			fmt.Printf("DEBUG: SSL-Bump verification successful - CONNECT tunnel and decrypted GET request detected!\n")
		})
	})

	Describe("SSL-Bump HTTPS RAM Caching", func() {
		It("should cache HTTPS content in RAM proving SSL-Bump decryption and caching work", func() {
			By("Getting Squid pod name")
			pods, err := k8sClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=squid",
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to list Squid pods")
			Expect(pods.Items).NotTo(BeEmpty(), "Should have at least one Squid pod")
			squidPod := pods.Items[0]

			// Use a well-known HTTPS server that's likely to send cacheable responses
			// httpbin.org typically sends reasonable cache headers
			// Add a unique timestamp to ensure fresh cache for each test run
			timestamp := time.Now().Unix()
			testURL := fmt.Sprintf("https://httpbin.org/cache/3600?test=%d", timestamp)

			fmt.Printf("DEBUG: Using unique test URL: %s\n", testURL)

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
			Expect(logOutput).To(ContainSubstring("httpbin.org/cache/3600"), "Should show the specific cache URL in logs")
			Expect(logOutput).To(ContainSubstring("TCP_MEM_HIT"), "Should show at least one TCP_MEM_HIT (RAM cache hit) for the test URL")

			fmt.Printf("DEBUG: Caching verification successful - found both TCP_MISS and TCP_MEM_HIT!\n")
		})
	})
})
