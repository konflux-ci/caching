package e2e_test

import (
	"crypto/tls"
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Nginx HTTPS Tests", Label("nginx"), Ordered, Serial, func() {
	var httpsClient *http.Client

	BeforeAll(func() {
		nexusConfig := testhelpers.NewNexusConfig()

		// Create Certificate resource for TLS
		err := testhelpers.CreateNginxCertificate(ctx, certManagerClient, "nginx-tls")
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			err := testhelpers.DeleteNginxCertificate(ctx, certManagerClient)
			Expect(err).NotTo(HaveOccurred())
		})

		// Configure nginx with TLS enabled
		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled:      true,
				ReplicaCount: 1,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: nexusConfig.URL,
				},
				Service: &testhelpers.NginxServiceValues{
					Port: 443,
				},
				TLS: &testhelpers.NginxTLSValues{
					Enabled:    true,
					SecretName: "nginx-tls",
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "nginx-tls", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to get TLS secret")

		caCert, ok := secret.Data["ca.crt"]
		Expect(ok).To(BeTrue(), "ca.crt should be in TLS secret")

		httpsClient, err = testhelpers.NewNginxHTTPSClient(caCert)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should serve HTTPS requests", func() {
		url := testhelpers.GetNginxHTTPSURL() + "/health"
		resp, err := httpsClient.Get(url)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("OK\n"))

		// Check TLS version from response
		Expect(resp.TLS).NotTo(BeNil(), "TLS connection state should not be nil")
		Expect(resp.TLS.Version).To(BeElementOf([]uint16{tls.VersionTLS12, tls.VersionTLS13}),
			"Should use TLS 1.2 or 1.3")
	})
})
