package main

import "C"

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

func main() {
	// Parse command line flags
	var message = flag.String("message", "Hello from Go server with cgo", "Message to include in responses")
	flag.Parse()

	// Determine port: check TEST_SERVER_PORT env var, default to 9090
	var port int
	if envPort := os.Getenv("TEST_SERVER_PORT"); envPort != "" {
		var err error
		port, err = strconv.Atoi(envPort)
		if err != nil {
			fmt.Printf("❌ Invalid TEST_SERVER_PORT value '%s': %v\n", envPort, err)
			os.Exit(1)
		}
		fmt.Printf("📍 Using port from TEST_SERVER_PORT environment variable: %d\n", port)
	} else {
		port = 9090
		fmt.Printf("📍 Using default port: %d\n", port)
	}

	// Get pod IP for logging - fail if not available
	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		fmt.Printf("❌ POD_IP environment variable is required but not set\n")
		os.Exit(1)
	}

	fmt.Printf("Server Pod IP: %s\n", podIP)
	fmt.Printf("🚀 Starting Go server (with cgo) on port %d...\n", port)
	fmt.Printf("📝 Message: %s\n", *message)

	// Create CachingTestServer with the specified port
	cachingServer, err := testhelpers.NewCachingTestServer(*message, podIP, port)
	if err != nil {
		fmt.Printf("❌ Failed to create caching test server: %v\n", err)
		os.Exit(1)
	}
	defer cachingServer.Close()

	fmt.Printf("✅ Server listening on %s\n", cachingServer.URL)

	// Keep the server running
	select {}
}
