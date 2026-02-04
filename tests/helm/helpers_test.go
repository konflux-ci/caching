package helm_test

import "strings"

// chartPath is the path to the Helm chart under test
const chartPath = "./squid"

// extractSection is a generic helper function that extracts a specific template section from helm output
// based on the Helm source comment marker (e.g., "# Source: squid/templates/deployment.yaml")
func extractSection(helmOutput, sourceMarker string) string {
	lines := strings.Split(helmOutput, "\n")
	var sectionLines []string
	inSection := false

	for _, line := range lines {
		// Start capturing when we find the source marker
		if strings.Contains(line, sourceMarker) {
			inSection = true
			continue
		}

		// Stop capturing when we hit the next resource
		if inSection && strings.HasPrefix(line, "---") {
			break
		}

		if inSection {
			sectionLines = append(sectionLines, line)
		}
	}

	return strings.Join(sectionLines, "\n")
}

// extractSquidDeploymentSection extracts just the squid statefulset YAML for precise testing
func extractSquidDeploymentSection(helmOutput string) string {
	return extractSection(helmOutput, "# Source: squid/templates/deployment.yaml")
}

// extractNginxStatefulSetSection extracts just the nginx statefulset YAML for precise testing
func extractNginxStatefulSetSection(helmOutput string) string {
	return extractSection(helmOutput, "# Source: squid/templates/nginx-statefulset.yaml")
}

// extractNginxConfigMapSection extracts just the nginx configmap YAML for precise testing
func extractNginxConfigMapSection(helmOutput string) string {
	return extractSection(helmOutput, "# Source: squid/templates/nginx-configmap.yaml")
}

// extractNginxServiceSection extracts just the nginx service YAML (non-headless) for precise testing
func extractNginxServiceSection(helmOutput string) string {
	return extractSection(helmOutput, "# Source: squid/templates/nginx-service.yaml")
}

// extractNginxHeadlessServiceSection extracts just the nginx headless service YAML for precise testing
func extractNginxHeadlessServiceSection(helmOutput string) string {
	return extractSection(helmOutput, "# Source: squid/templates/nginx-headless-service.yaml")
}
