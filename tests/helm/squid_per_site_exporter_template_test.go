package helm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template Squid Per-Site Exporter", func() {
	DescribeTable("passes the hostname allow-list to the exporter",
		func(hosts []string, expected string) {
			output, err := renderSquidTemplate(testhelpers.HelmValues{
				PerSiteExporter: &testhelpers.PerSiteExporterValues{AllowedHostnames: hosts},
			})
			Expect(err).NotTo(HaveOccurred())

			squid := containerByName(decodeSquidStatefulSet(output), "squid")
			Expect(squid.Args).To(ContainElement("squid-with-per-site-exporter"))
			Expect(squid.Args[len(squid.Args)-2:]).To(Equal([]string{"-metrics.allowed-hostnames", expected}))
		},
		Entry("empty by default", []string(nil), ""),
		Entry("configured exact hosts", []string{"quay.io", "registry.example.com"}, "quay.io,registry.example.com"),
	)

	It("omits exporter arguments when disabled", func() {
		output, err := renderSquidTemplate(testhelpers.HelmValues{
			PerSiteExporter: &testhelpers.PerSiteExporterValues{Enabled: testhelpers.BoolPtr(false)},
		})
		Expect(err).NotTo(HaveOccurred())
		squid := containerByName(decodeSquidStatefulSet(output), "squid")
		Expect(squid.Args).To(BeEmpty())
	})

	DescribeTable("rejects entries that could change comma-separated list semantics",
		func(host string) {
			_, err := renderSquidTemplate(testhelpers.HelmValues{
				PerSiteExporter: &testhelpers.PerSiteExporterValues{AllowedHostnames: []string{host}},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("allowedHostnames"))
		},
		Entry("embedded commas", "one.example.com,two.example.com"),
		Entry("empty entries", ""),
		Entry("URLs", "https://example.com"),
		Entry("wildcards", "*.example.com"),
	)
})
