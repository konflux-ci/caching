package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

// isChannelID checks if a string represents a positive integer (for channel-ID detection)
func isChannelID(s string) bool {
	val, err := strconv.ParseInt(s, 10, 64)
	return err == nil && val >= 0
}

func normalizeStoreID(requestURL string) string {
	if !strings.Contains(requestURL, "/sha256/") {
		return requestURL
	}
	return strings.SplitN(requestURL, "?", 2)[0]
}

// parseLine parses the input line according to Squid protocol:
// [channel-ID <SP>] request-URL [<SP> extras] <NL>
// and returns the response for Squid.
func parseLine(line string, normalizeFunc func(string) string) string {
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
	storeID := normalizeFunc(requestURL)

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
func processInput(in io.Reader, out io.Writer, normalizeFunc func(string) string) error {
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
