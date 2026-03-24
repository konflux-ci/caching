package e2e_test

import (
	"fmt"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Squid Certificate Renewal", Ordered, Serial, func() {
	It("should restart squid containers when the TLS certificate is renewed", func() {
		initialCounts, err := testhelpers.GetContainerRestartCounts(ctx, clientset, namespace, deploymentName, squidContainerName)
		Expect(err).NotTo(HaveOccurred(), "Failed to get restart counts")

		By("Deleting the TLS secret to trigger cert-manager re-issuance")
		err = clientset.CoreV1().Secrets(namespace).Delete(ctx, testhelpers.SquidTLSSecretName, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to delete TLS secret")

		By("Waiting for cert-manager to re-create the TLS secret")
		Eventually(func() error {
			secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, testhelpers.SquidTLSSecretName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("secret not yet recreated: %w", err)
			}
			if len(secret.Data["tls.crt"]) == 0 {
				return fmt.Errorf("secret recreated but tls.crt is empty")
			}
			return nil
		}, 60*time.Second, 2*time.Second).Should(Succeed(),
			"cert-manager should recreate the TLS secret")

		By("Waiting for all squid containers to restart")
		testhelpers.WaitForContainerRestart(ctx, clientset, namespace, squidContainerName, initialCounts)

		By("Verifying all pods are ready after restart")
		_, err = testhelpers.GetPods(ctx, clientset, namespace, deploymentName)
		Expect(err).NotTo(HaveOccurred(), "All pods should become ready after cert renewal")
	})
})
