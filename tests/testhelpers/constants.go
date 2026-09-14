package testhelpers

import (
	"os"
	"time"
)

// Test configuration constants shared across all test packages
const (
	// General constants
	Timeout  = 60 * time.Second
	Interval = 2 * time.Second

	// Squid constants
	SquidServiceName     = "squid"
	SquidStatefulSetName = "squid"
	SquidContainerName   = "squid"
	SquidComponentLabel  = "squid-caching"

	// Nginx constants
	NginxServiceName     = "nginx"
	NginxStatefulSetName = "nginx"
	NginxPort            = 80
	NginxHTTPSPort       = 443
	NginxComponentLabel  = "nginx-caching"

	// Nginx test backend constants
	NginxTestBackendServiceName = "nginx-test-backend"
	NginxTestBackendPort        = 9090
)

// Component namespaces follow the Helm test pod configuration, with local defaults.
var (
	SquidNamespace     = namespaceFromEnv("SQUID_NAMESPACE", "squid-proxy")
	NginxNamespace     = namespaceFromEnv("NGINX_NAMESPACE", "nginx-proxy")
	SquidTLSSecretName = SquidNamespace + "-tls"
)

func namespaceFromEnv(key, fallback string) string {
	if namespace := os.Getenv(key); namespace != "" {
		return namespace
	}
	return fallback
}
