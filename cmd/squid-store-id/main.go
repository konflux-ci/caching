package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTPClient interface for making HTTP requests (allows mocking)
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

// redirectCache tracks redirects from blob request URLs to artifacts-downloads URLs
// Key: normalized artifacts-downloads URL path (without token) + digest, Value: SHA256 digest from the original blob request URL
// We use path + digest as the key to ensure each blob has a unique cache entry
var redirectCache = struct {
	sync.RWMutex
	m map[string]string
}{
	m: make(map[string]string),
}

// pathToDigests maps normalized path (without token) to a list of possible digests
// This helps us find the correct digest when processing artifacts-downloads URLs
var pathToDigests = struct {
	sync.RWMutex
	m map[string][]string
}{
	m: make(map[string][]string),
}

// artifactsURLToDigest maps full artifacts-downloads URLs (with token) to their digests
// This is populated when we process blob requests and see the redirect
// Key: full artifacts-downloads URL, Value: digest
var artifactsURLToDigest = struct {
	sync.RWMutex
	m map[string]string
}{
	m: make(map[string]string),
}

// digestToArtifactsURLs maps digest to a list of artifacts-downloads URLs (with tokens) we've seen for that digest
// This allows us to match content by making requests to actual URLs (not normalized ones that don't exist)
// Key: digest, Value: list of full artifacts-downloads URLs
var digestToArtifactsURLs = struct {
	sync.RWMutex
	m map[string][]string
}{
	m: make(map[string][]string),
}

// normalizeGCRArtifactsURL removes the token from GCR artifacts-downloads URL and optionally adds the digest to create a stable, unique cache key
// If digest is provided: https://gcr.io/artifacts-downloads/namespaces/{namespace}/repositories/{repository}/downloads/sha256:{digest}
// If digest is empty: https://gcr.io/artifacts-downloads/namespaces/{namespace}/repositories/{repository}/downloads (for path key lookup)
// The token changes for each request, so we remove it and use the digest instead to allow cache matching
func normalizeGCRArtifactsURL(artifactsURL string, digest string) string {
	parsedURL, err := url.Parse(artifactsURL)
	if err != nil {
		return artifactsURL
	}
	
	// Extract path up to /downloads (remove the token part after /downloads/)
	path := parsedURL.Path
	downloadsIndex := strings.Index(path, "/downloads/")
	if downloadsIndex == -1 {
		return artifactsURL
	}
	
	// Reconstruct URL without token, optionally with digest
	normalizedPath := path[:downloadsIndex+len("/downloads")]
	if digest != "" {
		normalizedPath += "/sha256:" + digest
	}
	normalizedURL := fmt.Sprintf("%s://%s%s", parsedURL.Scheme, parsedURL.Host, normalizedPath)
	return normalizedURL
}

// isChannelID checks if a string represents a positive integer (for channel-ID detection)
func isChannelID(s string) bool {
	val, err := strconv.ParseInt(s, 10, 64)
	return err == nil && val >= 0
}

// normalizeStoreID normalizes the store-id for caching by removing query parameters from CDN URLs.
// Only content-addressable URLs (containing SHA256 hashes or GCR artifacts-downloads) are normalized.
// The request URL must return a 200 status code (or 302 for redirects) to ensure the request is authorized.
func normalizeStoreID(client HTTPClient, requestURL string) string {
	log.Printf("normalizeStoreID called for: %s", requestURL)
	// Handle GCR blob request URLs first - these contain the digest and redirect to artifacts-downloads
	if strings.Contains(requestURL, "/v2/") && strings.Contains(requestURL, "/blobs/sha256:") {
		// Extract the SHA256 digest from the blob request URL
		sha256Regex := regexp.MustCompile(`/blobs/sha256:([a-f0-9]{64})`)
		matches := sha256Regex.FindStringSubmatch(requestURL)
		if len(matches) == 2 {
			digest := matches[1]
			
			// Make the request to get the redirect location
			// Use a client that doesn't follow redirects automatically
			httpClient := &http.Client{
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			resp, err := httpClient.Get(requestURL)
			if err != nil {
				log.Printf("Error getting blob request URL: %v", err)
				// Still normalize using the digest even if request fails
				normalizedURL := fmt.Sprintf("http://gcr-blob.internal/sha256:%s", digest)
				return normalizedURL
			}
			defer resp.Body.Close()

			// If it's a redirect, store the mapping in cache and use normalized artifacts-downloads URL as store-id
			if resp.StatusCode == http.StatusFound {
				redirectLocation := resp.Header.Get("Location")
				if redirectLocation != "" {
					// Convert relative URL to absolute
					parsedURL, err := url.Parse(requestURL)
					if err == nil {
						redirectURL, err := parsedURL.Parse(redirectLocation)
						if err == nil {
							absoluteRedirectURL := redirectURL.String()
							// Normalize the URL (remove token, add digest) to create a stable, unique cache key
							normalizedKey := normalizeGCRArtifactsURL(absoluteRedirectURL, digest)
							// Store the mapping: path + digest -> digest (for lookup)
							// Use path + digest as the key to ensure each blob has a unique cache entry
							pathKey := normalizeGCRArtifactsURL(absoluteRedirectURL, "") + "/sha256:" + digest
							pathBase := normalizeGCRArtifactsURL(absoluteRedirectURL, "")
							redirectCache.Lock()
							redirectCache.m[pathKey] = digest
							// Also store in pathToDigests for reverse lookup
							if pathToDigests.m[pathBase] == nil {
								pathToDigests.m[pathBase] = []string{}
							}
							// Add digest if not already present
							found := false
							for _, d := range pathToDigests.m[pathBase] {
								if d == digest {
									found = true
									break
								}
							}
							if !found {
								pathToDigests.m[pathBase] = append(pathToDigests.m[pathBase], digest)
							}
							// Store mapping from full artifacts-downloads URL (with token) to digest
							// This allows us to look up the digest directly when we see the same URL again
							artifactsURLToDigest.m[absoluteRedirectURL] = digest
							// Also store reverse mapping: digest -> list of artifacts-downloads URLs
							if digestToArtifactsURLs.m[digest] == nil {
								digestToArtifactsURLs.m[digest] = []string{}
							}
							// Add URL if not already present
							foundURL := false
							for _, u := range digestToArtifactsURLs.m[digest] {
								if u == absoluteRedirectURL {
									foundURL = true
									break
								}
							}
							if !foundURL {
								digestToArtifactsURLs.m[digest] = append(digestToArtifactsURLs.m[digest], absoluteRedirectURL)
							}
							cacheSize := len(redirectCache.m)
							redirectCache.Unlock()
							// #region agent log
							func() {
								logData, _ := json.Marshal(map[string]interface{}{
									"sessionId":    "debug-session",
									"runId":        "run1",
									"hypothesisId": "D",
									"location":     "cmd/squid-store-id/main.go:112",
									"message":      "Storing redirect mapping",
									"data": map[string]interface{}{
										"blobURL":        requestURL,
										"redirectURL":    absoluteRedirectURL,
										"digest":         digest,
										"pathKey":        pathKey,
										"normalizedKey":  normalizedKey,
										"cacheSizeAfter": cacheSize,
									},
									"timestamp": time.Now().UnixMilli(),
								})
								if f, err := os.OpenFile("/home/hmariset/Projects/Github/caching/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
									fmt.Fprintln(f, string(logData))
									f.Close()
								}
							}()
							// #endregion
							log.Printf("Stored redirect mapping: %s -> sha256:%s (normalized: %s, cache key: %s). Cache now has %d entries", absoluteRedirectURL, digest, normalizedKey, pathKey, cacheSize)
							// Return the normalized URL with digest as the store-id
							return normalizedKey
						}
					}
				}
			}
			
			// Fallback: if no redirect or error, use internal URL format
			normalizedURL := fmt.Sprintf("http://gcr-blob.internal/sha256:%s", digest)
			return normalizedURL
		}
	}

	// Only normalize content-addressable URLs:
	// 1. URLs with SHA256 hashes in the path (Quay, Docker Hub)
	// 2. GCR artifacts-downloads URLs
	isContentAddressable := strings.Contains(requestURL, "/sha256/") ||
	strings.Contains(requestURL, "/artifacts-downloads/namespaces/")
	if !isContentAddressable {
		return requestURL
	}

	// For GCR artifacts-downloads URLs, we need to find the correct digest to create a stable cache key.
	// Since the token in the URL is unique per request, we can't determine the digest from the URL alone.
	// GCR doesn't support HEAD requests (returns 400), so we can't extract digest from headers.
	// Solution: First check if we've seen this exact URL before (with same token), then fall back to path-based lookup.
	if strings.Contains(requestURL, "/artifacts-downloads/namespaces/") {
		// First, check if we've seen this exact URL before (with same token)
		artifactsURLToDigest.RLock()
		digest, found := artifactsURLToDigest.m[requestURL]
		artifactsURLToDigest.RUnlock()
		
		if found {
			normalizedKey := normalizeGCRArtifactsURL(requestURL, digest)
			log.Printf("Found digest in artifactsURLToDigest cache for URL: sha256:%s (returning store-id: %s)", digest, normalizedKey)
			return normalizedKey
		}
		
		// Not found in exact URL cache, try path-based lookup
		pathBase := normalizeGCRArtifactsURL(requestURL, "")
		
		// Get list of possible digests for this path
		pathToDigests.RLock()
		possibleDigests := pathToDigests.m[pathBase]
		pathToDigests.RUnlock()
		
		if len(possibleDigests) == 0 {
			// No digests found for this path, return original URL
			log.Printf("No digests found in cache for path: %s", pathBase)
			return requestURL
		}
		
		// If only one digest, use it directly (common case)
		if len(possibleDigests) == 1 {
			digest := possibleDigests[0]
			normalizedKey := normalizeGCRArtifactsURL(requestURL, digest)
			// Store in artifactsURLToDigest for future lookups
			artifactsURLToDigest.Lock()
			artifactsURLToDigest.m[requestURL] = digest
			artifactsURLToDigest.Unlock()
			log.Printf("Found single digest in cache for path %s: sha256:%s (returning store-id: %s)", pathBase, digest, normalizedKey)
			return normalizedKey
		}
		
		// Multiple digests possible - we need to match by content
		// Make a small GET request (first 1KB) to the current URL to compute hash
		// Then compare with stored artifacts-downloads URLs for each candidate digest
		getReq, err := http.NewRequest("GET", requestURL, nil)
		if err != nil {
			log.Printf("Error creating GET request: %v", err)
			return requestURL
		}
		getReq.Header.Set("Range", "bytes=0-1023")
		
		getResp, err := http.DefaultClient.Do(getReq)
		if err != nil {
			log.Printf("Error making range request: %v", err)
			// Fallback: use first digest (not ideal but better than nothing)
			digest := possibleDigests[0]
			normalizedKey := normalizeGCRArtifactsURL(requestURL, digest)
			log.Printf("Error making range request, using first digest: sha256:%s", digest)
			return normalizedKey
		}
		defer getResp.Body.Close()
		
		if getResp.StatusCode != http.StatusOK && getResp.StatusCode != http.StatusPartialContent {
			log.Printf("Range request returned status: %v, using first digest", getResp.StatusCode)
			digest := possibleDigests[0]
			normalizedKey := normalizeGCRArtifactsURL(requestURL, digest)
			return normalizedKey
		}
		
		// Read first 1KB and compute hash
		sample := make([]byte, 1024)
		n, err := io.ReadFull(getResp.Body, sample)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Printf("Error reading sample: %v, using first digest", err)
			digest := possibleDigests[0]
			normalizedKey := normalizeGCRArtifactsURL(requestURL, digest)
			return normalizedKey
		}
		
		// Compute hash of sample
		hash := sha256.Sum256(sample[:n])
		hashStr := hex.EncodeToString(hash[:])
		
		// Try each digest by making range requests to stored artifacts-downloads URLs for that digest
		// and comparing the hash of the first 1KB
		digestToArtifactsURLs.RLock()
		for _, candidateDigest := range possibleDigests {
			storedURLs := digestToArtifactsURLs.m[candidateDigest]
			if len(storedURLs) == 0 {
				continue
			}
			
			// Try the first stored URL for this digest (they should all have the same content)
			candidateURL := storedURLs[0]
			candidateReq, err := http.NewRequest("GET", candidateURL, nil)
			if err != nil {
				continue
			}
			candidateReq.Header.Set("Range", "bytes=0-1023")
			
			candidateResp, err := http.DefaultClient.Do(candidateReq)
			if err != nil {
				continue
			}
			
			if candidateResp.StatusCode == http.StatusOK || candidateResp.StatusCode == http.StatusPartialContent {
				candidateSample := make([]byte, 1024)
				candidateN, err := io.ReadFull(candidateResp.Body, candidateSample)
				candidateResp.Body.Close()
				
				if err == nil || err == io.EOF || err == io.ErrUnexpectedEOF {
					candidateHash := sha256.Sum256(candidateSample[:candidateN])
					candidateHashStr := hex.EncodeToString(candidateHash[:])
					
					// Compare hashes
					if hashStr == candidateHashStr {
						// Found match!
						digestToArtifactsURLs.RUnlock()
						normalizedKey := normalizeGCRArtifactsURL(requestURL, candidateDigest)
						// Store in artifactsURLToDigest for future lookups
						artifactsURLToDigest.Lock()
						artifactsURLToDigest.m[requestURL] = candidateDigest
						artifactsURLToDigest.Unlock()
						// Also add to digestToArtifactsURLs
						digestToArtifactsURLs.Lock()
						if digestToArtifactsURLs.m[candidateDigest] == nil {
							digestToArtifactsURLs.m[candidateDigest] = []string{}
						}
						foundURL := false
						for _, u := range digestToArtifactsURLs.m[candidateDigest] {
							if u == requestURL {
								foundURL = true
								break
							}
						}
						if !foundURL {
							digestToArtifactsURLs.m[candidateDigest] = append(digestToArtifactsURLs.m[candidateDigest], requestURL)
						}
						digestToArtifactsURLs.Unlock()
						log.Printf("Matched digest by content hash for path %s: sha256:%s (returning store-id: %s)", pathBase, candidateDigest, normalizedKey)
						return normalizedKey
					}
				}
			}
		}
		digestToArtifactsURLs.RUnlock()
		
		// No match found, use first digest as fallback
		fallbackDigest := possibleDigests[0]
		normalizedKey := normalizeGCRArtifactsURL(requestURL, fallbackDigest)
		log.Printf("Multiple digests found for path %s, no content match, using first: sha256:%s (sample hash: %s)", pathBase, fallbackDigest, hashStr[:16])
		return normalizedKey
	}

	// Issue the request to the CDN/S3 to check authorization but don't read the body
	resp, err := client.Get(requestURL)
	if err != nil {
		// Don't log the request URL to avoid leaking sensitive information
		log.Printf("Error getting URL: %v", err)
		return requestURL
	}

	defer resp.Body.Close()

	// Accept both 200 (direct) and 302 (redirect) responses
	// GCR blob requests return 302 redirects to artifacts-downloads URLs
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
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
		log.Printf("Returning normalized store-id: %s (original: %s)", storeID, requestURL)
		// #region agent log
		func() {
			logData, _ := json.Marshal(map[string]interface{}{
				"sessionId":    "debug-session",
				"runId":        "run1",
				"hypothesisId": "E",
				"location":     "cmd/squid-store-id/main.go:424",
				"message":      "Returning store-id to Squid",
				"data": map[string]interface{}{
					"originalURL": requestURL,
					"storeID":     storeID,
					"response":    response,
				},
				"timestamp": time.Now().UnixMilli(),
			})
			if f, err := os.OpenFile("/home/hmariset/Projects/Github/caching/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				fmt.Fprintln(f, string(logData))
				f.Close()
			}
		}()
		// #endregion
	} else {
		// No normalization needed
		response += "OK"
		log.Printf("No normalization needed for: %s", requestURL)
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
			// Write response with explicit newline and flush to ensure Squid receives it immediately
			fmt.Fprintf(out, "%s\n", response)
			if flusher, ok := out.(interface{ Flush() error }); ok {
				flusher.Flush()
			}
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
