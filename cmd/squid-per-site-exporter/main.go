package main

import (
	"bufio"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// fileExists returns true if the path exists and is not a directory
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if info, err := os.Stat(path); err == nil {
		return !info.IsDir()
	}
	return false
}

// getEnvDefault returns the environment variable value or the default if not set
func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

var (
	// Configuration with environment variable fallbacks for container-friendly deployment
	listenAddress = flag.String("web.listen-address",
		getEnvDefault("WEB_LISTEN_ADDRESS", ":9302"),
		"Address to listen on for web interface and telemetry. (Env: WEB_LISTEN_ADDRESS)")

	// TLS configuration flags with environment variable support
	tlsCertFile = flag.String("web.tls-cert-file",
		getEnvDefault("WEB_TLS_CERT_FILE", "/etc/ssl/certs/exporter.crt"),
		"Path to TLS certificate file (enables HTTPS). (Env: WEB_TLS_CERT_FILE)")
	tlsKeyFile = flag.String("web.tls-key-file",
		getEnvDefault("WEB_TLS_KEY_FILE", "/etc/ssl/private/exporter.key"),
		"Path to TLS private key file (enables HTTPS). (Env: WEB_TLS_KEY_FILE)")
	// Require TLS by default; explicitly disable to allow HTTP
	tlsRequired = flag.Bool("web.tls-required",
		getEnvDefault("WEB_TLS_REQUIRED", "true") == "true",
		"Require TLS certificate and key to be present. If true and files are missing, the server will not start. (Env: WEB_TLS_REQUIRED)")

	// Prometheus metrics
	squidHitRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "squid_site_hit_ratio",
			Help: "Hit ratio per site (hits / (hits + misses))",
		},
		[]string{"hostname"},
	)

	squidHitTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "squid_site_hits_total",
			Help: "Total number of cache hits per site",
		},
		[]string{"hostname"},
	)

	squidMissTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "squid_site_misses_total",
			Help: "Total number of cache misses per site",
		},
		[]string{"hostname"},
	)

	squidRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "squid_site_requests_total",
			Help: "Total number of requests per site",
		},
		[]string{"hostname"},
	)

	squidBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "squid_site_bytes_total",
			Help: "Total bytes transferred per site",
		},
		[]string{"hostname"},
	)

	squidResponseTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "squid_site_response_time_seconds",
			Help:    "Response time per site in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"hostname"},
	)
)

type SiteStats struct {
	Hits     int64
	Misses   int64
	Bytes    int64
	Requests int64
}

type Exporter struct {
	mutex     sync.RWMutex
	siteStats map[string]*SiteStats
}

func NewExporter() *Exporter {
	return &Exporter{
		siteStats: make(map[string]*SiteStats),
	}
}

func (e *Exporter) parseLogLine(line string) {
	// Squid log format: timestamp elapsedtime remotehost code/status bytes method URL rfc931 peerstatus/peerhost type
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return
	}

	// Extract relevant fields
	elapsedTimeStr := fields[1]
	codeStatus := fields[3]
	bytesStr := fields[4]
	method := fields[5]
	urlStr := fields[6]

	// Skip non-HTTP methods
	if !strings.HasPrefix(method, "GET") && !strings.HasPrefix(method, "POST") &&
		!strings.HasPrefix(method, "HEAD") && !strings.HasPrefix(method, "PUT") {
		return
	}

	// Parse URL to extract hostname
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		return
	}

	// Parse bytes
	bytes, err := strconv.ParseInt(bytesStr, 10, 64)
	if err != nil {
		bytes = 0
	}

	// Parse elapsed time
	elapsedTime, err := strconv.ParseFloat(elapsedTimeStr, 64)
	if err != nil {
		elapsedTime = 0
	}

	// Determine hit/miss from status code
	isHit := false
	if strings.Contains(codeStatus, "HIT") || strings.Contains(codeStatus, "REFRESH") {
		isHit = true
	}

	// Update stats
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if _, exists := e.siteStats[hostname]; !exists {
		e.siteStats[hostname] = &SiteStats{}
	}

	stats := e.siteStats[hostname]
	stats.Requests++
	stats.Bytes += bytes

	if isHit {
		stats.Hits++
	} else {
		stats.Misses++
	}

	// Update Prometheus metrics
	squidRequestsTotal.WithLabelValues(hostname).Inc()
	squidBytesTotal.WithLabelValues(hostname).Add(float64(bytes))
	squidResponseTime.WithLabelValues(hostname).Observe(elapsedTime / 1000.0) // Convert ms to seconds

	if isHit {
		squidHitTotal.WithLabelValues(hostname).Inc()
	} else {
		squidMissTotal.WithLabelValues(hostname).Inc()
	}

	// Ensure both hit and miss counters are initialized (even if 0) for this hostname
	// This ensures squid_site_hits_total appears in metrics output even with 0 value
	squidHitTotal.WithLabelValues(hostname).Add(0)
	squidMissTotal.WithLabelValues(hostname).Add(0)

	// Update hit ratio
	if stats.Requests > 0 {
		ratio := float64(stats.Hits) / float64(stats.Requests)
		squidHitRatio.WithLabelValues(hostname).Set(ratio)
	}
}

func (e *Exporter) readFromStdin() {
	log.Printf("Reading squid logs from stdin")
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			e.parseLogLine(line)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading from stdin: %v", err)
	}
}

func init() {
	// Register Prometheus metrics
	prometheus.MustRegister(squidHitRatio)
	prometheus.MustRegister(squidHitTotal)
	prometheus.MustRegister(squidMissTotal)
	prometheus.MustRegister(squidRequestsTotal)
	prometheus.MustRegister(squidBytesTotal)
	prometheus.MustRegister(squidResponseTime)
}

func main() {
	flag.Parse()

	log.Printf("Starting squid per-site exporter")
	log.Printf("Listening on %s", *listenAddress)
	log.Printf("Reading logs from stdin (use shell redirection for files)")

	exporter := NewExporter()

	// Start reading from stdin in background
	go exporter.readFromStdin()

	// Setup HTTP handlers
	// Use HandlerFor with custom options to control content type format
	handler := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		// Disable the escaping=values parameter to match expected format
		EnableOpenMetrics: false,
	})
	http.Handle("/metrics", handler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>Squid Per-Site Exporter</title></head>
			<body>
			<h1>Squid Per-Site Exporter</h1>
			<p><a href='/metrics'>Metrics</a></p>
			</body>
			</html>`))
	})

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Start server with secure-by-default behavior
	if fileExists(*tlsCertFile) && fileExists(*tlsKeyFile) {
		log.Printf("Starting HTTPS server on %s", *listenAddress)
		log.Printf("Using TLS cert: %s", *tlsCertFile)
		log.Printf("Using TLS key: %s", *tlsKeyFile)
		log.Fatal(http.ListenAndServeTLS(*listenAddress, *tlsCertFile, *tlsKeyFile, nil))
	} else if !*tlsRequired {
		log.Printf("Starting HTTP server on %s", *listenAddress)
		log.Printf("Warning: Running without TLS encryption. Use -web.tls-cert-file and -web.tls-key-file for HTTPS.")
		log.Fatal(http.ListenAndServe(*listenAddress, nil))
	} else {
		log.Fatalf("TLS required but certificate or key not found (cert: %s, key: %s). Set WEB_TLS_REQUIRED=false to allow HTTP.", *tlsCertFile, *tlsKeyFile)
	}
}
