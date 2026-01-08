package testhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	nxrm "github.com/sonatype-nexus-community/nexus-repo-api-client-go/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// Environment variable names for overriding credentials
	EnvNexusAdminPassword = "NEXUS_ADMIN_PASSWORD"
	EnvNexusProxyPassword = "NEXUS_PROXY_PASSWORD"
	// DefaultNexusTimeout is the default timeout for waiting for Nexus to be ready
	DefaultNexusTimeout = 5 * time.Minute
)

// NexusConfig holds configuration for connecting to Nexus
type NexusConfig struct {
	URL           string
	AdminUser     string
	AdminPassword string
	ProxyUser     string
	ProxyPassword string
}

// configStep represents a named configuration step
type configStep struct {
	name string
	fn   func() error
}

// getEnvOrDefault returns the environment variable value if set, otherwise returns the default
func getEnvOrDefault(envVar, defaultVal string) string {
	if val := os.Getenv(envVar); val != "" {
		return val
	}
	return defaultVal
}

// NewNexusConfig returns a Nexus configuration for testing.
func NewNexusConfig() NexusConfig {
	return NexusConfig{
		URL:           fmt.Sprintf("http://%s.%s.svc.cluster.local:8081", NexusServiceName, Namespace),
		AdminUser:     "admin",
		AdminPassword: getEnvOrDefault(EnvNexusAdminPassword, "admin123"),
		ProxyUser:     "proxy-sa",
		ProxyPassword: getEnvOrDefault(EnvNexusProxyPassword, "proxy123"),
	}
}

// newNexusAPIClient creates a configured Nexus API client with authentication
func newNexusAPIClient(baseURL, username, password string) (*nxrm.APIClient, context.Context) {
	apiCfg := nxrm.NewConfiguration()
	apiCfg.Servers = nxrm.ServerConfigurations{
		{URL: baseURL + "/service/rest"},
	}
	apiCfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}

	client := nxrm.NewAPIClient(apiCfg)

	// Create context with basic auth
	ctx := context.WithValue(context.Background(), nxrm.ContextBasicAuth, nxrm.BasicAuth{
		UserName: username,
		Password: password,
	})

	return client, ctx
}

// ConfigureNexus performs the full Nexus configuration.
func ConfigureNexus(ctx context.Context, k8sClient kubernetes.Interface, restConfig *rest.Config, cfg NexusConfig) error {
	client, authCtx := newNexusAPIClient(cfg.URL, cfg.AdminUser, cfg.AdminPassword)

	steps := []configStep{
		{"Waiting for Nexus to be ready", func() error { return waitForNexus(ctx, client, DefaultNexusTimeout) }},
		{"Configuring admin password", func() error { return configureAdminPassword(ctx, k8sClient, restConfig, cfg) }},
		{"Accepting EULA", func() error { return acceptEULA(authCtx, client) }},
		{"Disabling anonymous access", func() error { return disableAnonymousAccess(authCtx, client) }},
		{"Creating user " + cfg.ProxyUser, func() error { return createProxyUser(authCtx, client, cfg) }},
		{"Creating go-proxy repository", func() error { return createGoProxyRepository(authCtx, client) }},
	}

	for _, step := range steps {
		fmt.Printf("%s...\n", step.name)
		if err := step.fn(); err != nil {
			return fmt.Errorf("%s: %w", strings.ToLower(step.name), err)
		}
	}

	fmt.Println("Nexus configuration complete!")
	return nil
}

func waitForNexus(ctx context.Context, client *nxrm.APIClient, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Nexus")
		case <-ticker.C:
			_, err := client.StatusAPI.IsAvailable(ctx).Execute()
			if err == nil {
				return nil
			}
			fmt.Println("Nexus not ready yet, retrying...")
		}
	}
}

func configureAdminPassword(ctx context.Context, k8sClient kubernetes.Interface, restConfig *rest.Config, cfg NexusConfig) error {
	pods, err := k8sClient.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=nexus",
	})
	if err != nil {
		return fmt.Errorf("listing nexus pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no nexus pods found")
	}

	stdout, stderr, err := ExecCommandInPod(ctx, k8sClient, restConfig, Namespace, pods.Items[0].Name, "nexus",
		[]string{"cat", "/nexus-data/admin.password"})
	if err != nil {
		fmt.Printf("Initial password file not found (may already be configured): %v, stderr: %s\n", err, stderr)
		return nil
	}

	initialPassword := strings.TrimSpace(stdout)
	if initialPassword == "" {
		fmt.Println("Initial password file is empty, skipping password change")
		return nil
	}

	fmt.Println("Found initial admin password, changing to configured password...")

	// Create a client with the initial password to change to the configured password
	client, authCtx := newNexusAPIClient(cfg.URL, cfg.AdminUser, initialPassword)
	_, err = client.SecurityManagementUsersAPI.ChangePassword(authCtx, cfg.AdminUser).Body(cfg.AdminPassword).Execute()
	if err != nil {
		return fmt.Errorf("changing password: %w", err)
	}

	fmt.Println("Admin password changed successfully")
	return nil
}

func acceptEULA(ctx context.Context, client *nxrm.APIClient) error {
	// First GET the current EULA status to retrieve the disclaimer
	resp, err := client.CommunityEditionEulaAPI.GetCommunityEulaStatus(ctx).Execute()
	if err != nil {
		return fmt.Errorf("getting EULA status: %w", err)
	}
	defer resp.Body.Close()

	var currentStatus nxrm.EulaStatus
	if err := json.NewDecoder(resp.Body).Decode(&currentStatus); err != nil {
		return fmt.Errorf("decoding EULA status: %w", err)
	}

	if currentStatus.Accepted != nil && *currentStatus.Accepted == true {
		fmt.Println("EULA already accepted, skipping acceptance")
		return nil
	}

	// POST back with the same disclaimer and accepted=true
	eulaStatus := nxrm.EulaStatus{
		Accepted:   nxrm.PtrBool(true),
		Disclaimer: currentStatus.Disclaimer,
	}

	_, err = client.CommunityEditionEulaAPI.SetEulaAcceptedCE(ctx).Body(eulaStatus).Execute()
	if err != nil {
		return fmt.Errorf("accepting EULA: %w", err)
	}
	return nil
}

func disableAnonymousAccess(ctx context.Context, client *nxrm.APIClient) error {
	settings := nxrm.AnonymousAccessSettingsXO{
		Enabled:   nxrm.PtrBool(false),
		UserId:    nxrm.PtrString("anonymous"),
		RealmName: nxrm.PtrString("NexusAuthorizingRealm"),
	}

	_, _, err := client.SecurityManagementAnonymousAccessAPI.Update1(ctx).Body(settings).Execute()
	if err != nil {
		return fmt.Errorf("disabling anonymous access: %w", err)
	}
	return nil
}

func createProxyUser(ctx context.Context, client *nxrm.APIClient, cfg NexusConfig) error {
	// Check if user already exists
	users, _, err := client.SecurityManagementUsersAPI.GetUsers(ctx).UserId(cfg.ProxyUser).Execute()
	if err == nil && len(users) > 0 {
		fmt.Printf("User %s already exists, skipping creation\n", cfg.ProxyUser)
		return nil
	}

	user := nxrm.ApiCreateUser{
		UserId:       nxrm.PtrString(cfg.ProxyUser),
		FirstName:    nxrm.PtrString("Proxy"),
		LastName:     nxrm.PtrString("Service Account"),
		EmailAddress: nxrm.PtrString(cfg.ProxyUser + "@localhost"),
		Password:     nxrm.PtrString(cfg.ProxyPassword),
		Status:       "active",
		Roles:        []string{"nx-anonymous"},
	}

	_, _, err = client.SecurityManagementUsersAPI.CreateUser(ctx).Body(user).Execute()
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}
	return nil
}

func createGoProxyRepository(ctx context.Context, client *nxrm.APIClient) error {
	const repoName = "go-proxy"

	// Check if repository already exists
	_, _, err := client.RepositoryManagementAPI.GetRepository(ctx, repoName).Execute()
	if err == nil {
		fmt.Printf("Repository %s already exists, skipping creation\n", repoName)
		return nil
	}

	repo := nxrm.GolangProxyRepositoryApiRequest{
		Name:   repoName,
		Online: true,
		Storage: nxrm.StorageAttributes{
			BlobStoreName:               "default",
			StrictContentTypeValidation: true,
		},
		Proxy: nxrm.ProxyAttributes{
			RemoteUrl:      nxrm.PtrString("https://proxy.golang.org"),
			ContentMaxAge:  1440,
			MetadataMaxAge: 1440,
		},
		NegativeCache: nxrm.NegativeCacheAttributes{
			Enabled:    true,
			TimeToLive: 1440,
		},
		HttpClient: nxrm.HttpClientAttributes{
			Blocked:   false,
			AutoBlock: true,
		},
	}

	_, err = client.RepositoryManagementAPI.CreateGoProxyRepository(ctx).Body(repo).Execute()
	if err != nil {
		return fmt.Errorf("creating repository: %w", err)
	}
	return nil
}
