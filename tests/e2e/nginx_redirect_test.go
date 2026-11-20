package e2e_test

import (
	"io"
	"net/http"
	"net/url"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Nginx Redirect Interception Tests", Label("nginx"), Ordered, Serial, func() {
	var (
		backendURL string
		client     *http.Client
	)

	BeforeAll(func() {
		backendURL = testhelpers.GetNginxTestBackendURL()

		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled: true,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: backendURL,
				},
				Cache: &testhelpers.NginxCacheValues{
					AllowList: []string{"^/redirect"},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		client = testhelpers.NewNginxClient()
	})

	It("should follow redirects internally and return 200 to the client", func() {
		targetURL := backendURL + "/content/test"
		reqURL := testhelpers.GetNginxURL() + "/redirect?url=" + url.QueryEscape(targetURL)

		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK),
			"Nginx should follow the redirect and return 200, not 302")

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(body)).To(BeNumerically(">", 0),
			"Response body should contain the redirect target's content")
	})

	It("should cache redirected content with MISS then HIT", func() {
		targetURL := backendURL + "/content/cache-test"
		reqURL := testhelpers.GetNginxURL() + "/redirect?url=" + url.QueryEscape(targetURL) + "&" + generateCacheBuster("redirect-cache")

		// First request - should be MISS
		resp1, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		body1, err := io.ReadAll(resp1.Body)
		Expect(err).NotTo(HaveOccurred())
		resp1.Body.Close()
		Expect(resp1.StatusCode).To(Equal(http.StatusOK))
		Expect(resp1.Header.Get("X-Cache-Status")).To(Equal("MISS"),
			"First request should be a cache MISS")

		// Second request - should be HIT
		resp2, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		body2, err := io.ReadAll(resp2.Body)
		Expect(err).NotTo(HaveOccurred())
		resp2.Body.Close()
		Expect(resp2.StatusCode).To(Equal(http.StatusOK))
		Expect(resp2.Header.Get("X-Cache-Status")).To(Equal("HIT"),
			"Second request should be a cache HIT")

		Expect(body2).To(Equal(body1),
			"Cached response should match original response")
	})

	It("should pass through 403 from upstream even when content is cached", func() {
		targetURL := backendURL + "/content/ban-test"
		reqURL := testhelpers.GetNginxURL() + "/redirect?url=" + url.QueryEscape(targetURL) + "&" + generateCacheBuster("ban-test")

		// First request - should succeed and cache
		resp1, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		resp1.Body.Close()
		Expect(resp1.StatusCode).To(Equal(http.StatusOK),
			"First request should succeed")

		// Second request - should be cached
		resp2, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		resp2.Body.Close()
		Expect(resp2.StatusCode).To(Equal(http.StatusOK),
			"Second request should succeed from cache")
		Expect(resp2.Header.Get("X-Cache-Status")).To(Equal("HIT"))

		// Third request with ban header - upstream should return 403
		req, err := http.NewRequest(http.MethodGet, reqURL, nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("X-Response-Status", "403")

		resp3, err := client.Do(req)
		Expect(err).NotTo(HaveOccurred())
		resp3.Body.Close()
		Expect(resp3.StatusCode).To(Equal(http.StatusForbidden),
			"Banned request should return 403, not cached 200")
	})

	DescribeTable("should reject local or internal redirect targets",
		func(target string) {
			reqURL := testhelpers.GetNginxURL() + "/redirect?url=" + url.QueryEscape(target) +
				"&" + generateCacheBuster("deny-"+target)

			resp, err := client.Get(reqURL)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusBadGateway),
				"Local/internal redirect target %q should be rejected with 502", target)
		},
		Entry("loopback", "http://127.0.0.1/"),
		Entry("localhost", "http://localhost/"),
		Entry("cloud metadata", "http://169.254.169.254/latest/meta-data/"),
		Entry("other cluster service", "http://other-service.svc.cluster.local/"),
		Entry("absolute cluster service DNS name", "http://other-service.svc.cluster.local./"),
		Entry("relative location", "/relative"),
		Entry("protocol-relative location", "//127.0.0.1/"),
		Entry("expanded IPv6 loopback", "http://[0:0:0:0:0:0:0:1]/"),
		Entry("zero-padded IPv6 loopback", "http://[::0001]/"),
		Entry("IPv4-mapped loopback", "http://[::ffff:127.0.0.1]/"),
		Entry("IPv4 unspecified", "http://0.0.0.0/"),
		Entry("IPv6 unspecified", "http://[::]/"),
	)

	// stub_status on :8081 is reachable from inside the nginx pod. If the map
	// allowed these Locations, @handle_redirect would fetch stub_status and
	// return 200 with an "Active connections" body. Denial must yield 502
	// without that body — proving the local listener got no hit.
	assertStubStatusNotFetched := func(target, cacheBuster string) {
		reqURL := testhelpers.GetNginxURL() + "/redirect?url=" + url.QueryEscape(target) +
			"&" + generateCacheBuster(cacheBuster)

		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusBadGateway),
			"Denied redirect to stub_status via %q must be rejected", target)
		Expect(string(body)).NotTo(ContainSubstring("Active connections"),
			"stub_status must not have been fetched/cached via %q", target)
	}

	It("should not fetch IPv4-mapped loopback via reachable stub_status", func() {
		assertStubStatusNotFetched(
			"http://[::ffff:127.0.0.1]:8081/stub_status",
			"deny-mapped-stub-status",
		)
	})

	It("should not fetch IPv4 unspecified via reachable stub_status", func() {
		assertStubStatusNotFetched(
			"http://0.0.0.0:8081/stub_status",
			"deny-unspecified-stub-status",
		)
	})
})
