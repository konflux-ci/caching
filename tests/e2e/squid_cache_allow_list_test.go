package e2e_test

import (
	"fmt"
	"net/http"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Cache allow list tests", Ordered, Serial, func() {
	var (
		testServer *testhelpers.CachingTestServer
		client     *http.Client
		deployment *appsv1.Deployment
		err        error
	)

	BeforeEach(func() {
		testServer = setupHTTPTestServer("Cache allow list test server")
		client = setupHTTPTestClient()

		deployment, err = clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to get squid deployment")
	})

	Context("When cache.allowList is empty (default behavior)", func() {
		It("should cache all requests by default", func() {
			testURL := testServer.URL + "?" + generateCacheBuster("disabled-allow-list")

			By("Making requests until we get a cache hit from any pod")

			initialServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Initial server hits: %d\n", initialServerHits)

			cacheHitResult, err := testhelpers.FindCacheHitFromAnyPod(client, testURL, *deployment.Spec.Replicas)
			Expect(err).NotTo(HaveOccurred(), "Should find a cache hit from any pod")
			Expect(cacheHitResult.CacheHitFound).To(BeTrue(), "Should find a cache hit from any pod")
			fmt.Printf("DEBUG: Cache hit result: %+v\n", cacheHitResult)

			By("Verifying we got a cache_hit from any pod")
			testhelpers.ValidateCacheHitSamePod(cacheHitResult.OriginalResponse, cacheHitResult.CachedResponse, cacheHitResult.CacheHitPod, cacheHitResult.CacheHitPod)

			By("Verifying server received expected number of requests")
			expectedServerHits := initialServerHits + int32(len(cacheHitResult.PodFirstHits))
			actualServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Server hits summary:\n")
			fmt.Printf("  Initial: %d\n", initialServerHits)
			fmt.Printf("  Expected: %d\n", expectedServerHits)
			fmt.Printf("  Actual: %d\n", actualServerHits)
			fmt.Printf("  Unique pods hit: %d\n", len(cacheHitResult.PodFirstHits))

			Expect(actualServerHits).To(Equal(expectedServerHits),
				"Server should have received one request per unique pod")
		})
	})

	Context("When cache.allowList contains specific patterns", func() {
		allowedPatterns := []string{
			"^http://.*/do-cache.*",
		}

		BeforeAll(func() {
			err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{

				Cache: &testhelpers.CacheValues{
					AllowList: allowedPatterns,
				},
				ReplicaCount: int(suiteReplicaCount),
			})

			Expect(err).NotTo(HaveOccurred(), "Failed to configure squid with cache allow list")

			DeferCleanup(func() {
				err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
					ReplicaCount: int(suiteReplicaCount),
				})
				Expect(err).NotTo(HaveOccurred(), "Failed to restore squid cache defaults")
			})
		})

		It("should cache HTTP requests that match allowList patterns", func() {
			// Test URL that matches the first pattern
			matchingURL := testServer.URL + "/do-cache?" + generateCacheBuster("http-included-in-allow-list")

			By("Testing URL that matches allowList pattern")
			By(fmt.Sprintf("Testing URL: %s", matchingURL))

			By("Making requests until we get a cache hit from any pod")
			// Track first server hit from each pod
			initialServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Initial server hits: %d\n", initialServerHits)

			cacheHitResult, err := testhelpers.FindCacheHitFromAnyPod(client, matchingURL, *deployment.Spec.Replicas)
			Expect(err).NotTo(HaveOccurred(), "Should find a cache hit from any pod")
			Expect(cacheHitResult.CacheHitFound).To(BeTrue(), "Should find a cache hit from any pod")
			fmt.Printf("DEBUG: Cache hit result: %+v\n", cacheHitResult)

			By("Verifying we got a cache_hit from any pod")
			testhelpers.ValidateCacheHitSamePod(cacheHitResult.OriginalResponse, cacheHitResult.CachedResponse, cacheHitResult.CacheHitPod, cacheHitResult.CacheHitPod)

			By("Verifying server received expected number of requests")
			expectedServerHits := initialServerHits + int32(len(cacheHitResult.PodFirstHits))
			actualServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Server hits summary:\n")
			fmt.Printf("  Initial: %d\n", initialServerHits)
			fmt.Printf("  Expected: %d\n", expectedServerHits)
			fmt.Printf("  Actual: %d\n", actualServerHits)
			fmt.Printf("  Unique pods hit: %d\n", len(cacheHitResult.PodFirstHits))

			Expect(actualServerHits).To(Equal(expectedServerHits),
				"Server should have received one request per unique pod")

		})

		It("should NOT cache requests that don't match allowList patterns", func() {
			// Reset test server counter
			testServer.ResetRequestCount()

			// Test URL that doesn't match any pattern
			nonMatchingURL := testServer.URL + "/dont-cache?" + generateCacheBuster("absent-from-allow-list")

			By("Testing URL that doesn't match allowList patterns")
			By(fmt.Sprintf("Testing URL: %s", nonMatchingURL))

			By("Making requests to get a cache hit from any pod")
			// Track first server hit from each pod
			initialServerHits := testServer.GetRequestCount()
			fmt.Printf("🔍 DEBUG: Initial server hits: %d\n", initialServerHits)

			cacheHitResult, err := testhelpers.FindCacheHitFromAnyPod(client, nonMatchingURL, *deployment.Spec.Replicas)
			Expect(err).To(HaveOccurred(), "Failed to get a cache hit from any pod")
			Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("no cache hit found from any pod within %d attempts", *deployment.Spec.Replicas+1)), "Should not find a cache hit from any pod")
			Expect(cacheHitResult).To(BeNil(), "Should not find a cache hit from any pod")
		})
	})
})
