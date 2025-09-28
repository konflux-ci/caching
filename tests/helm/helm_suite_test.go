package helm_test

import (
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = BeforeSuite(func() {
	By("Setting up Helm chart dependencies")

	// Find the project root directory using the shared helper
	projectRoot, err := testhelpers.FindChartDirectory()
	Expect(err).NotTo(HaveOccurred(), "Failed to find chart directory")

	// Add required Helm repositories
	cmd := exec.Command("helm", "repo", "add", "jetstack", "https://charts.jetstack.io")
	err = cmd.Run()
	if err != nil {
		// Repository might already exist, check if it's the right URL
		GinkgoWriter.Printf("Note: jetstack repo add failed (might already exist): %v\n", err)
	}

	// Update repositories to ensure we have latest charts
	cmd = exec.Command("helm", "repo", "update")
	err = cmd.Run()
	Expect(err).NotTo(HaveOccurred(), "Failed to update Helm repositories")

	// Build chart dependencies from the correct directory
	cmd = exec.Command("helm", "dependency", "build", "./squid")
	cmd.Dir = projectRoot
	err = cmd.Run()
	Expect(err).NotTo(HaveOccurred(), "Failed to build Helm chart dependencies")

	GinkgoWriter.Println("✅ Helm chart dependencies ready")
})

func TestHelm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Helm Template Unit Tests Suite")
}
