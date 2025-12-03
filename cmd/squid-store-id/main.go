package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Quay.io CDN patterns
var cdnRegex = regexp.MustCompile(`^https://cdn(\d{2})?\.quay\.io/.+/sha256/.+/[a-f0-9]{64}`)

// S3 URL patterns - supports both path-style and virtual-hosted-style for quay.io
// Path-style: https://s3.region.amazonaws.com/quayio-production-s3/sha256/.../hash
// Virtual-hosted: https://quayio-production-s3.s3.region.amazonaws.com/sha256/.../hash
var s3Regex = regexp.MustCompile(`^https://(?:quayio-production-s3\.s3[a-z0-9.-]*\.amazonaws\.com/sha256/.+/[a-f0-9]{64}|s3\.[a-z0-9-]+\.amazonaws\.com/quayio-production-s3/sha256/.+/[a-f0-9]{64})`)

// Docker Hub Cloudflare R2 patterns
// Example: https://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data
var dockerHubR2Regex = regexp.MustCompile(`^https://docker-images-prod\.[a-f0-9]{32}\.r2\.cloudflarestorage\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`)

// Docker Hub Cloudflare CDN pattern (production.cloudflare.docker.com)
// Example: https://production.cloudflare.docker.com/registry-v2/docker/registry/v2/blobs/sha256/24/24c63b8dcb66721062f32b893ef1027404afddd62aade87f3f39a3a6e70a74d0/data
var dockerHubCloudflareCDNRegex = regexp.MustCompile(`^https://production\.cloudflare\.docker\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`)

// HTTPClient interface for making HTTP requests (allows mocking)
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

// isChannelID checks if a string represents a positive integer (for channel-ID detection)
func isChannelID(s string) bool {
	val, err := strconv.ParseInt(s, 10, 64)
	return err == nil && val >= 0
}

// normalizeStoreID normalizes the store-id for caching by removing query parameters from CDN and S3 URLs.
// The request URL must return a 200 status code to ensure the request is authorized.
func normalizeStoreID(client HTTPClient, requestURL string) string {
	// Check if URL matches any of the content-addressable CDN patterns
	if !cdnRegex.MatchString(requestURL) &&
		!s3Regex.MatchString(requestURL) &&
		!dockerHubR2Regex.MatchString(requestURL) &&
		!dockerHubCloudflareCDNRegex.MatchString(requestURL) {
		return requestURL
	}

	// Issue the request to the CDN/S3 to check authorization but don't read the body
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

// processInput reads lines from in, processes each concurrently, and writes responses to out
func processInput(in io.Reader, out io.Writer, normalizeFunc func(HTTPClient, string) string) error {
	scanner := bufio.NewScanner(in)

	// Use a wait group to ensure all goroutines gracefully exit
	wg := sync.WaitGroup{}

	// Process each line from Squid concurrently
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		wg.Add(1)
		go func(l string) {
			defer wg.Done()
			response := parseLine(l, normalizeFunc)
			log.Printf("Response: %s", response)
			fmt.Fprintln(out, response)
		}(line)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Check for scanning errors
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func main() {
	// Initialize logging to stderr so it doesn't interfere with stdout communication
	log.SetOutput(os.Stderr)
	log.SetPrefix("[squid-store-id] ")

	log.Println("Starting Squid store-id helper")

	if err := processInput(os.Stdin, os.Stdout, normalizeStoreID); err != nil {
		log.Printf("Error reading from stdin: %v", err)
		os.Exit(1)
	}

	log.Println("Squid store-id helper shutting down")
}
