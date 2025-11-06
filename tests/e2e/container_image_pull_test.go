package e2e_test

import (
	"context"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Container image pulls", Ordered, Serial, Label("external-deps"), func() {
	AfterAll(func() {
		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{})
		Expect(err).NotTo(HaveOccurred(), "Failed to restore squid cache defaults")
	})

	DescribeTable("should cache layers from quay CDNs",
		pullAndVerifyQuayCDN,
		Entry("quay.io", "quay.io/konflux-ci/caching/squid@sha256:497644fae8de47ed449126735b02d54fdc152ef22634e32f175186094c2d638e"),
		Entry("registry.access.redhat.com", "registry.access.redhat.com/ubi10-minimal:late@sha256:649f7ce8082531148ac5e45b61612046a21e36648ab096a77e6ba0c94428cf60"),
	)
})

func pullAndVerifyQuayCDN(imageRef string) {
	// Reconfigured squid to force the cache to be cleared
	err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
		Cache: &testhelpers.CacheValues{
			AllowList: []string{
				"^https://cdn([0-9]{2})?\\.quay\\.io/.+/sha256/.+/[a-f0-9]{64}",
				"dummy-" + imageRef, // Unique dummy value to ensure the pod is recreated
			},
		},
	})
	Expect(err).NotTo(HaveOccurred(), "Failed to configure squid")

	cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(context.Background(), namespace+"-ca-bundle", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to get "+namespace+"-ca-bundle ConfigMap")

	client, err := testhelpers.NewTrustedSquidCachingClient(
		serviceName,
		namespace,
		[]byte(cm.Data["ca-bundle.crt"]),
		[]byte(nil),
	)
	Expect(err).NotTo(HaveOccurred(), "Failed to create trusted squid caching client")

	pod, err := testhelpers.GetSquidPod(ctx, clientset, namespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to get squid pod")

	By("Pulling the image for the first time (expect MISS)")
	before := metav1.Now()
	err = testhelpers.PullContainerImage(&client.Transport, imageRef)
	Expect(err).NotTo(HaveOccurred(), "Failed to pull container image")

	By("Verifying CDN request MISS in squid logs")
	logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, "squid", &before)
	Expect(err).NotTo(HaveOccurred(), "Failed to get logs after first pull")
	logStr := string(logs)
	Expect(logStr).To(
		MatchRegexp("(?m)^.*TCP_MISS.*cdn(?:[0-9]{2})?\\.quay\\.io.*$"),
		"First pull should be a MISS from a quay CDN host",
	)
	Expect(logStr).To(Not(ContainSubstring("TCP_HIT")), "First pull should not produce a HIT from a quay CDN host")

	By("Pulling the image for a second time (expect HIT)")
	before = metav1.Now()
	err = testhelpers.PullContainerImage(&client.Transport, imageRef)
	Expect(err).NotTo(HaveOccurred(), "Failed to pull container image")

	By("Verifying CDN request HIT in squid logs")
	logs, err = testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, "squid", &before)
	Expect(err).NotTo(HaveOccurred(), "Failed to get logs after second pull")
	Expect(string(logs)).To(
		MatchRegexp("(?m)^.*TCP_HIT.*cdn(?:[0-9]{2})?\\.quay\\.io.*$"),
		"Second pull should be a HIT from a quay CDN host",
	)
}
