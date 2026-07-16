package helm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template ServiceMonitor Configuration", func() {
	Describe("Squid ServiceMonitor caFile Configuration", func() {
		It("should not include caFile when perSiteTLS.caFile is not set", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Prometheus: &testhelpers.PrometheusValues{
					ServiceMonitor: &testhelpers.ServiceMonitorValues{
						PerSiteTLS: &testhelpers.PerSiteTLSMonitorValues{
							Enabled: true,
						},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).NotTo(ContainSubstring("caFile"),
				"ServiceMonitor should not have caFile when not set")
		})

		It("should include caFile when perSiteTLS.caFile is set", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Prometheus: &testhelpers.PrometheusValues{
					ServiceMonitor: &testhelpers.ServiceMonitorValues{
						PerSiteTLS: &testhelpers.PerSiteTLSMonitorValues{
							Enabled: true,
							CaFile:  "/etc/prometheus/configmaps/proxy-ca/ca.crt",
						},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).To(ContainSubstring("caFile: /etc/prometheus/configmaps/proxy-ca/ca.crt"),
				"ServiceMonitor should have caFile when set")
		})
	})
})
