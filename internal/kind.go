package internal

import (
	"fmt"
	"strings"

	"github.com/magefile/mage/sh"
)

// ClusterExists checks if the specified kind cluster exists
func ClusterExists(name string) (bool, error) {
	clusters, err := sh.Output("kind", "get", "clusters")
	if err != nil {
		return false, fmt.Errorf("failed to get clusters: %w", err)
	}

	for _, cluster := range strings.Split(clusters, "\n") {
		if strings.TrimSpace(cluster) == name {
			return true, nil
		}
	}

	return false, nil
}

// CreateCluster creates a new kind cluster with the given name
func CreateCluster(name string) error {
	return sh.Run("kind", "create", "cluster", "--name", name, "--wait", "60s")
}

// DeleteCluster deletes the kind cluster with the given name
func DeleteCluster(name string) error {
	return sh.Run("kind", "delete", "cluster", "--name", name)
}

// ExportKubeconfig exports the kubeconfig for the given cluster
func ExportKubeconfig(name string) error {
	return sh.Run("kind", "export", "kubeconfig", "--name", name)
}

// LoadImage loads a container image into the Kind cluster's control-plane node
func LoadImage(clusterName, imageTag string) error {
	nodeName := clusterName + "-control-plane" // single-node cluster
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	cmd := fmt.Sprintf(
		"set -o pipefail; podman save --format docker-archive %s | podman exec -i %s ctr --namespace=k8s.io images import --digests -",
		q(imageTag), q(nodeName),
	)
	if err := sh.Run("bash", "-c", cmd); err != nil {
		return fmt.Errorf("loading image %s into cluster %s: %w", imageTag, clusterName, err)
	}
	return nil
}

// GetClusterInfo gets cluster info for the given cluster
func GetClusterInfo(name string) (string, error) {
	return sh.Output("kubectl", "cluster-info", "--context", "kind-"+name)
}

// GetNodeStatus runs kubectl get nodes for the given cluster
func GetNodeStatus(name string) error {
	return sh.Run("kubectl", "get", "nodes", "--context", "kind-"+name)
}
