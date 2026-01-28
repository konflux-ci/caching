package helm_test

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template Affinity Configuration", func() {
	const chartPath = "./squid"

	Describe("Default Configuration", func() {
		It("should include pod anti-affinity rules by default", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				ReplicaCount: 2,
			})
			Expect(err).NotTo(HaveOccurred(), "Helm template rendering should succeed")

			// Check that the squid statefulset is present
			Expect(output).To(ContainSubstring("name: "+testhelpers.SquidStatefulSetName), "Should contain squid statefulset")
			Expect(output).To(ContainSubstring("namespace: "+testhelpers.Namespace), "Should be in caching namespace")

			// Check for anti-affinity configuration
			Expect(output).To(ContainSubstring("podAntiAffinity"), "Should contain podAntiAffinity")
			Expect(output).To(ContainSubstring("preferredDuringSchedulingIgnoredDuringExecution"), "Should use preferred anti-affinity")
			Expect(output).To(ContainSubstring("kubernetes.io/hostname"), "Should use hostname topology key")
			Expect(output).To(ContainSubstring("weight: 100"), "Should have weight 100")

			// Verify label selector
			Expect(output).To(ContainSubstring("app.kubernetes.io/name: "+testhelpers.SquidStatefulSetName), "Should target squid pods")
			Expect(output).To(ContainSubstring("app.kubernetes.io/component: "+testhelpers.SquidComponentLabel), "Should target squid component")
		})

		It("should not include required anti-affinity (only preferred)", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{})
			Expect(err).NotTo(HaveOccurred())

			// Should have preferred but not required anti-affinity
			Expect(output).To(ContainSubstring("preferredDuringSchedulingIgnoredDuringExecution"), "Should have preferred anti-affinity")
			Expect(output).NotTo(ContainSubstring("requiredDuringSchedulingIgnoredDuringExecution"), "Should not have required anti-affinity")
		})
	})

	Describe("Disabled Affinity", func() {
		It("should not include any affinity when user sets empty affinity", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Affinity: json.RawMessage("{}"),
			})
			Expect(err).NotTo(HaveOccurred(), "Helm template rendering should succeed")

			// Should contain the statefulset but no affinity section
			Expect(output).To(ContainSubstring("name: "+testhelpers.SquidStatefulSetName), "Should contain squid statefulset")

			// Extract just the squid statefulset section for more precise checking
			squidDeploymentSection := extractSquidDeploymentSection(output)
			Expect(squidDeploymentSection).NotTo(ContainSubstring("affinity:"), "Squid statefulset should not contain affinity section")
			Expect(squidDeploymentSection).NotTo(ContainSubstring("podAntiAffinity"), "Squid statefulset should not contain podAntiAffinity")
		})
	})

	Describe("Custom Affinity", func() {
		It("should use custom node affinity instead of template defaults", func() {
			customAffinity := `{
				"nodeAffinity": {
					"requiredDuringSchedulingIgnoredDuringExecution": {
						"nodeSelectorTerms": [{
							"matchExpressions": [{
								"key": "node-type",
								"operator": "In",
								"values": ["proxy-nodes"]
							}]
						}]
					}
				}
			}`

			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Affinity: json.RawMessage(customAffinity),
			})
			Expect(err).NotTo(HaveOccurred(), "Helm template rendering should succeed")

			// Should have user's node affinity
			Expect(output).To(ContainSubstring("nodeAffinity"), "Should contain nodeAffinity from user")
			Expect(output).To(ContainSubstring("node-type"), "Should contain user's node selector")
			Expect(output).To(ContainSubstring("proxy-nodes"), "Should contain user's node values")

			// Should NOT have template's pod anti-affinity
			squidDeploymentSection := extractSquidDeploymentSection(output)
			Expect(squidDeploymentSection).NotTo(ContainSubstring("podAntiAffinity"), "Should not have template's podAntiAffinity when user provides custom affinity")
		})

		It("should support custom pod anti-affinity with different settings", func() {
			customAffinity := `{
				"podAntiAffinity": {
					"requiredDuringSchedulingIgnoredDuringExecution": [{
						"labelSelector": {
							"matchLabels": {
								"app": "custom-squid"
							}
						},
						"topologyKey": "topology.kubernetes.io/zone"
					}]
				}
			}`

			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Affinity: json.RawMessage(customAffinity),
			})
			Expect(err).NotTo(HaveOccurred())

			// Should have user's custom pod anti-affinity
			Expect(output).To(ContainSubstring("podAntiAffinity"), "Should have podAntiAffinity")
			Expect(output).To(ContainSubstring("requiredDuringSchedulingIgnoredDuringExecution"), "Should have required anti-affinity from user")
			Expect(output).To(ContainSubstring("topology.kubernetes.io/zone"), "Should use user's topology key")
			Expect(output).To(ContainSubstring("app: custom-squid"), "Should use user's label selector")

			// Should NOT have template's preferred anti-affinity
			squidDeploymentSection := extractSquidDeploymentSection(output)
			Expect(squidDeploymentSection).NotTo(ContainSubstring("preferredDuringSchedulingIgnoredDuringExecution"), "Should not have template's preferred rules when user provides custom")
		})
	})

	It("should include custom volumes and volumeMounts in squid container", func() {
		output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
			Volumes: []corev1.Volume{
				{
					Name: "custom-config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "my-configmap",
							},
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "custom-config",
					MountPath: "/etc/custom-config",
					ReadOnly:  true,
				},
			},
		})
		Expect(err).NotTo(HaveOccurred(), "Helm template rendering should succeed")

		squidDeploymentSection := extractSquidDeploymentSection(output)

		// Verify custom volume is present
		Expect(squidDeploymentSection).To(ContainSubstring("name: custom-config"), "Should contain custom volume name")
		Expect(squidDeploymentSection).To(ContainSubstring("name: my-configmap"), "Should contain configmap reference")

		// Verify custom volumeMount is present
		Expect(squidDeploymentSection).To(ContainSubstring("mountPath: /etc/custom-config"), "Should contain custom mount path")
	})

	Describe("Template Validation", func() {
		It("should produce valid YAML for all configuration scenarios", func() {
			testCases := []struct {
				name   string
				values testhelpers.SquidHelmValues
			}{
				{
					name: "default configuration",
					values: testhelpers.SquidHelmValues{
						ReplicaCount: 3,
					},
				},
				{
					name: "disabled affinity",
					values: testhelpers.SquidHelmValues{
						Affinity: json.RawMessage("{}"),
					},
				},
				{
					name: "custom node affinity",
					values: testhelpers.SquidHelmValues{
						Affinity: json.RawMessage(`{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"node-type","operator":"In","values":["proxy"]}]}]}}}`),
					},
				},
				{
					name: "custom volumes and volumeMounts",
					values: testhelpers.SquidHelmValues{
						Volumes: []corev1.Volume{
							{
								Name: "custom-secret",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "my-secret",
									},
								},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "custom-secret",
								MountPath: "/etc/custom-secret",
								ReadOnly:  true,
							},
						},
					},
				},
			}

			for _, tc := range testCases {
				By(tc.name)
				output, err := testhelpers.RenderHelmTemplate(chartPath, tc.values)
				Expect(err).NotTo(HaveOccurred(), "Template rendering should succeed for %s", tc.name)
				Expect(output).NotTo(BeEmpty(), "Should produce non-empty YAML output")

				// Verify basic Kubernetes resource structure
				Expect(output).To(ContainSubstring("apiVersion:"), "Should contain apiVersion")
				Expect(output).To(ContainSubstring("kind:"), "Should contain kind")
				Expect(output).To(ContainSubstring("name: "+testhelpers.SquidStatefulSetName), "Should contain squid statefulset")
			}
		})
	})
})

// extractSquidDeploymentSection extracts just the squid statefulset YAML for more precise testing
func extractSquidDeploymentSection(helmOutput string) string {
	lines := strings.Split(helmOutput, "\n")
	var squidDeploymentLines []string
	inSquidDeployment := false

	for _, line := range lines {
		// Start capturing when we find the squid statefulset
		if strings.Contains(line, "# Source: squid/templates/deployment.yaml") {
			inSquidDeployment = true
			continue
		}

		// Stop capturing when we hit the next resource
		if inSquidDeployment && strings.HasPrefix(line, "---") {
			break
		}

		if inSquidDeployment {
			squidDeploymentLines = append(squidDeploymentLines, line)
		}
	}

	return strings.Join(squidDeploymentLines, "\n")
}
