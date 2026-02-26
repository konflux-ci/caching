package helm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template Squid Service Configuration", func() {
	Describe("Squid Service trafficDistribution Configuration", func() {
		It("should not include trafficDistribution when not set", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{})
			Expect(err).NotTo(HaveOccurred())

			service := extractSquidServiceSection(output)
			Expect(service).NotTo(ContainSubstring("trafficDistribution"), "Regular service should not have trafficDistribution when not set")
		})

		It("should include trafficDistribution when set", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Service: &testhelpers.ServiceValues{
					TrafficDistribution: "PreferSameZone",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			service := extractSquidServiceSection(output)
			Expect(service).To(ContainSubstring("trafficDistribution: PreferSameZone"), "Regular service should have trafficDistribution: PreferSameZone")
		})

		It("should not include trafficDistribution when set but kube version is before 1.30", func() {
			output, err := testhelpers.RenderHelmTemplateWithKubeVersion(chartPath, testhelpers.SquidHelmValues{
				Service: &testhelpers.ServiceValues{
					TrafficDistribution: "PreferSameZone",
				},
			}, "1.29.0")
			Expect(err).NotTo(HaveOccurred())

			service := extractSquidServiceSection(output)
			Expect(service).NotTo(ContainSubstring("trafficDistribution"), "Regular service must not have trafficDistribution on K8s < 1.30")
		})
	})

	Describe("Squid Custom Name Configuration", func() {
		It("should use custom name for all squid resource names and labels", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Squid: &testhelpers.SquidValues{
					Name: "my-proxy",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			service := extractSquidServiceSection(output)
			Expect(service).To(ContainSubstring("name: my-proxy"), "Service should use custom name")
			Expect(service).To(ContainSubstring("app.kubernetes.io/name: my-proxy"), "Service label should use custom name")

			headless := extractSquidHeadlessServiceSection(output)
			Expect(headless).To(ContainSubstring("name: my-proxy-headless"), "Headless service should use custom name with -headless suffix")

			statefulSet := extractSquidDeploymentSection(output)
			Expect(statefulSet).To(ContainSubstring("name: my-proxy"), "StatefulSet should use custom name")
			Expect(statefulSet).To(ContainSubstring("serviceName: my-proxy-headless"), "StatefulSet serviceName should reference custom headless name")

			configMap := extractSquidConfigMapSection(output)
			Expect(configMap).To(ContainSubstring("name: my-proxy-config"), "ConfigMap should use custom name with -config suffix")
		})
	})
})
