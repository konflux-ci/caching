package helm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template Squid ServiceMonitor Configuration", func() {
	Describe("Squid ServiceMonitor Rendering Conditions", func() {
		It("should not render ServiceMonitor when squid is disabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Squid: &testhelpers.SquidValues{
					Enabled: testhelpers.BoolPtr(false),
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).To(BeEmpty(), "ServiceMonitor should not be rendered when squid.enabled=false")
		})

		It("should not render ServiceMonitor when squidExporter is disabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				SquidExporter: &testhelpers.SquidExporterValues{
					Enabled: testhelpers.BoolPtr(false),
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).To(BeEmpty(), "ServiceMonitor should not be rendered when squidExporter.enabled=false")
		})

		It("should not render ServiceMonitor when prometheus.serviceMonitor is disabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Prometheus: &testhelpers.PrometheusValues{
					ServiceMonitor: &testhelpers.ServiceMonitorValues{
						Enabled: testhelpers.BoolPtr(false),
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).To(BeEmpty(), "ServiceMonitor should not be rendered when prometheus.serviceMonitor.enabled=false")
		})

		It("should not render ServiceMonitor when multiple components are disabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Squid: &testhelpers.SquidValues{
					Enabled: testhelpers.BoolPtr(false),
				},
				SquidExporter: &testhelpers.SquidExporterValues{
					Enabled: testhelpers.BoolPtr(false),
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).To(BeEmpty(), "ServiceMonitor should not be rendered when multiple components are disabled")
		})

		It("should render ServiceMonitor when all three conditions are enabled (default behavior)", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).NotTo(BeEmpty(), "ServiceMonitor should be rendered when all conditions are true (default)")
			Expect(serviceMonitor).To(ContainSubstring("kind: ServiceMonitor"))
		})

		It("should render ServiceMonitor when all three conditions are explicitly enabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Squid: &testhelpers.SquidValues{
					Enabled: testhelpers.BoolPtr(true),
				},
				SquidExporter: &testhelpers.SquidExporterValues{
					Enabled: testhelpers.BoolPtr(true),
				},
				Prometheus: &testhelpers.PrometheusValues{
					ServiceMonitor: &testhelpers.ServiceMonitorValues{
						Enabled: testhelpers.BoolPtr(true),
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			serviceMonitor := extractSquidServiceMonitorSection(output)
			Expect(serviceMonitor).NotTo(BeEmpty(), "ServiceMonitor should be rendered when all three conditions are explicitly true")
			Expect(serviceMonitor).To(ContainSubstring("kind: ServiceMonitor"))
		})
	})
})
