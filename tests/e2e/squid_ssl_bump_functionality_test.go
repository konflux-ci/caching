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
			By("Making HTTPS request through Squid proxy with trusted client")
			resp, err := trustedClient.Get("https://www.google.com")
			Expect(err).NotTo(HaveOccurred(), "HTTPS request should succeed with trusted client")
			defer resp.Body.Close()

			By("Verifying the certificate issuer is Squid CA")
			Expect(resp.TLS).NotTo(BeNil(), "TLS connection details must be available")
			Expect(resp.TLS.PeerCertificates).NotTo(BeEmpty(), "Should have at least one peer certificate")

			// Check the certificate issuer
			issuer := resp.TLS.PeerCertificates[0].Issuer
			Expect(issuer.Organization[0]).To(ContainSubstring("konflux"), "Certificate issuer organization should be konflux")

			// Read the response body to ensure the connection was fully established
			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred(), "Should be able to read response body")
			Expect(len(body)).To(BeNumerically(">", 0), "Response should have content")

			// Verify that untrusted client fails (proves SSL-Bump is working)
			By("Verifying untrusted client fails (proves SSL-Bump is working)")
			_, err = client.Get("https://www.google.com")
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

			By("Making single HTTPS request to generate SSL-Bump log entries")
			resp, err := trustedClient.Get(testURL)
			Expect(err).NotTo(HaveOccurred(), "HTTPS request should succeed")
			Expect(resp.StatusCode).To(Equal(200), "Request should return 200 OK")
			resp.Body.Close()

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

	Describe("SSL-Bump HTTPS Caching", func() {
		It("should cache HTTPS content in RAM proving SSL-Bump decryption works", func() {
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

			By("Making first HTTPS request (should be TCP_MISS)")
			// Get logs before first request
			beforeFirstRequest := metav1.Now()

			resp1, err := trustedClient.Get(testURL)
			Expect(err).NotTo(HaveOccurred(), "First HTTPS request should succeed")
			Expect(resp1.StatusCode).To(Equal(200), "First request should return 200 OK")
			resp1.Body.Close()

			// Wait a moment to ensure the request is logged
			time.Sleep(1 * time.Second)

			// Get logs after first request and verify TCP_MISS
			By("Verifying first request was TCP_MISS")
			firstRequestLogs, err := k8sClient.CoreV1().Pods(namespace).GetLogs(squidPod.Name, &corev1.PodLogOptions{
				Container: "squid",
				SinceTime: &beforeFirstRequest,
			}).Do(context.Background()).Raw()
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs after first request")

			firstLogOutput := string(firstRequestLogs)
			fmt.Printf("DEBUG: Logs after first request:\n")
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", firstLogOutput)
			fmt.Printf("==========================================\n")

			// Verify first request shows TCP_MISS
			Expect(firstLogOutput).To(ContainSubstring("TCP_MISS"), "First request should show TCP_MISS")
			Expect(firstLogOutput).To(ContainSubstring("httpbin.org/cache/3600"), "Should show the specific URL in logs")

			By("Making second HTTPS request to the same URL (should be TCP_MEM_HIT)")
			// Get logs before second request
			beforeSecondRequest := metav1.Now()

			resp2, err := trustedClient.Get(testURL)
			Expect(err).NotTo(HaveOccurred(), "Second HTTPS request should succeed")
			Expect(resp2.StatusCode).To(Equal(200), "Second request should return 200 OK")
			resp2.Body.Close()

			// Wait a moment to ensure the request is logged
			time.Sleep(1 * time.Second)

			// Get logs after second request and verify TCP_MEM_HIT
			By("Verifying second request was TCP_MEM_HIT")
			secondRequestLogs, err := k8sClient.CoreV1().Pods(namespace).GetLogs(squidPod.Name, &corev1.PodLogOptions{
				Container: "squid",
				SinceTime: &beforeSecondRequest,
			}).Do(context.Background()).Raw()
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs after second request")

			secondLogOutput := string(secondRequestLogs)
			fmt.Printf("DEBUG: Logs after second request:\n")
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", secondLogOutput)
			fmt.Printf("==========================================\n")

			// Verify second request shows TCP_MEM_HIT
			Expect(secondLogOutput).To(ContainSubstring("TCP_MEM_HIT"), "Second request should show TCP_MEM_HIT")
			Expect(secondLogOutput).To(ContainSubstring("httpbin.org/cache/3600"), "Should show the specific URL in logs")

			fmt.Printf("DEBUG: Final summary - Both requests successfully decrypted and cached!\n")
		})
	})
})
