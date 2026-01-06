package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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

// HTTPClient interface for making HTTP requests (allows mocking)
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

// gcrDigestCache maps GCR host/namespace to their most recent blob digest
// Key: "host/namespace" (e.g., "gcr.io/google-containers")
// Value: digest without "sha256:" prefix
// This is populated when blob requests come in and used when artifacts-downloads URLs are requested
var gcrDigestCache = struct {
	sync.RWMutex
	m map[string]string
}{
	m: make(map[string]string),
}

// isChannelID checks if a string represents a positive integer (for channel-ID detection)
func isChannelID(s string) bool {
	val, err := strconv.ParseInt(s, 10, 64)
	return err == nil && val >= 0
}

// normalizeStoreID normalizes the store-id for caching by removing query parameters from CDN URLs.
// Only content-addressable URLs (containing SHA256 hashes or GCR artifacts-downloads) are normalized.
// The request URL must return a 200 status code to ensure the request is authorized.
func normalizeStoreID(client HTTPClient, requestURL string) string {
	// Handle GCR blob request URLs: /v2/{namespace}/{repo}/blobs/sha256:{digest}
	// Store the host/namespace → digest mapping for later use with artifacts-downloads URLs
	if strings.Contains(requestURL, "/v2/") && strings.Contains(requestURL, "/blobs/sha256:") {
		blobRegex := regexp.MustCompile(`https?://([^/]+)/v2/([^/]+)/([^/]+)/blobs/sha256:([a-f0-9]{64})`)
		if matches := blobRegex.FindStringSubmatch(requestURL); len(matches) == 5 {
			host := matches[1]
			namespace := matches[2]
			digest := matches[4]
			cacheKey := host + "/" + namespace

			// Store: "host/namespace" → digest for lookup when artifacts-downloads request comes
			gcrDigestCache.Lock()
			gcrDigestCache.m[cacheKey] = digest
			gcrDigestCache.Unlock()

			log.Printf("GCR blob: %s → sha256:%s", cacheKey, digest)
			// Return original URL (blob requests are 302 redirects, not cached)
			return requestURL
		}
	}

	// Handle GCR artifacts-downloads URLs
	// These are the actual blob downloads. GCR generates unique tokens for each request,
	// so we normalize based on the host/namespace lookup from the earlier blob request.
	if strings.Contains(requestURL, "/artifacts-downloads/namespaces/") {
		// Match the full artifacts-downloads URL structure including /downloads/ path
		artifactsRegex := regexp.MustCompile(`https?://([^/]+)/artifacts-downloads/namespaces/([^/]+)/repositories/([^/]+)/downloads/`)
		matches := artifactsRegex.FindStringSubmatch(requestURL)
		if len(matches) == 4 {
			host := matches[1]
			namespace := matches[2]
			repo := matches[3]
			cacheKey := host + "/" + namespace

			// Try to find the digest by looking up host/namespace in cache
			gcrDigestCache.RLock()
			digest, found := gcrDigestCache.m[cacheKey]
			gcrDigestCache.RUnlock()

			if found {
				// Create normalized store-id using the digest
				normalizedURL := fmt.Sprintf("https://%s/artifacts-downloads/namespaces/%s/repositories/%s/downloads/sha256:%s",
					host, namespace, repo, digest)
				log.Printf("GCR artifacts-downloads: %s → sha256:%s (normalized)", cacheKey, digest)
				return normalizedURL
			}

			log.Printf("GCR artifacts-downloads: %s not found in digest cache, fallback to token hash", cacheKey)
			// If no digest found in cache, use a hash of the download token as the cache key
			// This ensures the same token always maps to the same cache entry
			tokenStart := strings.Index(requestURL, "/downloads/")
			if tokenStart >= 0 {
				token := requestURL[tokenStart+11:] // Skip "/downloads/"
				tokenHash := sha256.Sum256([]byte(token))
				hashKey := "unknown-" + hex.EncodeToString(tokenHash[:8])
				log.Printf("GCR artifacts-downloads (no digest): using token hash %s", hashKey)
				// Return URL with query params stripped
				return strings.SplitN(requestURL, "?", 2)[0]
			}
		} else {
			log.Printf("GCR artifacts-downloads: regex did not match URL: %s", requestURL)
		}
	}

	// Only normalize content-addressable URLs with SHA256 hashes in the path (Quay, Docker Hub)
	isContentAddressable := strings.Contains(requestURL, "/sha256/")
	if !isContentAddressable {
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
