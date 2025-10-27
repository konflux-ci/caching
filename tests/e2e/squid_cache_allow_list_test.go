package e2e_test

import (
	"fmt"
	"net/http"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cache allow list tests", Ordered, func() {
	var (
		testServer *testhelpers.CachingTestServer
		client     *http.Client
	)

	BeforeEach(func() {
		testServer = setupHTTPTestServer("Cache allow list test server")
		client = setupHTTPTestClient()
	})

	Context("When cache.allowList is empty (default behavior)", func() {
		It("should cache all requests by default", func() {
			testURL := testServer.URL + "?" + generateCacheBuster("disabled-allow-list")

			By("Making the first request")
			resp1, body1, err := testhelpers.MakeCachingRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp1.Body.Close()

			response1, err := testhelpers.ParseTestServerResponse(body1)
			Expect(err).NotTo(HaveOccurred())
			testhelpers.ValidateServerHit(response1, 1, testServer)

			By("Making the second request for the same URL")
			time.Sleep(100 * time.Millisecond)
			resp2, body2, err := testhelpers.MakeCachingRequest(client, testURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close()

			response2, err := testhelpers.ParseTestServerResponse(body2)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the second request was served from cache")
			testhelpers.ValidateCacheHit(response1, response2, 1)
			Expect(testServer.GetRequestCount()).To(Equal(int32(1)),
				"Server should have received only 1 request (second served from cache)")
		})
	})

	Context("When cache.allowList contains specific patterns", func() {
		allowedPatterns := []string{
			"^http://.*/do-cache.*",
		}

		BeforeAll(func() {
			// Configure Squid with cache allow list for this test
			err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
				CacheAllowList: allowedPatterns,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to configure squid with cache allow list")

			DeferCleanup(func() {
				err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{})
				Expect(err).NotTo(HaveOccurred(), "Failed to restore squid cache defaults")
			})
		})

		It("should cache HTTP requests that match allowList patterns", func() {
			// Test URL that matches the first pattern
			matchingURL := testServer.URL + "/do-cache?" + generateCacheBuster("http-included-in-allow-list")

			By("Testing URL that matches allowList pattern")
			By(fmt.Sprintf("Testing URL: %s", matchingURL))

			resp1, body1, err := testhelpers.MakeCachingRequest(client, matchingURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp1.Body.Close()

			response1, err := testhelpers.ParseTestServerResponse(body1)
			Expect(err).NotTo(HaveOccurred())
			testhelpers.ValidateServerHit(response1, 1, testServer)

			By("Making second request to same URL")
			time.Sleep(100 * time.Millisecond)
			resp2, body2, err := testhelpers.MakeCachingRequest(client, matchingURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close()

			response2, err := testhelpers.ParseTestServerResponse(body2)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the second request was served from cache")
			testhelpers.ValidateCacheHit(response1, response2, 1)
			Expect(testServer.GetRequestCount()).To(Equal(int32(1)),
				"Matching URL should be cached")
		})

		It("should NOT cache requests that don't match allowList patterns", func() {
			// Reset test server counter
			testServer.ResetRequestCount()

			// Test URL that doesn't match any pattern
			nonMatchingURL := testServer.URL + "/dont-cache?" + generateCacheBuster("absent-from-allow-list")

			By("Testing URL that doesn't match allowList patterns")
			By(fmt.Sprintf("Testing URL: %s", nonMatchingURL))

			resp1, body1, err := testhelpers.MakeCachingRequest(client, nonMatchingURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp1.Body.Close()

			response1, err := testhelpers.ParseTestServerResponse(body1)
			Expect(err).NotTo(HaveOccurred())
			testhelpers.ValidateServerHit(response1, 1, testServer)

			By("Making second request to same URL")
			time.Sleep(100 * time.Millisecond)
			resp2, body2, err := testhelpers.MakeCachingRequest(client, nonMatchingURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close()

			response2, err := testhelpers.ParseTestServerResponse(body2)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the second request was NOT served from cache")
			// Both requests should have hit the server (no caching)
			testhelpers.ValidateServerHit(response2, 2, testServer)
			Expect(testServer.GetRequestCount()).To(Equal(int32(2)),
				"Non-matching URL should not be cached, both requests should hit server")

			// Request IDs should be different (not served from cache)
			Expect(response1.RequestID).NotTo(Equal(response2.RequestID),
				"Request IDs should be different (not cached)")
		})
	})
})
