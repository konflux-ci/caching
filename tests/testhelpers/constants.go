package testhelpers

import "time"

// Test configuration constants shared across all test packages
const (
	Namespace          = "caching"
	DeploymentName     = "squid"
	ServiceName        = "squid"
	Timeout            = 60 * time.Second
	Interval           = 2 * time.Second
	SquidContainerName = "squid"
	NexusServiceName   = "nexus"
)
