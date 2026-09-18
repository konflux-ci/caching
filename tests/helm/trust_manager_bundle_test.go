package helm_test

import (
	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Helm Template Trust-Manager Bundle Configuration", func() {
	It("should preserve the default Bundle name and target key", func() {
		output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{})
		Expect(err).NotTo(HaveOccurred())
		var b map[string]interface{}
		Expect(yaml.Unmarshal([]byte(extractTrustManagerBundleSection(output)), &b)).To(Succeed())
		Expect(b["metadata"].(map[string]interface{})["name"]).To(Equal("caching-ca-bundle"))
		spec := b["spec"].(map[string]interface{})
		Expect(spec["sources"]).To(HaveLen(1))
		Expect(spec["target"].(map[string]interface{})["configMap"]).To(Equal(map[string]interface{}{"key": "ca-bundle.crt"}))
	})
	It("should keep the legacy Bundle name while trusting both proxy roots", func() {
		output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
			Squid: &testhelpers.SquidValues{Namespace: "container-image-proxy"},
			SelfsignedBundle: &testhelpers.SelfsignedBundleValues{
				Name:              "caching-ca-bundle",
				AdditionalSources: []map[string]interface{}{{"secret": map[string]interface{}{"name": "caching-root-ca-secret", "key": "ca.crt"}}},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		var b map[string]interface{}
		Expect(yaml.Unmarshal([]byte(extractTrustManagerBundleSection(output)), &b)).To(Succeed())
		Expect(b["metadata"].(map[string]interface{})["name"]).To(Equal("caching-ca-bundle"))
		spec := b["spec"].(map[string]interface{})
		Expect(spec["sources"]).To(ConsistOf(
			map[string]interface{}{"secret": map[string]interface{}{"name": "container-image-proxy-root-ca-secret", "key": "ca.crt"}},
			map[string]interface{}{"secret": map[string]interface{}{"name": "caching-root-ca-secret", "key": "ca.crt"}},
		))
		Expect(spec["target"].(map[string]interface{})["configMap"]).To(Equal(map[string]interface{}{"key": "ca-bundle.crt"}))
	})
	It("should allow stopping management of the legacy Bundle without removing CA resources", func() {
		output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{SelfsignedBundle: &testhelpers.SelfsignedBundleValues{Enabled: testhelpers.BoolPtr(false)}})
		Expect(err).NotTo(HaveOccurred())
		Expect(extractTrustManagerBundleSection(output)).To(BeEmpty())
		Expect(extractSection(output, "# Source: caching/templates/self-signed-cluster-issuer.yaml")).NotTo(BeEmpty())
	})
	It("should pass through additional trust-manager sources", func() {
		for _, source := range []map[string]interface{}{
			{"secret": map[string]interface{}{"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"trust": "enabled"}}, "includeAllKeys": true}},
			{"configMap": map[string]interface{}{"name": "extra-ca", "key": "ca.crt"}},
			{"inLine": "certificate"},
			{"useDefaultCAs": true},
		} {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				SelfsignedBundle: &testhelpers.SelfsignedBundleValues{AdditionalSources: []map[string]interface{}{source}},
			})
			Expect(err).NotTo(HaveOccurred())
			var bundle map[string]interface{}
			Expect(yaml.Unmarshal([]byte(extractTrustManagerBundleSection(output)), &bundle)).To(Succeed())
			Expect(bundle["spec"].(map[string]interface{})["sources"]).To(ContainElement(source))
		}
	})

})
