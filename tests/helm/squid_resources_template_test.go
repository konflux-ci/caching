package helm_test

import (
	"encoding/json"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template Squid Resources", func() {
	It("should set resource requests and limits by default", func() {
		output, err := renderSquidTemplate(testhelpers.HelmValues{})
		Expect(err).NotTo(HaveOccurred())

		statefulSet := decodeSquidStatefulSet(output)
		expectResources(containerByName(statefulSet, "squid").Resources, "100m", "256Mi", "500m", "1Gi")
		expectResources(containerByName(statefulSet, "icap-server").Resources, "10m", "32Mi", "100m", "128Mi")
	})

	It("should allow users to override resource requests and limits", func() {
		output, err := renderSquidTemplate(testhelpers.HelmValues{
			Resources: json.RawMessage(`{
				"requests": {"cpu": "1", "memory": "1Gi"},
				"limits": {"cpu": "2", "memory": "2Gi"}
			}`),
			IcapServer: &testhelpers.IcapServerValues{
				Resources: json.RawMessage(`{
					"requests": {"cpu": "200m", "memory": "64Mi"},
					"limits": {"cpu": "300m", "memory": "256Mi"}
				}`),
			},
		})
		Expect(err).NotTo(HaveOccurred())

		statefulSet := decodeSquidStatefulSet(output)
		expectResources(containerByName(statefulSet, "squid").Resources, "1", "1Gi", "2", "2Gi")
		expectResources(containerByName(statefulSet, "icap-server").Resources, "200m", "64Mi", "300m", "256Mi")
	})
})

func decodeSquidStatefulSet(helmOutput string) appsv1.StatefulSet {
	var statefulSet appsv1.StatefulSet
	err := yaml.Unmarshal([]byte(extractSquidDeploymentSection(helmOutput)), &statefulSet)
	Expect(err).NotTo(HaveOccurred())
	Expect(statefulSet.Name).To(Equal(testhelpers.SquidStatefulSetName))
	return statefulSet
}

func containerByName(statefulSet appsv1.StatefulSet, name string) corev1.Container {
	for _, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == name {
			return container
		}
	}
	Fail("Expected StatefulSet to contain container " + name)
	return corev1.Container{}
}

func expectResources(resources corev1.ResourceRequirements, requestCPU, requestMemory, limitCPU, limitMemory string) {
	Expect(resources.Requests.Cpu().Equal(resource.MustParse(requestCPU))).To(BeTrue())
	Expect(resources.Requests.Memory().Equal(resource.MustParse(requestMemory))).To(BeTrue())
	Expect(resources.Limits.Cpu().Equal(resource.MustParse(limitCPU))).To(BeTrue())
	Expect(resources.Limits.Memory().Equal(resource.MustParse(limitMemory))).To(BeTrue())
}
