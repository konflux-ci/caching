//go:build mage

package main

import (
	"fmt"
	"os"

	"github.com/konflux-ci/caching/internal"
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Kind manages kind cluster operations
type Kind mg.Namespace

// Build manages image building operations
type Build mg.Namespace

// SquidHelm manages squid helm chart operations
type SquidHelm mg.Namespace

// Test manages test execution operations
type Test mg.Namespace

const (
	clusterName = "caching"
	// SquidImageTag is the tag used for the squid container image
	squidImageTag = "localhost/konflux-ci/squid:latest"
	// SquidContainerfile is the path to the Containerfile for squid
	squidContainerfile = "Containerfile"
	// TestImageTag is the tag used for the test container image
	testImageTag = "localhost/konflux-ci/squid-test:latest"
	// TestContainerfile is the path to the Containerfile for tests
	testContainerfile = "test.Containerfile"
)

// Default target - shows available targets
func Default() error {
	return sh.Run("mage", "-l")
}

// Kind:Up creates or connects to a kind cluster named 'caching'
func (Kind) Up() error {
	fmt.Println("🚀 Setting up kind cluster...")

	// Check if cluster already exists
	exists, err := internal.ClusterExists(clusterName)
	if err != nil {
		return fmt.Errorf("failed to check cluster existence: %w", err)
	}

	if exists {
		fmt.Printf("✅ Cluster '%s' already exists\n", clusterName)
	} else {
		fmt.Printf("📦 Creating kind cluster '%s'...\n", clusterName)
		err := internal.CreateCluster(clusterName)
		if err != nil {
			return fmt.Errorf("failed to create cluster: %w", err)
		}
		fmt.Printf("✅ Cluster '%s' created successfully\n", clusterName)
	}

	// Export kubeconfig
	fmt.Printf("🔧 Exporting kubeconfig for cluster '%s'...\n", clusterName)
	err = internal.ExportKubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("failed to export kubeconfig: %w", err)
	}

	fmt.Printf("✅ Kind cluster '%s' is ready!\n", clusterName)
	return nil
}

// Kind:UpClean forces recreation of the kind cluster (deletes existing cluster and creates new one)
func (Kind) UpClean() error {
	fmt.Println("🚀 Setting up kind cluster (clean recreation)...")

	// Check if cluster already exists
	exists, err := internal.ClusterExists(clusterName)
	if err != nil {
		return fmt.Errorf("failed to check cluster existence: %w", err)
	}

	if exists {
		fmt.Printf("🔄 Deleting existing cluster '%s'...\n", clusterName)
		err := internal.DeleteCluster(clusterName)
		if err != nil {
			return fmt.Errorf("failed to delete existing cluster: %w", err)
		}
		fmt.Printf("✅ Cluster '%s' deleted successfully\n", clusterName)
	}

	// Create new cluster
	fmt.Printf("📦 Creating kind cluster '%s'...\n", clusterName)
	err = internal.CreateCluster(clusterName)
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}
	fmt.Printf("✅ Cluster '%s' created successfully\n", clusterName)

	// Export kubeconfig
	fmt.Printf("🔧 Exporting kubeconfig for cluster '%s'...\n", clusterName)
	err = internal.ExportKubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("failed to export kubeconfig: %w", err)
	}

	fmt.Printf("✅ Kind cluster '%s' is ready!\n", clusterName)
	return nil
}

// Kind:Down tears down the kind cluster
func (Kind) Down() error {
	fmt.Println("🔥 Tearing down kind cluster...")

	// Check if cluster exists first
	exists, err := internal.ClusterExists(clusterName)
	if err != nil {
		return fmt.Errorf("failed to check cluster existence: %w", err)
	}

	if !exists {
		fmt.Printf("ℹ️  Cluster '%s' does not exist\n", clusterName)
		return nil
	}

	// Delete the cluster
	fmt.Printf("🗑️  Deleting kind cluster '%s'...\n", clusterName)
	err = internal.DeleteCluster(clusterName)
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	fmt.Printf("✅ Cluster '%s' deleted successfully\n", clusterName)
	return nil
}

// Kind:Status shows the status of the kind cluster
func (Kind) Status() error {
	fmt.Println("📊 Checking kind cluster status...")

	// Check if cluster exists
	exists, err := internal.ClusterExists(clusterName)
	if err != nil {
		return fmt.Errorf("failed to check cluster existence: %w", err)
	}

	if !exists {
		fmt.Printf("❌ Cluster '%s' does not exist\n", clusterName)
		return nil
	}

	fmt.Printf("✅ Cluster '%s' exists\n", clusterName)

	// Check kubeconfig
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
	}

	// Try to get cluster info
	fmt.Printf("🔍 Checking cluster connectivity...\n")
	output, err := internal.GetClusterInfo(clusterName)
	if err != nil {
		fmt.Printf("⚠️  Could not connect to cluster: %v\n", err)
		fmt.Printf("💡 Try running 'mage kind:up' to ensure kubeconfig is exported\n")
		return nil
	}

	fmt.Printf("✅ Cluster is accessible:\n%s\n", output)

	// Get node status
	fmt.Printf("🖥️  Node status:\n")
	err = internal.GetNodeStatus(clusterName)
	if err != nil {
		fmt.Printf("⚠️  Could not get node status: %v\n", err)
	}

	return nil
}

// Build:Squid builds the Squid container image
func (Build) Squid() error {
	fmt.Println("🐳 Building Squid container image...")

	// Build the squid image using podman
	fmt.Printf("📦 Building image with tag '%s'...\n", squidImageTag)
	err := sh.Run("podman", "build", "-t", squidImageTag, "-f", squidContainerfile, ".")
	if err != nil {
		return fmt.Errorf("failed to build squid image: %w", err)
	}

	fmt.Printf("✅ Squid image built successfully\n")

	// Verify the image was built
	fmt.Printf("🔍 Verifying image exists...\n")
	err = sh.Run("podman", "images", squidImageTag)
	if err != nil {
		return fmt.Errorf("failed to verify squid image: %w", err)
	}

	fmt.Printf("✅ Squid image '%s' is ready!\n", squidImageTag)
	return nil
}

// Build:LoadSquid loads the Squid image into the kind cluster
func (Build) LoadSquid() error {
	// Ensure dependencies are met
	mg.Deps(Kind.Up, Build.Squid)

	fmt.Println("📦 Loading Squid image into kind cluster...")

	// Load image into kind cluster using process substitution
	fmt.Printf("📤 Loading image into kind cluster '%s'...\n", clusterName)
	err := sh.Run("bash", "-c", fmt.Sprintf("kind load image-archive --name %s <(podman save %s)", clusterName, squidImageTag))
	if err != nil {
		return fmt.Errorf("failed to load image into kind cluster: %w", err)
	}

	// Verify image is available in cluster
	fmt.Printf("🔍 Verifying image is available in cluster...\n")
	err = internal.GetNodeStatus(clusterName)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster for verification: %w", err)
	}

	fmt.Printf("✅ Squid image loaded successfully into kind cluster '%s'!\n", clusterName)
	return nil
}

// Build:TestImage builds the test container image
func (Build) TestImage() error {
	fmt.Println("🔨 Building test container image...")

	// Build the test image using podman
	fmt.Printf("📦 Building image with tag '%s'...\n", testImageTag)
	err := sh.Run("podman", "build", "-t", testImageTag, "-f", testContainerfile, ".")
	if err != nil {
		return fmt.Errorf("failed to build test image: %w", err)
	}

	fmt.Printf("✅ Test image built successfully\n")

	// Verify the image was built
	fmt.Printf("🔍 Verifying image exists...\n")
	err = sh.Run("podman", "images", testImageTag)
	if err != nil {
		return fmt.Errorf("failed to verify test image: %w", err)
	}

	fmt.Printf("✅ Test image '%s' is ready!\n", testImageTag)
	return nil
}

// Build:LoadTestImage loads the test image into the kind cluster
func (Build) LoadTestImage() error {
	// Ensure dependencies are met
	mg.Deps(Kind.Up, Build.TestImage)

	fmt.Println("📦 Loading test image into kind cluster...")

	// Load image into kind cluster using process substitution
	fmt.Printf("📤 Loading image into kind cluster '%s'...\n", clusterName)
	err := sh.Run("bash", "-c", fmt.Sprintf("kind load image-archive --name %s <(podman save %s)", clusterName, testImageTag))
	if err != nil {
		return fmt.Errorf("failed to load test image into kind cluster: %w", err)
	}

	fmt.Printf("✅ Test image loaded successfully into kind cluster '%s'!\n", clusterName)
	return nil
}

// SquidHelm:Up deploys the Squid Helm chart to the cluster
func (SquidHelm) Up() error {
	// Ensure dependencies are met (squid and test images needed)
	mg.Deps(Build.LoadSquid, Build.LoadTestImage)

	fmt.Println("⚓ Deploying Squid Helm chart...")

	// Ensure required helm repositories are available
	fmt.Printf("📦 Ensuring helm repositories are available...\n")
	err := internal.EnsureHelmRepo("jetstack", "https://charts.jetstack.io")
	if err != nil {
		return fmt.Errorf("failed to ensure jetstack repository: %w", err)
	}

	// Build helm dependencies from lock file
	fmt.Printf("📦 Building helm dependencies...\n")
	err = sh.Run("helm", "dependency", "build", "./squid")
	if err != nil {
		return fmt.Errorf("failed to build helm dependencies: %w", err)
	}

	// Check if release already exists
	exists, err := internal.ReleaseExists("squid")
	if err != nil {
		return fmt.Errorf("failed to check release existence: %w", err)
	}

	if exists {
		// Upgrade existing release
		fmt.Printf("⚓ Upgrading existing squid helm release and waiting for readiness...\n")
		err = sh.Run("helm", "upgrade", "squid", "./squid", "--set", "environment=dev", "--wait", "--timeout=120s")
		if err != nil {
			return fmt.Errorf("failed to upgrade helm chart: %w", err)
		}
	} else {
		// Install new release
		fmt.Printf("⚓ Installing squid helm chart and waiting for readiness...\n")
		err = sh.Run("helm", "install", "squid", "./squid", "--set", "environment=dev", "--wait", "--timeout=120s")
		if err != nil {
			return fmt.Errorf("failed to install helm chart: %w", err)
		}
	}

	// Show comprehensive deployment status
	fmt.Printf("🔍 Verifying deployment status...\n")
	err = (SquidHelm{}).Status()
	if err != nil {
		return fmt.Errorf("deployment verification failed: %w", err)
	}

	fmt.Printf("✅ Squid helm chart deployed successfully!\n")
	return nil
}

// SquidHelm:Down removes the Squid Helm chart from the cluster
func (SquidHelm) Down() error {
	fmt.Println("🗑️  Removing Squid Helm chart...")

	// Check if release exists first
	exists, err := internal.ReleaseExists("squid")
	if err != nil {
		return fmt.Errorf("failed to check release existence: %w", err)
	}

	if !exists {
		fmt.Printf("ℹ️  Helm release 'squid' does not exist\n")
		return nil
	}

	// Uninstall the helm release
	fmt.Printf("🗑️  Uninstalling squid helm release...\n")
	err = sh.Run("helm", "uninstall", "squid")
	if err != nil {
		return fmt.Errorf("failed to uninstall helm chart: %w", err)
	}

	// Wait for proxy namespace to be fully deleted
	err = internal.WaitForNamespaceDeleted("proxy")
	if err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
		// Don't fail the function, just warn - the namespace might be stuck
	}

	fmt.Printf("✅ Squid helm chart removed successfully!\n")
	return nil
}

// SquidHelm:UpClean forces redeployment of the Squid Helm chart (removes and reinstalls)
func (SquidHelm) UpClean() error {
	fmt.Println("🔄 Force redeploying Squid Helm chart...")

	// Remove existing release
	err := (SquidHelm{}).Down()
	if err != nil {
		return fmt.Errorf("failed to remove existing release: %w", err)
	}

	// Install fresh release
	fmt.Printf("⚓ Installing fresh squid helm chart...\n")
	return (SquidHelm{}).Up()
}

// SquidHelm:Status shows the deployment status
func (SquidHelm) Status() error {
	fmt.Println("📊 Checking deployment status...")

	// Check if squid helm release exists
	fmt.Printf("🔍 Checking helm release status...\n")
	err := sh.Run("helm", "status", "squid")
	if err != nil {
		fmt.Printf("❌ Helm release 'squid' not found or not deployed\n")
		return fmt.Errorf("helm release not found: %w", err)
	}

	// Show pod status
	fmt.Printf("🖥️  Pod status:\n")
	err = sh.RunV("kubectl", "get", "pods", "-n", "proxy", "-l", "app.kubernetes.io/name=squid")
	if err != nil {
		fmt.Printf("⚠️  Could not get pod status: %v\n", err)
	}

	// Show service status
	fmt.Printf("🌐 Service status:\n")
	err = sh.RunV("kubectl", "get", "svc", "-n", "proxy", "-l", "app.kubernetes.io/name=squid")
	if err != nil {
		fmt.Printf("⚠️  Could not get service status: %v\n", err)
	}

	// Show deployment status
	fmt.Printf("📦 Deployment status:\n")
	err = sh.RunV("kubectl", "get", "deployment", "-n", "proxy", "-l", "app.kubernetes.io/name=squid")
	if err != nil {
		fmt.Printf("⚠️  Could not get deployment status: %v\n", err)
	}

	fmt.Printf("✅ Deployment status check completed!\n")
	return nil
}

// All runs the complete automation workflow
func All() error {
	fmt.Println("🎯 Running complete automation workflow...")
	fmt.Println("This will set up the complete local dev/test environment")
	fmt.Println("(dependencies will be handled automatically)")
	fmt.Println()

	// SquidHelm.Up will automatically handle all dependencies:
	// SquidHelm.Up -> Build.LoadSquid + Build.LoadSquidExporter + Build.LoadTestImage -> Kind.Up + Build.Squid + Build.TestImage
	err := (SquidHelm{}).Up()
	if err != nil {
		return err
	}

	// Run helm tests to validate the deployment
	fmt.Println()
	fmt.Println("🧪 Running helm tests to validate deployment...")
	err = sh.Run("helm", "test", "squid")
	if err != nil {
		return fmt.Errorf("helm tests failed: %w", err)
	}
	fmt.Println("✅ All helm tests passed!")

	fmt.Println()
	fmt.Println("🎉 Complete automation workflow finished successfully!")
	fmt.Println("Your local dev/test environment is ready:")
	fmt.Println("  • Kind cluster: 'caching'")
	fmt.Println("  • Squid proxy: http://squid.proxy.svc.cluster.local:3128")
	fmt.Println("  • Helm tests: ✅ All passing")
	fmt.Println("  • Ready for development and testing!")
	return nil
}

// Clean removes all resources (cluster, images, etc.)
func Clean() error {
	fmt.Println("🧹 Cleaning up all resources...")
	fmt.Println("This will remove:")
	fmt.Println("  • Kind cluster (including all deployments)")
	fmt.Println("  • Built container images")
	fmt.Println()

	fmt.Printf("🗑️  Removing kind cluster...\n")
	err := (Kind{}).Down()
	if err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove kind cluster: %v\n", err)
	}

	fmt.Printf("🗑️  Removing container images...\n")
	err = sh.Run("podman", "rmi", squidImageTag)
	if err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove squid image: %v\n", err)
	}

	err = sh.Run("podman", "rmi", testImageTag)
	if err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove test image: %v\n", err)
	}

	fmt.Printf("✅ Resource cleanup completed!\n")
	return nil
}

// Test:Cluster runs tests with cluster network access via mirrord
func (Test) Cluster() error {
	// Ensure cluster and deployment are ready (includes mirrord infrastructure)
	mg.Deps(SquidHelm{}.Up)

	fmt.Println("🔮 Running tests with cluster network access...")
	fmt.Println("Tests run as if inside the cluster using mirrord")
	fmt.Println("This provides the most realistic testing environment")

	// Check if mirrord is available
	err := sh.Run("which", "mirrord")
	if err != nil {
		return fmt.Errorf("mirrord not found in PATH - ensure it's installed: %w", err)
	}

	// Verify mirrord target pod is ready (deployed by Helm chart)
	fmt.Println("⏳ Waiting for mirrord target pod to be ready...")
	err = sh.Run("kubectl", "wait", "--for=condition=Ready", "pod/mirrord-test-target", "-n", "proxy", "--timeout=60s")
	if err != nil {
		return fmt.Errorf("mirrord target pod not ready - check Helm deployment: %w", err)
	}

	// Build test binary with ginkgo for better output
	fmt.Println("🔨 Building test binary with ginkgo...")
	err = sh.RunWith(map[string]string{
		"CGO_ENABLED": "1",
	}, "ginkgo", "build", "./tests/e2e/")
	if err != nil {
		return fmt.Errorf("failed to build test binary with ginkgo: %w", err)
	}

	// Run tests with mirrord using ginkgo binary
	fmt.Println("🚀 Running tests with mirrord connection stealing...")
	return sh.RunWithV(map[string]string{
		"CGO_ENABLED": "1",
	}, "mirrord", "exec", "--config-file", ".mirrord/mirrord.json", "--",
		"./tests/e2e/e2e.test", "-ginkgo.v")
}
