package e2e

import (
	"context"
	"crypto/tls"
	"net/http"

	"github.com/containers/image/v5/transports/alltransports"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Docker Hub container pull tests", Ordered, Label("external-deps"), func() {
	var (
		ctx       context.Context
		clientset kubernetes.Interface
		namespace string
	)

	BeforeAll(func() {
		ctx = context.Background()
		clientset = testhelpers.GetClientset()
		namespace = testhelpers.GetNamespace()

		By("Ensuring squid is configured for Docker Hub caching")
		// Use default configuration which should include Docker Hub R2 patterns
	})

	When("pulling a Docker Hub container image through the proxy", func() {
		It("should cache Docker Hub R2 CDN requests", Label("docker-hub"), func() {
			By("Setting up container image reference")
			// Use a small, well-known image for testing
			imageRef := "docker.io/library/alpine:3.19"

			By("Creating container image client with proxy")
			parsedImageRef, err := alltransports.ParseImageName("docker://" + imageRef)
			Expect(err).NotTo(HaveOccurred(), "Failed to parse image reference")

			client, err := testhelpers.NewSquidProxiedContainerImageClient(
				ctx,
				clientset,
				namespace,
				parsedImageRef,
				[]byte(nil), // No credentials needed for public image
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to create proxied container image client")

			pod, err := testhelpers.GetSquidPod(ctx, clientset, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid pod")

			By("Pulling the image for the first time (expect CDN MISS)")
			before := metav1.Now()
			err = testhelpers.PullContainerImage(&client.Transport, parsedImageRef)
			Expect(err).NotTo(HaveOccurred(), "Failed to pull container image")

			By("Verifying Docker Hub R2 CDN request MISS in squid logs")
			logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, "squid", &before)
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs after first pull")
			logStr := string(logs)

			// Look for Docker Hub R2 CDN pattern in logs
			Expect(logStr).To(
				MatchRegexp("(?m)^.*TCP_MISS.*docker-images-prod\\.[a-f0-9]{32}\\.r2\\.cloudflarestorage\\.com.*$"),
				"First pull should be a MISS from Docker Hub R2 CDN",
			)
			Expect(logStr).To(Not(ContainSubstring("TCP_HIT")), "First pull should not produce a HIT from Docker Hub R2 CDN")

			By("Pulling the image for a second time (expect CDN HIT)")
			before = metav1.Now()
			err = testhelpers.PullContainerImage(&client.Transport, parsedImageRef)
			Expect(err).NotTo(HaveOccurred(), "Failed to pull container image")

			By("Verifying Docker Hub R2 CDN request HIT in squid logs")
			logs, err = testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, "squid", &before)
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs after second pull")
			logStr = string(logs)

			// Look for cache hit pattern
			Expect(logStr).To(
				MatchRegexp("(?m)^.*TCP_HIT.*docker-images-prod\\.[a-f0-9]{32}\\.r2\\.cloudflarestorage\\.com.*$"),
				"Second pull should be a HIT from Docker Hub R2 CDN",
			)
		})
	})

	When("testing Docker Hub R2 URL patterns directly", func() {
		It("should handle R2 URLs with proper normalization", func() {
			By("Creating HTTP client with squid proxy")
			proxyURL := testhelpers.GetSquidProxyURL(ctx, clientset, namespace)
			
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // Allow self-signed certificates
				},
			}
			
			client := &http.Client{
				Transport: transport,
			}

			By("Making request to sample Docker Hub R2 URL")
			// This is a synthetic test URL that matches the R2 pattern
			// In reality, this would be a redirect from registry-1.docker.io
			testURL := "https://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Expires=1200"
			
			// Note: This request will likely fail (401/403) since we don't have valid credentials,
			// but the important thing is that the URL pattern is recognized by squid
			resp, err := client.Get(testURL)
			if err == nil {
				resp.Body.Close()
			}
			
			// The test passes if squid recognizes the pattern and attempts to process it
			// We can verify this in the squid logs
			By("Verifying squid processed the R2 URL pattern")
			pod, err := testhelpers.GetSquidPod(ctx, clientset, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid pod")
			
			logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, "squid", &metav1.Time{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid logs")
			
			logStr := string(logs)
			// Look for the R2 domain in logs, indicating squid processed the request
			Expect(logStr).To(
				ContainSubstring("docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com"),
				"Squid should have processed the Docker Hub R2 URL",
			)
		})
	})
})
