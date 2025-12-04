package e2e_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Container image pulls", Ordered, Serial, Label("external-deps"), func() {
	AfterAll(func() {
		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			ReplicaCount: int(suiteReplicaCount),
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to restore squid cache defaults")
	})

	DescribeTable("should cache layers from quay CDNs",
		pullAndVerifyQuayCDN,
		Entry("quay.io", "quay.io/konflux-ci/caching/squid@sha256:497644fae8de47ed449126735b02d54fdc152ef22634e32f175186094c2d638e"),
		Entry("registry.access.redhat.com", "registry.access.redhat.com/ubi10-minimal:late@sha256:649f7ce8082531148ac5e45b61612046a21e36648ab096a77e6ba0c94428cf60"),
	)

	DescribeTable("should cache layers from docker.io CDNs",
		pullAndVerifyDockerHubCDN,
		Entry("docker.io/library/alpine", "docker.io/library/alpine:3.19@sha256:13b7e62e8df80264dbb747995705a986aa530415763a6c58f84a3ca8af9a5bcd"),
		Entry("docker.io/library/nginx", "docker.io/library/nginx:1.25@sha256:4c0fdaa8b6341bfdeca5f18f7837462c80cff90527ee35ef185571e1c327beac"),
	)
})

func pullAndVerifyQuayCDN(imageRef string) {
	// Reconfigure squid to force the cache to be cleared
	err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
		Cache: &testhelpers.CacheValues{
			AllowList: []string{
				"^https://cdn([0-9]{2})?\\.quay\\.io/.+/sha256/.+/[a-f0-9]{64}",
				"^https://s3\\.[a-z0-9-]+\\.amazonaws\\.com/quayio-production-s3/sha256/.+/[a-f0-9]{64}",
				"^https://quayio-production-s3\\.s3[a-z0-9.-]*\\.amazonaws\\.com/sha256/.+/[a-f0-9]{64}",
				"dummy-" + imageRef, // Unique dummy value to ensure the pod is recreated
			},
		},
		ReplicaCount: int(suiteReplicaCount),
	})
	Expect(err).NotTo(HaveOccurred(), "Failed to configure squid")

	pullAndVerifyContainerImageCDN(imageRef,
		`(?m)^.*TCP_(MISS|HIT).*(cdn(?:[0-9]{2})?\.quay\.io|quayio-production-s3\.s3[a-z0-9.-]*\.amazonaws\.com|s3\.[a-z0-9-]+\.amazonaws\.com/quayio-production-s3).*$`,
		"Quay CDN")
}

func pullAndVerifyDockerHubCDN(imageRef string) {
	// Reconfigure squid to force the cache to be cleared
	err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
		Cache: &testhelpers.CacheValues{
			AllowList: []string{
				"^https://docker-images-prod\\.[a-f0-9]{32}\\.r2\\.cloudflarestorage\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
				"^https://production\\.cloudflare\\.docker\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
				"^https://docker-images-prod\\.s3[a-z0-9.-]*\\.amazonaws\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
				"dummy-" + imageRef, // Unique dummy value to ensure the pod is recreated
			},
		},
		ReplicaCount: int(suiteReplicaCount),
	})
	Expect(err).NotTo(HaveOccurred(), "Failed to configure squid")

	pullAndVerifyContainerImageCDN(imageRef,
		`(?m)^.*TCP_(MISS|HIT).*(docker-images-prod\.[a-f0-9]{32}\.r2\.cloudflarestorage\.com|production\.cloudflare\.docker\.com|docker-images-prod\.s3[a-z0-9.-]*\.amazonaws\.com).*$`,
		"Docker Hub CDN")
}

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
	fmt.Printf("🔍 DEBUG: Replica count: %d, pulling %d times to guarantee cache hit\n", *deployment.Spec.Replicas, maxAttempts)

	// Get timestamp before starting pulls
	beforeSequence := metav1.Now()

	By("Pulling the image multiple times to guarantee a cache hit")
	// Pull (replicas + 1) times - pigeonhole principle guarantees at least one pod gets hit twice
	for i := range maxAttempts {
		fmt.Printf("🔍 DEBUG: Pull attempt %d/%d\n", i+1, maxAttempts)
		err = testhelpers.PullContainerImage(&client.Transport, imageRef)
		Expect(err).NotTo(HaveOccurred(), "Failed to pull container image")
	}

	// Wait a moment to ensure all requests are logged
	time.Sleep(1 * time.Second)

	By("Verifying CDN requests in squid logs")
	// Collect logs from all pods and check for MISS and HIT patterns
	var foundMiss, foundHit bool
	missPattern := strings.Replace(cdnRegexPattern, "TCP_(MISS|HIT)", "TCP_MISS", 1)
	hitPattern := strings.Replace(cdnRegexPattern, "TCP_(MISS|HIT)", "TCP_HIT", 1)

	for _, pod := range pods {
		logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, squidContainerName, &beforeSequence)
		if err != nil {
			continue // Skip pods where we can't get logs
		}
		logStr := string(logs)

		if logStr == "" {
			continue
		}

		fmt.Printf("DEBUG: === Logs from pod %s ===\n", pod.Name)
		fmt.Printf("%s\n", logStr)

		// Check for MISS pattern
		if matched, _ := regexp.MatchString(missPattern, logStr); matched {
			fmt.Printf("DEBUG: Found TCP_MISS for %s in pod %s\n", cdnName, pod.Name)
			foundMiss = true
		}

		// Check for HIT pattern
		if matched, _ := regexp.MatchString(hitPattern, logStr); matched {
			fmt.Printf("DEBUG: Found TCP_HIT for %s in pod %s\n", cdnName, pod.Name)
			foundHit = true
		}
	}

	// Verify we found both MISS and HIT across all pods
	// This proves caching is working (MISS = fetched and cached, HIT = served from cache)
	Expect(foundMiss).To(BeTrue(), "Should find TCP_MISS for %s in pod logs (proves content was fetched and cached)", cdnName)
	Expect(foundHit).To(BeTrue(), "Should find TCP_HIT for %s in pod logs (proves content was served from cache)", cdnName)

	fmt.Printf("DEBUG: Caching verification successful - found CDN requests from %s!\n", cdnName)
}
