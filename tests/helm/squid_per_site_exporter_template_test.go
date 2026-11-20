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
		Entry("configured exact hosts", []string{"quay.io", "registry-cache-1.example.com"}, "quay.io,registry-cache-1.example.com"),
		Entry("IP literals", []string{"192.0.2.1", "2001:db8::1"}, "192.0.2.1,2001:db8::1"),
		Entry("IPv4-mapped IPv6", []string{"::ffff:192.0.2.1"}, "::ffff:192.0.2.1"),
		Entry("case and trailing dot", []string{"QUAY.IO."}, "QUAY.IO."),
	)

	It("omits exporter arguments when disabled", func() {
		output, err := renderSquidTemplate(testhelpers.HelmValues{
			PerSiteExporter: &testhelpers.PerSiteExporterValues{Enabled: testhelpers.BoolPtr(false)},
		})
		Expect(err).NotTo(HaveOccurred())
		squid := containerByName(decodeSquidStatefulSet(output), "squid")
		Expect(squid.Args).To(BeEmpty())
	})

	DescribeTable("rejects unsupported or reserved entries",
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
		Entry("quote", `registry".example.com`),
		Entry("backslash", `registry\.example.com`),
		Entry("dollar sign", "registry$.example.com"),
		Entry("braces", "registry{cache}.example.com"),
		Entry("empty DNS label", "registry..example.com"),
		Entry("DNS root without a hostname", "."),
		Entry("hostname with port", "quay.io:443"),
		Entry("hexadecimal hostname with port", "cafe:443"),
		Entry("IPv4 with port", "192.0.2.1:8080"),
		Entry("IPv6 with port", "[2001:db8::1]:443"),
		Entry("bracketed IPv6", "[2001:db8::1]"),
		Entry("reserved label", "other"),
		Entry("reserved label with case and trailing dot", "OTHER."),
	)
})
