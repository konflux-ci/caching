package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

func main() {
	const port = 9090

	// EXPECTED_AUTH is the full Authorization header value (e.g. "Basic dXNlcjpwYXNz")
	// sourced from the same secret nginx uses for header injection
	expectedAuth := os.Getenv("EXPECTED_AUTH")

	var requestCount int32

	// checkOverrideStatus returns true (and writes the response) if the client
	// requested a specific status code via X-Response-Status header.
	checkOverrideStatus := func(w http.ResponseWriter, r *http.Request) bool {
		val := r.Header.Get("X-Response-Status")
		if val == "" {
			return false
		}
		code, err := strconv.Atoi(val)
		if err != nil || code < 100 || code > 599 {
			http.Error(w, "invalid X-Response-Status value", http.StatusBadRequest)
			return true
		}
		http.Error(w, http.StatusText(code), code)
		return true
	}

	checkAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if expectedAuth == "" {
			return true
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="nginx-test-backend"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		if auth != expectedAuth {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
		return true
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if checkOverrideStatus(w, r) {
			return
		}
		target := r.URL.Query().Get("url")
		if target == "" {
			http.Error(w, "missing 'url' query parameter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusFound)
	})

	mux.HandleFunc("/content/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if checkOverrideStatus(w, r) {
			return
		}

		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")

		response := map[string]interface{}{
			"message":     "nginx-test-backend",
			"request_id":  float64(count),
			"timestamp":   float64(time.Now().Unix()),
			"server_hits": float64(count),
		}
		json.NewEncoder(w).Encode(response)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		fmt.Printf("Failed to listen on port %d: %v\n", port, err)
		os.Exit(1)
	}

	fmt.Printf("nginx-test-backend listening on port %d\n", port)
	if expectedAuth != "" {
		fmt.Println("Auth validation enabled")
	}

	server := &http.Server{Handler: mux}
	if err := server.Serve(listener); err != nil {
		fmt.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}
