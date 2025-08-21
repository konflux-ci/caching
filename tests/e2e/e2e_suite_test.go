package e2e_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	certmanagerclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var (
	clientset         *kubernetes.Clientset
	certManagerClient *certmanagerclient.Clientset
	ctx               context.Context

	// Suite-level variables for squid configuration management
	suiteSquidConfigMgr *testhelpers.SquidConfigManager
)

const (
	// Test configuration
	namespace      = "proxy"
	deploymentName = "squid"
	serviceName    = "squid"
	timeout        = 60 * time.Second
	interval       = 2 * time.Second
)

// getPodIP returns the pod IP address from downward API
func getPodIP() (string, error) {
	// Get pod IP from environment variable set by downward API
	// This works both in real pods and when mirrored via mirrord
	podIP := os.Getenv("POD_IP")
	fmt.Printf("DEBUG: Pod IP from downward API: %s\n", podIP)

	if podIP == "" {
		return "", fmt.Errorf("POD_IP environment variable not set (requires downward API)")
	}

	return podIP, nil
}

var _ = BeforeSuite(func() {
	ctx = context.Background()

	// Create Kubernetes client
	var config *rest.Config
	var err error

	// Try in-cluster config first (when running in a pod)
	config, err = rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig file (when running locally)
		var kubeconfig string
		if os.Getenv("KUBECONFIG") != "" {
			kubeconfig = os.Getenv("KUBECONFIG")
		} else if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		Expect(err).NotTo(HaveOccurred(), "Failed to create kubeconfig from %s", kubeconfig)
	}

	clientset, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client")

	// Create cert-manager client
	certManagerClient, err = certmanagerclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "Failed to create cert-manager client")

	// Verify we can connect to the cluster
	_, err = clientset.CoreV1().Pods("proxy").List(ctx, metav1.ListOptions{Limit: 1})
	Expect(err).NotTo(HaveOccurred(), "Failed to connect to Kubernetes cluster")

	By("Setting up suite-level squid configuration")

	// Create SquidConfigManager for suite-level configuration
	suiteSquidConfigMgr = testhelpers.NewSquidConfigManager(
		clientset, namespace, "squid-config", deploymentName)

	// Ensure required TLS configuration is present
	By("Ensuring required TLS configuration is present")
	err = suiteSquidConfigMgr.EnsureRequiredTLSConfig(ctx)
	Expect(err).NotTo(HaveOccurred(), "Failed to ensure required TLS config")

	// Restart deployment if configuration was modified
	if suiteSquidConfigMgr.WasConfigModified() {
		By("Restarting squid deployment for suite-level config changes")
		fmt.Printf("DEBUG: Configuration was modified, restarting deployment\n")

		err = suiteSquidConfigMgr.RestartSquidDeployment(ctx)
		Expect(err).NotTo(HaveOccurred(), "Failed to restart squid deployment")
	} else {
		By("TLS configuration already present, no restart needed")
		fmt.Printf("DEBUG: Configuration was not modified, no restart needed\n")
	}

	// Set up cleanup using DeferCleanup
	DeferCleanup(func() {
		By("Cleaning up suite-level squid configuration")

		if suiteSquidConfigMgr == nil {
			fmt.Printf("DEBUG: No configuration manager found, skipping cleanup\n")
			return
		}

		wasModified := suiteSquidConfigMgr.WasConfigModified()
		fmt.Printf("DEBUG: Configuration modified: %t\n", wasModified)

		if !wasModified {
			fmt.Printf("DEBUG: No configuration changes to restore\n")
			return
		}

		// Restore the original configuration
		By("Restoring original squid configuration")
		wasRestored, err := suiteSquidConfigMgr.RestoreOriginalConfig(ctx)
		Expect(err).NotTo(HaveOccurred(), "Failed to restore original squid config")

		if !wasRestored {
			fmt.Printf("DEBUG: Configuration restoration was not needed\n")
			return
		}

		// Restart deployment to apply restored configuration
		By("Restarting squid deployment to apply restored configuration")
		fmt.Printf("DEBUG: Configuration restored, restarting deployment\n")

		err = suiteSquidConfigMgr.RestartSquidDeployment(ctx)
		Expect(err).NotTo(HaveOccurred(), "Failed to restart squid deployment after restore")

		fmt.Printf("DEBUG: Suite cleanup completed successfully\n")
	})

	By("Suite setup complete - Configuration is ready")
	fmt.Printf("DEBUG: Suite-level configuration setup complete\n")
})

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Squid Helm Chart E2E Suite")
}
