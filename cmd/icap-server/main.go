package main

import (
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/intra-sh/icap"
)

// Quay.io CDN patterns
var cdnRegex = regexp.MustCompile(`^https://cdn(\d{2})?\.quay\.io/.+/sha256/.+/[a-f0-9]{64}`)

// Docker Hub Cloudflare R2 patterns
// Example: https://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data
var dockerHubR2Regex = regexp.MustCompile(`^https://docker-images-prod\.[a-f0-9]{32}\.r2\.cloudflarestorage\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`)

// Docker Hub Cloudflare CDN pattern (production.cloudflare.docker.com)
// Example: https://production.cloudflare.docker.com/registry-v2/docker/registry/v2/blobs/sha256/24/24c63b8dcb66721062f32b893ef1027404afddd62aade87f3f39a3a6e70a74d0/data
var dockerHubCloudflareCDNRegex = regexp.MustCompile(`^https://production\.cloudflare\.docker\.com/registry-v2/docker/registry/v2/blobs/sha256/[a-f0-9]{2}/[a-f0-9]{64}/data`)

// reqmodHandler handles REQMOD requests
func reqmodHandler(w icap.ResponseWriter, req *icap.Request) {
	h := w.Header()
	h.Set("ISTag", "\"SQUID-ICAP-REQMOD\"")
	h.Set("Service", "Squid ICAP REQMOD")

	switch req.Method {
	case "OPTIONS":
		h.Set("Methods", "REQMOD")
		// Support 204 responses (if the client also allows it)
		h.Set("Allow", "204")
		// Don't allow clients to send preview bytes
		h.Set("Preview", "0")
		writeHeaderAndLog(w, req, 200)
	case "REQMOD":
		// If there is no encapsulated HTTP request, return a 200 response
		if req.Request == nil {
			writeHeaderAndLog(w, req, 200)
			return
		}

		// If the request is for a content-addressable CDN URL, delete the Authorization header
		requestURL := req.Request.URL.String()
		if cdnRegex.MatchString(requestURL) ||
			dockerHubR2Regex.MatchString(requestURL) ||
			dockerHubCloudflareCDNRegex.MatchString(requestURL) {
			req.Request.Header.Del("Authorization")
			writeHeaderAndLog(w, req, 200)
			return
		}

		// No modification is needed for the request
		// If the client allows 204 responses, use that to reduce bandwidth usage
		if req.Header.Get("Allow") == "204" {
			writeHeaderAndLog(w, req, 204)
			return
		}

		// Otherwise, return a 200 response
		writeHeaderAndLog(w, req, 200)
	default:
		// Unsupported method
		writeHeaderAndLog(w, req, 405)
	}
}

// writeHeaderAndLog writes the ICAP response header and logs the request with the resulting status code
func writeHeaderAndLog(w icap.ResponseWriter, req *icap.Request, code int) {
	url := ""
	if req.Request != nil {
		// Remove credentials and potentially sensitive query parameters from the encapsulate HTTP request URL
		url = strings.SplitN(req.Request.URL.Redacted(), "?", 2)[0]
	}

	log.Println(req.Method, code, url)

	if req.Request != nil && code == 200 {
		w.WriteHeader(code, req.Request, false)
	} else {
		w.WriteHeader(code, nil, false)
	}
}

func main() {
	log.SetOutput(os.Stdout)

	port := os.Getenv("ICAP_PORT")
	if port == "" {
		port = "1344"
	}

	icap.HandleFunc("/reqmod", reqmodHandler)

	log.Println("Starting ICAP server on port", port)
	if err := icap.ListenAndServe(":"+port, nil); err != nil {
		log.Println("Error starting server:", err)
		os.Exit(1)
	}
}
