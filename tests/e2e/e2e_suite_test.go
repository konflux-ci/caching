package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

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
)

const (
	namespace      = testhelpers.Namespace
	deploymentName = testhelpers.DeploymentName
	serviceName    = testhelpers.ServiceName
	timeout        = testhelpers.Timeout
	interval       = testhelpers.Interval
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

// setupHTTPTestServerAndClient sets up an HTTP test server
// Registers a cleanup function to automatically close the test server
func setupHTTPTestServer(msg string) *testhelpers.CachingTestServer {
	// Get pod IP for test server
	podIP, err := getPodIP()
	Expect(err).NotTo(HaveOccurred(), "Failed to get pod IP")

	// Get test server port
	testPort := 0
	if testPortStr := os.Getenv("TEST_SERVER_PORT"); testPortStr != "" {
		if port, parseErr := strconv.Atoi(testPortStr); parseErr == nil {
			testPort = port
		}
	}

	// Create test server
	server, err := testhelpers.NewCachingTestServer(msg, podIP, testPort)
	Expect(err).NotTo(HaveOccurred(), "Failed to create test server")
	Expect(server).NotTo(BeNil())

	DeferCleanup(func() {
		fmt.Printf("DEBUG: Closing test server\n")
		server.Close()
	})

	return server
}

// setupHTTPTestClient sets up an HTTP test client
// Registers a cleanup function to automatically close idle connections
func setupHTTPTestClient() *http.Client {
	client, err := testhelpers.NewSquidCachingClient(serviceName, namespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to create caching client")

	DeferCleanup(func() {
		fmt.Printf("DEBUG: Closing idle test client connections\n")
		client.CloseIdleConnections()
	})

	return client
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

	// Build helm chart dependencies - needed for tests that reconfigure squid
	// The test image doesn't include dependencies (hermetic build)
	// All containerized tests (Konflux, EaaS) have read-only filesystem → use /tmp/
	// Only local dev (mage test:cluster) has writable filesystem, but dependencies already built by mage
	fmt.Println("Building helm dependencies in temp directory (read-only filesystem)...")
	err = testhelpers.BuildHelmDependencies()
	Expect(err).NotTo(HaveOccurred(), "Failed to build helm dependencies")
	
	// Check if squid is already deployed
	err = testhelpers.WaitForSquidDeploymentReady(ctx, clientset)
	if err != nil {
		// Squid NOT deployed - install it (EaaS scenario)
		fmt.Println("Squid not found - installing with default configuration")
		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{})
		Expect(err).NotTo(HaveOccurred(), "Failed to install squid")
	} else {
		fmt.Println("Squid already deployed - using existing deployment")
	}

	// Verify we can connect to the cluster
	_, err = clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{Limit: 1})
	Expect(err).NotTo(HaveOccurred(), "Failed to connect to Kubernetes cluster")

	By("Suite setup complete - Configuration is ready")
	fmt.Printf("DEBUG: Suite-level configuration setup complete\n")
})

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Squid Helm Chart E2E Suite")
}
