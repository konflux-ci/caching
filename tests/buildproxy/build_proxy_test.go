package buildproxy_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// This suite deliberately does not share tests/e2e's Helm-upgrading BeforeSuite.
// It validates the deployed cluster-config and default build CA bundle as-is.
func TestBuildProxy(t *testing.T) {
	if os.Getenv("BUILD_PROXY_TEST_NAMESPACE") == "" {
		t.Skip("set BUILD_PROXY_TEST_NAMESPACE and BUILD_PROXY_TEST_SERVICE_ACCOUNT to run deployment E2E tests")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "KFLUXVNGD-1358 deployed build proxy regression")
}

const buildImage = "quay.io/konflux-ci/konflux-build-cli:latest@sha256:" +
	"0415613f955ef8f7c6fc821a6738dcf062d9a3582441042b87a2391c375d7f54"

var _ = Describe("Deployed build proxy", Serial, Label("external-deps", "build-proxy"), func() {
	var (
		client                                  *kubernetes.Clientset
		runs                                    dynamic.ResourceInterface
		tenant, serviceAccount, proxy, imageRef string
	)

	BeforeEach(func() {
		tenant = os.Getenv("BUILD_PROXY_TEST_NAMESPACE")
		serviceAccount = os.Getenv("BUILD_PROXY_TEST_SERVICE_ACCOUNT")
		Expect(serviceAccount).NotTo(BeEmpty(),
			"supply an existing build service account; the test does not grant SCC permissions")
		imageRef = os.Getenv("BUILD_PROXY_TEST_IMAGE")
		if imageRef == "" {
			// The image pull that failed in the September 16 production incident.
			imageRef = "registry.access.redhat.com/ubi10/openjdk-25-runtime:1784364635"
		}
		config, err := testhelpers.GetRESTConfig()
		Expect(err).NotTo(HaveOccurred())
		config.Timeout = 30 * time.Second
		client, err = kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		dc, err := dynamic.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		runs = dc.Resource(schema.GroupVersionResource{
			Group: "tekton.dev", Version: "v1", Resource: "pipelineruns",
		}).Namespace(tenant)
		cm, err := client.CoreV1().ConfigMaps("konflux-info").Get(context.Background(), "cluster-config", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cm.Data["allow-cache-proxy"]).To(Equal("true"), "cluster must enable the cache proxy for builds")
		proxy = strings.TrimSpace(cm.Data["http-proxy"])
		Expect(proxy).NotTo(BeEmpty(), "cluster-config must provide http-proxy; do not substitute a test endpoint")
		if !strings.Contains(proxy, "://") {
			proxy = "http://" + proxy
		}
		parsed, err := url.Parse(proxy)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed.Scheme).To(Equal("http"), "Squid uses an HTTP CONNECT proxy")
		Expect(parsed.Host).NotTo(BeEmpty())
		Expect(parsed.User).To(BeNil(), "proxy credentials must not appear in test logs")
		if expected := os.Getenv("BUILD_PROXY_TEST_EXPECTED_PROXY"); expected != "" {
			Expect(parsed.Host).To(Equal(expected),
				"cluster-config must select the proxy under promotion; testing another endpoint would give false confidence")
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Cluster=%s tenant=%s proxy=%s image=%s bundle=caching-ca-bundle\n",
			config.Host, tenant, proxy, imageRef)
	})

	DescribeTable("pulling the incident image with TLS verification",
		func(installBundle bool) {
			zero := int64(0)
			podSpec := &corev1.PodSpec{
				Volumes: []corev1.Volume{
					{Name: "build-storage", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					{Name: "build-context", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				},
				Containers: []corev1.Container{{
					Name: "build", Image: buildImage,
					VolumeMounts: []corev1.VolumeMount{
						{Name: "build-storage", MountPath: "/var/lib/containers"},
						{Name: "build-context", MountPath: "/tmp/proxy-build"},
					},
					Command: []string{"/bin/bash", "-ceu", buildScript},
					Env: []corev1.EnvVar{
						{Name: "HTTP_PROXY", Value: proxy}, {Name: "HTTPS_PROXY", Value: proxy},
						{Name: "http_proxy", Value: proxy}, {Name: "https_proxy", Value: proxy},
						// Force the pull through the proxy, even if the cluster has a bypass for this registry.
						{Name: "NO_PROXY", Value: ""}, {Name: "no_proxy", Value: ""},
						{Name: "IMAGE_REF", Value: imageRef},
						{Name: "INSTALL_PROXY_CA", Value: fmt.Sprint(installBundle)},
					},
					SecurityContext: &corev1.SecurityContext{
						RunAsUser:    &zero,
						Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SETFCAP"}},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				}},
			}
			if installBundle {
				// The positive test must use the same ConfigMap name/key as existing build tasks.
				// Never fetch the new proxy's Secret or choose a matching bundle for the endpoint.
				podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
					Name: "proxy-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "caching-ca-bundle"},
						Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "ca-bundle.crt"}},
					}},
				})
				podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
					Name: "proxy-ca", MountPath: "/mnt/proxy-ca-bundle", ReadOnly: true,
				})
			}

			// Use Tekton's normal tenant execution path; users need no direct Pod-create permission.
			spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(podSpec)
			Expect(err).NotTo(HaveOccurred())
			step := spec["containers"].([]interface{})[0].(map[string]interface{})
			step["computeResources"] = step["resources"]
			delete(step, "resources")
			taskSpec := map[string]interface{}{"steps": []interface{}{step}}
			if volumes, ok := spec["volumes"]; ok {
				taskSpec["volumes"] = volumes
			}
			run, err := runs.Create(context.Background(), &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "tekton.dev/v1", "kind": "PipelineRun",
				"metadata": map[string]interface{}{
					"generateName": "build-proxy-1358-",
					"labels":       map[string]interface{}{"app.kubernetes.io/name": "build-proxy-e2e"},
				},
				"spec": map[string]interface{}{
					"timeouts":        map[string]interface{}{"pipeline": "15m0s"},
					"taskRunTemplate": map[string]interface{}{"serviceAccountName": serviceAccount},
					"pipelineSpec": map[string]interface{}{
						"tasks": []interface{}{map[string]interface{}{"name": "verify", "taskSpec": taskSpec}},
					},
				},
			}}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				propagation := metav1.DeletePropagationForeground
				Expect(runs.Delete(context.Background(), run.GetName(), metav1.DeleteOptions{
					PropagationPolicy: &propagation,
				})).To(Succeed())
			})
			logsForRun := func() (string, error) {
				pods, err := client.CoreV1().Pods(tenant).List(context.Background(), metav1.ListOptions{
					LabelSelector: "tekton.dev/pipelineRun=" + run.GetName(),
				})
				if err != nil {
					return "", err
				}
				if len(pods.Items) != 1 {
					return "", fmt.Errorf("expected one build pod, got %d", len(pods.Items))
				}
				logs, err := client.CoreV1().Pods(tenant).GetLogs(
					pods.Items[0].Name, &corev1.PodLogOptions{Container: "step-build"}).DoRaw(context.Background())
				return string(logs), err
			}
			DeferCleanup(func() {
				logs, err := logsForRun()
				_, _ = fmt.Fprintf(GinkgoWriter, "PipelineRun %s/%s logs (error=%v):\n%s\n",
					tenant, run.GetName(), err, logs)
			})
			var result, message string
			Eventually(func(g Gomega) {
				current, err := runs.Get(context.Background(), run.GetName(), metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				conditions, _, err := unstructured.NestedSlice(current.Object, "status", "conditions")
				g.Expect(err).NotTo(HaveOccurred())
				result = ""
				for _, value := range conditions {
					condition := value.(map[string]interface{})
					if condition["type"] == "Succeeded" {
						result, _ = condition["status"].(string)
						message, _ = condition["message"].(string)
					}
				}
				g.Expect(result).To(Or(Equal("True"), Equal("False")), "waiting for PipelineRun: %s", message)
			}, 16*time.Minute, 5*time.Second).Should(Succeed())
			logs, err := logsForRun()
			Expect(err).NotTo(HaveOccurred(), "PipelineRun status: %s", message)
			if installBundle {
				Expect(result).To(Equal("True"), "build using default CA bundle failed: %s\n%s", message, logs)
				Expect(logs).To(ContainSubstring("IMAGE_PULL_AND_BUILD_SUCCESS"))
			} else {
				Expect(result).To(Equal("False"), "pull without proxy trust unexpectedly succeeded")
				Expect(logs).To(ContainSubstring("x509: certificate signed by unknown authority"),
					"negative control must fail for CA trust, not image availability or pod permissions")
			}
		},
		Entry("without the proxy CA fails with unknown authority", false),
		Entry("with the default build bundle pulls all layers and builds successfully", true),
	)
})

const buildScript = `
set -o pipefail
if [ "$INSTALL_PROXY_CA" = true ]; then
  test -s /mnt/proxy-ca-bundle/ca-bundle.crt
  cp /mnt/proxy-ca-bundle/ca-bundle.crt /etc/pki/ca-trust/source/anchors/proxy-ca.crt
  update-ca-trust
fi
# Each test uses a fresh pod and storage, so a warm local image cannot hide a failed pull.
buildah --storage-driver=vfs pull --tls-verify=true "docker://$IMAGE_REF"
mkdir -p /tmp/proxy-build
printf 'FROM %s\nLABEL konflux.proxy-regression=KFLUXVNGD-1358\n' "$IMAGE_REF" > /tmp/proxy-build/Containerfile
buildah --storage-driver=vfs bud --isolation=chroot --pull=never --tls-verify=true \
  -t localhost/proxy-regression:1358 /tmp/proxy-build
echo IMAGE_PULL_AND_BUILD_SUCCESS
`
