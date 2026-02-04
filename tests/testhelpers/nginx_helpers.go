package testhelpers

import (
	"fmt"
	"net/http"
)

// NginxValues holds Helm values for nginx configuration
type NginxValues struct {
	// Enabled must NOT have omitempty since we need to explicitly set false to disable
	Enabled      bool                   `json:"enabled"`
	ReplicaCount int                    `json:"replicaCount,omitempty"`
	Upstream     *NginxUpstreamValues   `json:"upstream,omitempty"`
	Auth         *NginxAuthValues       `json:"auth,omitempty"`
	Cache        *NginxCacheValues      `json:"cache,omitempty"`
	Service      *NginxServiceValues    `json:"service,omitempty"`
}

// NginxUpstreamValues holds upstream server configuration
type NginxUpstreamValues struct {
	URL string `json:"url,omitempty"`
}

// NginxAuthValues holds authorization header injection configuration
type NginxAuthValues struct {
	Enabled    bool   `json:"enabled"`
	SecretName string `json:"secretName,omitempty"`
}

// NginxCacheValues holds cache configuration
type NginxCacheValues struct {
	AllowList []string `json:"allowList,omitempty"`
	Size      int      `json:"size,omitempty"`
}

// NginxServiceValues holds service configuration
type NginxServiceValues struct {
	Type                string `json:"type,omitempty"`
	Port                int    `json:"port,omitempty"`
	TrafficDistribution string `json:"trafficDistribution,omitempty"`
}

// NewNginxClient creates an HTTP client for requests to nginx
func NewNginxClient() *http.Client {
	transport := &http.Transport{
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   Timeout,
	}
}

// GetNginxURL returns the URL for the nginx service
func GetNginxURL() string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", NginxServiceName, Namespace, NginxPort)
}
