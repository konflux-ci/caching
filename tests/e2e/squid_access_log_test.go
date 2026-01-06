package e2e_test

import (
	"fmt"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Squid Access Logs", Ordered, func() {

	var statefulSet *appsv1.StatefulSet
	var err error

	Describe("Filtering", func() {
		It("should omit internal squid manager and health check requests from access logs", func() {
			By("Waiting 10s for internal squid manager requests to be generated")
			start := metav1.Now()
			time.Sleep(10 * time.Second)
			statefulSet, err = clientset.AppsV1().StatefulSets(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid statefulset")

			squidPods, err := testhelpers.GetSquidPods(ctx, clientset, namespace, *statefulSet.Spec.Replicas)
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid pod")

			for _, squidPod := range squidPods {
				By("Getting the squid container logs from the last 10s")
				logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, squidPod.Name, squidContainerName, &start)
				Expect(err).NotTo(HaveOccurred(), "Failed to get logs")

				By("Analyzing access logs for filtered content patterns")
				logString := string(logs)
				Expect(logString).NotTo(ContainSubstring("squid-internal-mgr"), "Internal manager requests should not be logged")
				Expect(logString).NotTo(ContainSubstring("NONE_NONE/000"), "Health check requests should not be logged")

				fmt.Printf("DEBUG: Squid access logs for pod %s\n", squidPod.Name)
				fmt.Printf("==========================================\n")
				fmt.Printf("%s\n", logString)
				fmt.Printf("==========================================\n")
			}
		})
	})
})
