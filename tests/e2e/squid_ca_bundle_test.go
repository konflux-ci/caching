package e2e_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var _ = Describe("Squid CA bundle distribution", Serial, Label("ca-bundle"), func() {
	It("resolves the chart CA source from the trust namespace and distributes the same certificate", func() {
		// These are the namespaces and names used by the E2E chart installation.
		root, err := certManagerClient.CertmanagerV1().Certificates("cert-manager").Get(
			ctx, namespace+"-self-signed-ca", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "Root Certificate must be in the trust namespace")
		Expect(root.Spec.SecretName).To(Equal(namespace + "-root-ca-secret"))
		secret, err := clientset.CoreV1().Secrets("cert-manager").Get(ctx, root.Spec.SecretName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "trust-manager must be able to resolve the root CA source")
		want := caFingerprints(secret.Data["ca.crt"])
		Eventually(func(g Gomega) {
			cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, namespace+"-ca-bundle", metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(caFingerprints([]byte(cm.Data["ca-bundle.crt"]))).To(Equal(want))
		}, timeout, interval).Should(Succeed())
	})

	It("rejects a source in the wrong namespace and distributes both CAs once it is available", func() {
		config, err := testhelpers.GetRESTConfig()
		Expect(err).NotTo(HaveOccurred())
		dc, err := dynamic.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		bundles := dc.Resource(schema.GroupVersionResource{
			Group: "trust.cert-manager.io", Version: "v1alpha1", Resource: "bundles",
		})

		// Use isolated resources: never patch the chart-owned Bundle or CA Secrets.
		tenant, err := clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "ca-bundle-e2e-"},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(clientset.CoreV1().Namespaces().Delete(ctx, tenant.Name, metav1.DeleteOptions{})).To(Succeed())
		})
		name := tenant.Name
		root, err := clientset.CoreV1().Secrets("cert-manager").Get(ctx, namespace+"-root-ca-secret", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		other, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, "test-server-bundle", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		otherCA := []byte(other.Data["ca.crt"])
		Expect(caFingerprints(otherCA)).NotTo(Equal(caFingerprints(root.Data["ca.crt"])))

		By("placing a public CA source outside trust-manager's trust namespace")
		_, err = clientset.CoreV1().Secrets(tenant.Name).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string][]byte{"ca.crt": otherCA},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, err = bundles.Create(ctx, &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "trust.cert-manager.io/v1alpha1", "kind": "Bundle",
			"metadata": map[string]interface{}{"name": name},
			"spec": map[string]interface{}{
				"sources": []interface{}{
					map[string]interface{}{"secret": map[string]interface{}{"name": namespace + "-root-ca-secret", "key": "ca.crt"}},
					map[string]interface{}{"secret": map[string]interface{}{"name": name, "key": "ca.crt"}},
				},
				"target": map[string]interface{}{
					"configMap": map[string]interface{}{"key": "ca-bundle.crt"},
					"namespaceSelector": map[string]interface{}{
						"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": tenant.Name},
					},
				},
			},
		}}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(bundles.Delete(ctx, name, metav1.DeleteOptions{})).To(Succeed()) })

		waitForSync := func(status string) {
			Eventually(func(g Gomega) {
				bundle, err := bundles.Get(ctx, name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				conditions, _, err := unstructured.NestedSlice(bundle.Object, "status", "conditions")
				g.Expect(err).NotTo(HaveOccurred())
				var synced map[string]interface{}
				for _, condition := range conditions {
					c := condition.(map[string]interface{})
					if c["type"] == "Synced" {
						synced = c
					}
				}
				g.Expect(synced).NotTo(BeNil())
				g.Expect(synced["status"]).To(Equal(status))
				g.Expect(synced["observedGeneration"]).To(Equal(bundle.GetGeneration()))
				if status == "False" {
					g.Expect(synced["reason"]).To(Equal("SourceNotFound"))
				}
			}, timeout, interval).Should(Succeed())
		}
		waitForSync("False")

		By("making the same public CA source available in the trust namespace")
		_, err = clientset.CoreV1().Secrets("cert-manager").Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string][]byte{"ca.crt": otherCA},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(clientset.CoreV1().Secrets("cert-manager").Delete(ctx, name, metav1.DeleteOptions{})).To(Succeed())
		})
		waitForSync("True")
		var distributed []byte
		want := caFingerprints(append(append([]byte{}, root.Data["ca.crt"]...), otherCA...))
		Eventually(func(g Gomega) {
			cm, err := clientset.CoreV1().ConfigMaps(tenant.Name).Get(ctx, name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			distributed = []byte(cm.Data["ca-bundle.crt"])
			g.Expect(caFingerprints(distributed)).To(Equal(want))
		}, timeout, interval).Should(Succeed())

		By("verifying HTTPS through Squid with only the distributed bundle as client trust")
		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			TLSOutgoingOptions: &testhelpers.TLSOutgoingOptionsValues{CAFile: "/etc/squid/trust/test-server/ca.crt"},
			ReplicaCount:       int(suiteReplicaCount),
		})
		DeferCleanup(func() {
			Expect(testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
				ReplicaCount: int(suiteReplicaCount),
			})).To(Succeed())
		})
		Expect(err).NotTo(HaveOccurred())
		client, err := testhelpers.NewTrustedSquidCachingClient(serviceName, namespace, distributed, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(client.CloseIdleConnections)
		url := "https://test-server." + namespace + ".svc.cluster.local:443"
		Eventually(func() error {
			resp, err := client.Get(url)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("unexpected HTTP status: %s", resp.Status)
			}
			if resp.TLS == nil || len(resp.TLS.VerifiedChains) == 0 {
				return fmt.Errorf("missing verified TLS chain")
			}
			_, err = io.Copy(io.Discard, resp.Body)
			return err
		}, timeout, interval).Should(Succeed())

		By("proving that trusting only the origin CA does not trust the intercepting proxy")
		untrusted, err := testhelpers.NewTrustedSquidCachingClient(serviceName, namespace, otherCA, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(untrusted.CloseIdleConnections)
		resp, err := untrusted.Get(url)
		if resp != nil {
			Expect(resp.Body.Close()).To(Succeed())
		}
		var unknownAuthority x509.UnknownAuthorityError
		Expect(errors.As(err, &unknownAuthority)).To(BeTrue(), "expected an unknown-authority TLS failure, got %v", err)
	})
})

// Compare certificate identities, not PEM formatting or bundle ordering.
func caFingerprints(data []byte) map[[32]byte]bool {
	certs := map[[32]byte]bool{}
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		Expect(block.Type).To(Equal("CERTIFICATE"))
		cert, err := x509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(cert.IsCA).To(BeTrue())
		certs[sha256.Sum256(cert.Raw)] = true
	}
	Expect(certs).NotTo(BeEmpty(), "bundle must contain valid CA certificates")
	return certs
}
