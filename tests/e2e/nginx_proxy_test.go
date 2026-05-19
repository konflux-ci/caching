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

	It("should contact upstream on every request for allowList paths", func() {
		reqURL := testhelpers.GetNginxURL() + "/content/test?" + generateCacheBuster("always-upstream")

		// Make two requests to the same URL — both should return fresh
		// responses from the upstream since allowList locations don't cache directly
		resp1, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		body1, err := io.ReadAll(resp1.Body)
		Expect(err).NotTo(HaveOccurred())
		resp1.Body.Close()

		resp2, err := client.Get(reqURL)
		Expect(err).NotTo(HaveOccurred())
		body2, err := io.ReadAll(resp2.Body)
		Expect(err).NotTo(HaveOccurred())
		resp2.Body.Close()

		// The server_hits counter should differ, proving both requests
		// reached the upstream rather than being served from cache
		Expect(body2).NotTo(Equal(body1),
			"Responses should differ because each request contacts the upstream")
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
