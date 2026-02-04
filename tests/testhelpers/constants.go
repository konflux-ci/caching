package testhelpers

import "time"

// Test configuration constants shared across all test packages
const (
	// General constants
	Namespace = "caching"
	Timeout   = 60 * time.Second
	Interval  = 2 * time.Second

	// Squid constants
	SquidServiceName     = "squid"
	SquidStatefulSetName = "squid"
	SquidContainerName   = "squid"
	SquidComponentLabel  = "squid-caching"

	// Nginx constants
	NginxServiceName     = "nginx"
	NginxStatefulSetName = "nginx"
	NginxPort            = 8080
	NginxComponentLabel  = "nginx-caching"

	// Nexus constants
	NexusServiceName = "nexus"
)
