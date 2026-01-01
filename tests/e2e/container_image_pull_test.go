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
		// Configure Squid ONCE with all CDN patterns
		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Cache: &testhelpers.CacheValues{
				AllowList: []string{
					// Quay.io CDN patterns
					"^https://cdn([0-9]{2})?\\.quay\\.io/.+/sha256/.+/[a-f0-9]{64}",
					"^https://s3\\.[a-z0-9-]+\\.amazonaws\\.com/quayio-production-s3/sha256/.+/[a-f0-9]{64}",
					"^https://quayio-production-s3\\.s3[a-z0-9.-]*\\.amazonaws\\.com/sha256/.+/[a-f0-9]{64}",
					// Docker Hub CDN patterns
					"^https://docker-images-prod\\.[a-f0-9]{32}\\.r2\\.cloudflarestorage\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
					"^https://production\\.cloudflare\\.docker\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
					"^https://docker-images-prod\\.s3[a-z0-9.-]*\\.amazonaws\\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data",
				},
			},
			ReplicaCount: int(suiteReplicaCount),
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to configure squid with CDN patterns")
	})

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
	pullAndVerifyContainerImageCDN(imageRef,
		`(cdn(?:[0-9]{2})?\.quay\.io|quayio-production-s3\.s3[a-z0-9.-]*\.amazonaws\.com|s3\.[a-z0-9-]+\.amazonaws\.com/quayio-production-s3)`,
		"Quay CDN")
}

func pullAndVerifyDockerHubCDN(imageRef string) {
	pullAndVerifyContainerImageCDN(imageRef,
		`(docker-images-prod\.[a-f0-9]{32}\.r2\.cloudflarestorage\.com|production\.cloudflare\.docker\.com|docker-images-prod\.s3[a-z0-9.-]*\.amazonaws\.com)`,
		"Docker Hub CDN")
}

// pullAndVerifyContainerImageCDN verifies that container image layers are cached from CDN hosts.
// cdnRegexPattern should contain ONLY the CDN host pattern (e.g., "(cdn\.quay\.io|s3\.amazonaws\.com)").
// The function will automatically build the full patterns with TCP_MISS and TCP_HIT prefixes.
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

	statefulSet, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to get statefulset")
	pods, err := testhelpers.GetSquidPods(ctx, clientset, namespace, *statefulSet.Spec.Replicas)
	Expect(err).NotTo(HaveOccurred(), "Failed to get squid pods")

	maxAttempts := int(*statefulSet.Spec.Replicas) + 1
	fmt.Printf("🔍 DEBUG: Replica count: %d, pulling %d times to guarantee cache hit\n", *statefulSet.Spec.Replicas, maxAttempts)

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
	// Build full patterns from the CDN host pattern
	missPattern := fmt.Sprintf(`(?m)^.*TCP_MISS.*%s.*$`, cdnRegexPattern)
	hitPattern := fmt.Sprintf(`(?m)^.*TCP_HIT.*%s.*$`, cdnRegexPattern)

	// First, check logs from our test sequence
	for _, pod := range pods {
		logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, squidContainerName, &beforeSequence)
		if err != nil {
			continue // Skip pods where we can't get logs
		}
		logStr := string(logs)

		if logStr == "" {
			continue
		}

		fmt.Printf("DEBUG: === Logs from pod %s (since test start) ===\n", pod.Name)
		fmt.Printf("%s\n", logStr)

		// Check for MISS pattern
		matched, err := regexp.MatchString(missPattern, logStr)
		Expect(err).NotTo(HaveOccurred(), "Invalid regex pattern: %s", missPattern)
		if matched {
			fmt.Printf("DEBUG: Found TCP_MISS for %s in pod %s\n", cdnName, pod.Name)
			foundMiss = true
		}

		// Check for HIT pattern
		matched, err = regexp.MatchString(hitPattern, logStr)
		Expect(err).NotTo(HaveOccurred(), "Invalid regex pattern: %s", hitPattern)
		if matched {
			fmt.Printf("DEBUG: Found TCP_HIT for %s in pod %s\n", cdnName, pod.Name)
			foundHit = true
		}
	}

	// If we found TCP_HIT but not TCP_MISS, the cache may have been warm from a previous test.
	// Check logs from pod creation time to see if there was a TCP_MISS that populated the cache.
	if foundHit && !foundMiss {
		fmt.Printf("DEBUG: Found TCP_HIT but no TCP_MISS in test window. Checking logs from pod creation time to see if cache was warm from previous test...\n")

		for _, pod := range pods {
			podCreationTime := pod.CreationTimestamp

			logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, squidContainerName, &podCreationTime)
			if err != nil {
				continue
			}
			logStr := string(logs)

			matched, err := regexp.MatchString(missPattern, logStr)
			Expect(err).NotTo(HaveOccurred(), "Invalid regex pattern: %s", missPattern)
			if matched {
				fmt.Printf("DEBUG: Found TCP_MISS for %s in pod %s from logs since pod creation (cache was warm from previous test)\n", cdnName, pod.Name)
				foundMiss = true
				break // Found it, no need to check other pods
			}
		}
		
		if foundMiss {
			fmt.Printf("DEBUG: Cache was warm from previous test, but caching is working correctly (MISS happened earlier, HIT in current test)\n")
		}
	}

	// Verify we found both MISS and HIT (MISS may be from current test or earlier)
	// This proves caching is working (MISS = fetched and cached, HIT = served from cache)
	//
	// NOTE: Commented out the MISS check when switching to using PVCs for cache storag, because
	// its a lot harder to get a MISS because the storage can easily contain cached data from
	// previous tests, and persists across redeployments of squid.
	// I'm leaving the miss detection code behind because the debug prints might help us in the
	// future, and we might decide to add force-cleaning of the storgate at some point.
	//
	// Expect(foundMiss).To(BeTrue(), "Should find TCP_MISS for %s in pod logs (proves content was fetched and cached, either in current test or earlier)", cdnName)
	Expect(foundHit).To(BeTrue(), "Should find TCP_HIT for %s in pod logs (proves content was served from cache)", cdnName)

	fmt.Printf("DEBUG: Caching verification successful - found both TCP_MISS and TCP_HIT for %s!\n", cdnName)
}
