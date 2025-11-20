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
	suiteReplicaCount int32 // Will be set from env var or default to 1
)

const (
	namespace          = testhelpers.Namespace
	deploymentName     = testhelpers.DeploymentName
	serviceName        = testhelpers.ServiceName
	timeout            = testhelpers.Timeout
	interval           = testhelpers.Interval
	squidContainerName = testhelpers.SquidContainerName
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

// SetSuiteReplicaCount sets the replica count for the test suite
// This should be called by Mage targets before running tests
func SetSuiteReplicaCount(count int32) {
	suiteReplicaCount = count
}

var _ = BeforeSuite(func() {
	ctx = context.Background()

	// Create Kubernetes client first (need it to read current replica count)
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

	// Read replica count from environment variable or from existing deployment
	if envReplicas := os.Getenv("SQUID_REPLICA_COUNT"); envReplicas != "" {
		if count, parseErr := strconv.ParseInt(envReplicas, 10, 32); parseErr == nil {
			suiteReplicaCount = int32(count)
			fmt.Printf("DEBUG: Using replica count from SQUID_REPLICA_COUNT env var: %d\n", suiteReplicaCount)
		} else {
			fmt.Printf("DEBUG: Failed to parse SQUID_REPLICA_COUNT: %v\n", parseErr)
		}
	} else {
		// No env var set, try to read from existing deployment
		fmt.Printf("DEBUG: SQUID_REPLICA_COUNT not set, reading from deployment...\n")
		deployment, err := clientset.AppsV1().Deployments(testhelpers.Namespace).Get(ctx, testhelpers.DeploymentName, metav1.GetOptions{})
		if err == nil && deployment != nil && deployment.Spec.Replicas != nil {
			suiteReplicaCount = *deployment.Spec.Replicas
			fmt.Printf("DEBUG: Using replica count from existing deployment: %d\n", suiteReplicaCount)
		} else {
			// No existing deployment, default to 1
			suiteReplicaCount = 1
			fmt.Printf("DEBUG: No existing deployment found, defaulting to: %d\n", suiteReplicaCount)
		}
	}

	// Check if we should skip helm reconfiguration (e.g., in EaaS where deployment is already correct)
	skipReconfigure := os.Getenv("SKIP_HELM_RECONFIGURE")
	if skipReconfigure != "true" {
		// Local/devcontainer testing: reconfigure to ensure correct state
		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			ReplicaCount: int(suiteReplicaCount),
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to configure squid")

		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{})
		Expect(err).NotTo(HaveOccurred(), "Failed to configure squid")
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
