package e2e_test

import (
	"fmt"
	"io"
	"net/http"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Nginx Nexus Proxy Tests", Label("nginx", "external-deps"), Ordered, Serial, func() {
	const (
		authSecretName = "nexus-auth"
		goModulePath   = "/repository/go-proxy/golang.org/x/text/@v/list"
	)

	var client *http.Client

	BeforeAll(func() {
		nexusConfig := testhelpers.NewNexusConfig()

		err := testhelpers.CreateNexusAuthSecret(ctx, clientset, authSecretName, nexusConfig)
		Expect(err).NotTo(HaveOccurred())

		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled: true,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: nexusConfig.URL,
				},
				Auth: &testhelpers.NginxAuthValues{
					Enabled:    true,
					SecretName: authSecretName,
				},
				Cache: &testhelpers.NginxCacheValues{
					AllowList: []string{"^/repository/go-proxy/"},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		client = testhelpers.NewNginxClient()

		DeferCleanup(func() {
			fmt.Println("Cleaning up Nexus auth secret...")
			err = clientset.CoreV1().Secrets(namespace).Delete(ctx, authSecretName,
				metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	It("should respond to health check", func() {
		healthURL := testhelpers.GetNginxURL() + "/health"
		resp, err := client.Get(healthURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("OK\n"))
	})

	It("should successfully proxy requests to Nexus go-proxy", func() {
		// Make request through nginx
		reqURL := testhelpers.GetNginxURL() + goModulePath
		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK),
			"Request to go-proxy should succeed")
	})

	It("should return X-Cache-Status: MISS on first request", func() {
		uniquePath := goModulePath + "?" + generateCacheBuster("nginx-cache-miss")
		reqURL := testhelpers.GetNginxURL() + uniquePath

		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		cacheStatus := resp.Header.Get("X-Cache-Status")
		Expect(cacheStatus).To(Equal("MISS"),
			"First request should be a cache MISS")
	})

	It("should return X-Cache-Status: HIT on repeated request", func() {
		uniquePath := goModulePath + "?" + generateCacheBuster("nginx-cache-hit")
		reqURL := testhelpers.GetNginxURL() + uniquePath

		// First request - should be MISS
		resp1, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		body1, err := io.ReadAll(resp1.Body)
		Expect(err).NotTo(HaveOccurred())
		resp1.Body.Close()
		Expect(resp1.Header.Get("X-Cache-Status")).To(Equal("MISS"),
			"First request should be a cache MISS")

		// Second request - should be HIT
		resp2, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		body2, err := io.ReadAll(resp2.Body)
		Expect(err).NotTo(HaveOccurred())
		resp2.Body.Close()
		Expect(resp2.Header.Get("X-Cache-Status")).To(Equal("HIT"),
			"Second request should be a cache HIT")

		// Verify response bodies are identical
		Expect(body2).To(Equal(body1),
			"Cached response should match original response")
	})

	It("should NOT cache requests to non-matching paths", func() {
		// /service/rest/v1/status is a Nexus health endpoint that doesn't match the cache pattern
		nonCachedPath := "/service/rest/v1/status"
		reqURL := testhelpers.GetNginxURL() + nonCachedPath

		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		cacheStatus := resp.Header.Get("X-Cache-Status")
		Expect(cacheStatus).To(Equal("BYPASS"),
			"Requests to non-matching paths should be BYPASS")
	})
})
