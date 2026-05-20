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

var _ = Describe("Nginx Proxy Caching Tests", Label("nginx"), Ordered, Serial, func() {
	const authSecretName = "upstream-auth"

	var client *http.Client

	BeforeAll(func() {
		err := testhelpers.CreateAuthSecret(ctx, clientset, authSecretName, "testuser", "testpass")
		Expect(err).NotTo(HaveOccurred())

		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled: true,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: testhelpers.GetNginxTestBackendURL(),
				},
				Auth: &testhelpers.NginxAuthValues{
					Enabled:    true,
					SecretName: authSecretName,
				},
				Cache: &testhelpers.NginxCacheValues{
					AllowList: []string{"^/content/"},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		client = testhelpers.NewNginxClient()

		DeferCleanup(func() {
			fmt.Println("Cleaning up auth secret...")
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

	It("should successfully proxy requests to the backend", func() {
		reqURL := testhelpers.GetNginxURL() + "/content/test"
		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK),
			"Request to backend should succeed")
	})

	It("should return X-Cache-Status: MISS on first request", func() {
		reqURL := testhelpers.GetNginxURL() + "/content/test?" + generateCacheBuster("nginx-cache-miss")

		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		cacheStatus := resp.Header.Get("X-Cache-Status")
		Expect(cacheStatus).To(Equal("MISS"),
			"First request should be a cache MISS")
	})

	It("should return X-Cache-Status: HIT on repeated request", func() {
		reqURL := testhelpers.GetNginxURL() + "/content/test?" + generateCacheBuster("nginx-cache-hit")

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
		reqURL := testhelpers.GetNginxURL() + "/not-cached"
		resp, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		cacheStatus := resp.Header.Get("X-Cache-Status")
		Expect(cacheStatus).To(Equal("BYPASS"),
			"Requests to non-matching paths should be BYPASS")
	})
})
