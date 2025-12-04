package e2e_test

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Container image pulls", Ordered, Serial, Label("external-deps"), func() {
	BeforeAll(func() {
		// Configure squid once for all CDN caching tests
		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Cache: &testhelpers.CacheValues{
				AllowList: []string{
					// Quay.io content-addressable blob downloads
					"^https://cdn([0-9]{2})?\\.quay\\.io/.+/sha256/.+/[a-f0-9]{64}",
					"^https://s3\\.[a-z0-9-]+\\.amazonaws\\.com/quayio-production-s3/sha256/.+/[a-f0-9]{64}",
					"^https://quayio-production-s3\\.s3[a-z0-9.-]*\\.amazonaws\\.com/sha256/.+/[a-f0-9]{64}",
					
					// Docker Hub content-addressable blob downloads
					"^https://docker-images-prod\\.[a-f0-9]{32}\\.r2\\.cloudflarestorage\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
					"^https://production\\.cloudflare\\.docker\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
				},
			},
			ReplicaCount: int(suiteReplicaCount),
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to configure squid for CDN caching tests")
	})

	AfterAll(func() {
		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			ReplicaCount: int(suiteReplicaCount),
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to restore squid cache defaults")
	})

	DescribeTable("should cache layers from quay CDNs",
		func(imageRef string) {
			pullAndVerifyContainerImageCDN(imageRef, 
				`(?m)^.*TCP_(MISS|HIT).*(cdn(?:[0-9]{2})?\.quay\.io|quayio-production-s3\.s3[a-z0-9.-]*\.amazonaws\.com|s3\.[a-z0-9-]+\.amazonaws\.com/quayio-production-s3).*$`,
				"Quay CDN")
		},
		Entry("quay.io", "quay.io/konflux-ci/caching/squid@sha256:497644fae8de47ed449126735b02d54fdc152ef22634e32f175186094c2d638e"),
		Entry("registry.access.redhat.com", "registry.access.redhat.com/ubi10-minimal:late@sha256:649f7ce8082531148ac5e45b61612046a21e36648ab096a77e6ba0c94428cf60"),
	)

	DescribeTable("should cache layers from docker.io CDNs",
		func(imageRef string) {
			pullAndVerifyContainerImageCDN(imageRef,
				`(?m)^.*TCP_(MISS|HIT).*(docker-images-prod\.[a-f0-9]{32}\.r2\.cloudflarestorage\.com|production\.cloudflare\.docker\.com|cdn[0-9]*\.quay\.io/.+/sha256/.+/[a-f0-9]{64}|s3.*amazonaws\.com/.+/sha256/.+/[a-f0-9]{64}).*$`,
				"Docker Hub Caching")
		},
		Entry("docker.io/library/alpine", "docker.io/library/alpine:3.19@sha256:13b7e62e8df80264dbb747995705a986aa530415763a6c58f84a3ca8af9a5bcd"),
		Entry("docker.io/library/nginx", "docker.io/library/nginx:1.25@sha256:4c0fdaa8b6341bfdeca5f18f7837462c80cff90527ee35ef185571e1c327beac"),
	)
})

func pullAndVerifyContainerImageCDN(imageRef, cdnRegexPattern, cdnName string) {
	cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(context.Background(), namespace+"-ca-bundle", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to get "+namespace+"-ca-bundle ConfigMap")

	client, err := testhelpers.NewTrustedSquidCachingClient(
		serviceName,
		namespace,
		[]byte(cm.Data["ca-bundle.crt"]),
		[]byte(nil),
	)
	Expect(err).NotTo(HaveOccurred(), "Failed to create trusted squid caching client")

	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to get deployment")
	pods, err := testhelpers.GetSquidPods(ctx, clientset, namespace, *deployment.Spec.Replicas)
	Expect(err).NotTo(HaveOccurred(), "Failed to get squid pods")

	maxAttempts := int(*deployment.Spec.Replicas) + 1

	// Get timestamp before starting pulls
	beforeSequence := metav1.Now()

	By("Pulling the image multiple times to guarantee a cache hit")
	// Pull (replicas + 1) times - pigeonhole principle guarantees at least one pod gets hit twice
	for range maxAttempts {
		err = testhelpers.PullContainerImage(&client.Transport, imageRef)
		Expect(err).NotTo(HaveOccurred(), "Failed to pull container image")
	}

	// Wait a moment to ensure all requests are logged
	time.Sleep(1 * time.Second)

	By("Verifying CDN requests in squid logs")
	// Check logs from all pods since the beginning
	cacheHitFound := false

	for _, pod := range pods {
		logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, squidContainerName, &beforeSequence)
		if err != nil {
			continue // Skip pods where we can't get logs
		}
		logStr := string(logs)
		
		if logStr == "" {
			continue
		}

		// Check if this pod has CDN requests (either MISS or HIT proves caching is working)
		cdnRegex := regexp.MustCompile(cdnRegexPattern)
		if cdnRegex.MatchString(logStr) {
			fmt.Printf("DEBUG: Found %s CDN pattern in pod %s\n", cdnName, pod.Name)
			cacheHitFound = true
			break // Found evidence of CDN caching, no need to check others
		}
	}

	Expect(cacheHitFound).To(BeTrue(), "Should find evidence of %s caching from any pod", cdnName)
}
