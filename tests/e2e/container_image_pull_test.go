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

	// Wait for logs to be written and available through Kubernetes API
	// EaaS environment needs more time for log propagation than local/quickcluster
	waitTime := 1 * time.Second
	if testhelpers.IsEaaSEnvironment() {
		waitTime = 5 * time.Second
		fmt.Printf("🔍 DEBUG: EaaS detected - waiting %v for log propagation\n", waitTime)
	}
	time.Sleep(waitTime)

	By("Verifying CDN requests in squid logs")
	// Check logs from all pods since the beginning
	cacheHitFound := false

	fmt.Printf("🔍 DEBUG: Checking logs from %d squid pods\n", len(pods))
	for _, pod := range pods {
		fmt.Printf("🔍 DEBUG: Retrieving logs from pod %s (container: %s)\n", pod.Name, squidContainerName)
		logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, squidContainerName, &beforeSequence)
		if err != nil {
			fmt.Printf("⚠️  WARNING: Failed to get logs from pod %s: %v\n", pod.Name, err)
			continue // Skip pods where we can't get logs
		}
		logStr := string(logs)
		fmt.Printf("🔍 DEBUG: Retrieved %d bytes of logs from pod %s\n", len(logStr), pod.Name)
		if logStr == "" {
			fmt.Printf("⚠️  WARNING: Pod %s has empty logs - might not have processed requests yet\n", pod.Name)
			continue
		}
		
		fmt.Printf("DEBUG: === Logs from pod %s ===\n", pod.Name)
		fmt.Printf("%s\n", logStr)

		// Check if this pod has a cache HIT
		hitRegex := regexp.MustCompile(`(?m)^.*TCP_HIT.*cdn(?:[0-9]{2})?\.quay\.io.*$`)
		if hitRegex.MatchString(logStr) {
			fmt.Printf("DEBUG: Found TCP_HIT in pod %s, verifying corresponding MISS\n", pod.Name)

			// Verify this pod also has the corresponding MISS
			Expect(logStr).To(
				MatchRegexp(`(?m)^.*TCP_MISS.*cdn(?:[0-9]{2})?\.quay\.io.*$`),
				"Pod with cache hit should also show TCP_MISS for the same image",
			)

			cacheHitFound = true
			fmt.Printf("DEBUG: Successfully verified both MISS and HIT in pod %s!\n", pod.Name)
			break // Found a pod with both MISS and HIT, no need to check others
		}
	}

	Expect(cacheHitFound).To(BeTrue(), "Should find at least one cache hit from any pod")

	fmt.Printf("DEBUG: Caching verification successful - found both TCP_MISS and TCP_HIT in the same pod!\n")
}
