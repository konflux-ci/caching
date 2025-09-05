package e2e_test

import (
	"fmt"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Squid Access Logs", Ordered, func() {

	Describe("Filtering", func() {
		It("should omit internal squid manager and health check requests from access logs", func() {
			By("Waiting 10s for internal squid manager requests to be generated")
			start := metav1.Now()
			time.Sleep(10 * time.Second)

			squidPod, err := testhelpers.GetSquidPod(ctx, clientset, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to get squid pod")

			By("Getting the squid container logs from the last 10s")
			logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, squidPod.Name, "squid", &start)
			Expect(err).NotTo(HaveOccurred(), "Failed to get logs")

			By("Analyzing access logs for filtered content patterns")
			logString := string(logs)
			Expect(logString).NotTo(ContainSubstring("squid-internal-mgr"), "Internal manager requests should not be logged")
			Expect(logString).NotTo(ContainSubstring("NONE_NONE/000"), "Health check requests should not be logged")

			fmt.Printf("DEBUG: Squid access logs\n")
			fmt.Printf("==========================================\n")
			fmt.Printf("%s\n", logString)
			fmt.Printf("==========================================\n")
		})
	})
})
