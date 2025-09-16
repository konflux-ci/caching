package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var cdnRegex = regexp.MustCompile(`^https://cdn(\d{2})?\.quay\.io/.+/sha256/.+/[a-f0-9]{64}`)

// HTTPClient interface for making HTTP requests (allows mocking)
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

// isChannelID checks if a string represents a positive integer (for channel-ID detection)
func isChannelID(s string) bool {
	val, err := strconv.ParseInt(s, 10, 64)
	return err == nil && val >= 0
}

// normalizeStoreID normalizes the store-id for caching by removing query parameters from CDN URLs.
// The request URL must return a 200 status code to ensure the request is authorized.
func normalizeStoreID(client HTTPClient, requestURL string) string {
	if !cdnRegex.MatchString(requestURL) {
		return requestURL
	}

	// Issue the request to the CDN to check authorization but don't read the body
	resp, err := client.Get(requestURL)
	if err != nil {
		// Don't log the request URL to avoid leaking sensitive information
		log.Printf("Error getting URL: %v", err)
		return requestURL
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error getting URL, status code: %v", resp.StatusCode)
		return requestURL
	}

	// Return the URL without query parameters as the cache key
	return strings.SplitN(requestURL, "?", 2)[0]
}

// parseLine parses the input line according to Squid protocol:
// [channel-ID <SP>] request-URL [<SP> extras] <NL>
// and returns the response for Squid.
func parseLine(line string, normalizeFunc func(HTTPClient, string) string) string {
	parts := strings.Fields(line)

	var requestURL string
	var response string

	// Determine if we have a channel-ID (numeric first field)
	if len(parts) >= 2 && isChannelID(parts[0]) {
		response = parts[0] + " "
		parts = parts[1:]
	}

	requestURL = parts[0]

	// Normalize the store-id for caching
	storeID := normalizeFunc(http.DefaultClient, requestURL)

	if storeID != requestURL {
		// Return the normalized store-id for caching
		response += fmt.Sprintf("OK store-id=%s", storeID)
	} else {
		// No normalization needed
		response += "OK"
	}
	return response
}

func main() {
	// Initialize logging to stderr so it doesn't interfere with stdout communication
	log.SetOutput(os.Stderr)
	log.SetPrefix("[squid-store-id] ")

	log.Println("Starting Squid store-id helper")

	// Create scanner to read from stdin line by line
	scanner := bufio.NewScanner(os.Stdin)

	// Process each line from Squid
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		response := parseLine(line, normalizeStoreID)

		log.Printf("Response: %s", response)
		fmt.Println(response)
	}

	// Check for scanning errors
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading from stdin: %v", err)
		os.Exit(1)
	}

	log.Println("Squid store-id helper shutting down")
}
