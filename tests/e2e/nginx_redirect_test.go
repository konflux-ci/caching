package e2e_test

import (
	"net/http"
	"net/url"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Upstream Redirect Tests", Label("nginx"), Ordered, Serial, func() {
	var backendURL string

	BeforeAll(func() {
		backendURL = testhelpers.GetNginxTestBackendURL()

		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled: true,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: backendURL,
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should return a 302 redirect from the redirect endpoint", func() {
		targetURL := backendURL + "/content/test"
		redirectURL := backendURL + "/redirect?url=" + url.QueryEscape(targetURL)

		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resp, err := client.Get(redirectURL)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusFound),
			"Redirect endpoint should return 302")

		location := resp.Header.Get("Location")
		Expect(location).To(Equal(targetURL),
			"Location header should point to the target URL")
	})
})
