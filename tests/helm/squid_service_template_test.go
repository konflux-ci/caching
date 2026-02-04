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
})
