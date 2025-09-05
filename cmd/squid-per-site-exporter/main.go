package main

import (
	"bufio"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
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

// getEnvDurationDefault returns the duration from env or the provided default
func getEnvDurationDefault(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

var (
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

type Exporter struct {
	mutex     sync.RWMutex
	parseFunc func(string)
}

func NewExporter() *Exporter {
	e := &Exporter{}
	// Default parsing function
	e.parseFunc = e.parseLogLine
	return e
}

// getCounterValue reads the current value of a labeled Counter from a CounterVec
func getCounterValue(vec *prometheus.CounterVec, hostname string) (float64, error) {
	m, err := vec.GetMetricWithLabelValues(hostname)
	if err != nil {
		return 0, err
	}
	pb := &dto.Metric{}
	if err := m.Write(pb); err != nil {
		return 0, err
	}
	if pb.Counter == nil || pb.Counter.Value == nil {
		return 0, nil
	}
	return pb.Counter.GetValue(), nil
}

func (e *Exporter) parseLogLine(line string) {
	// Squid log format: timestamp elapsedtime remotehost code/status bytes method URL rfc931 peerstatus/peerhost type
	fields := strings.Fields(line)
	if len(fields) < 7 {
		log.Printf("Malformed access log entry: need >=7 fields, got %d: %q", len(fields), line)
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
		log.Printf("Unsupported method %q", method)
		return
	}

	// Parse URL to extract hostname
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		log.Printf("Invalid request URL %q: %v", urlStr, err)
		return
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		log.Printf("Missing hostname in URL %q", urlStr)
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

	// Determine hit/miss from result code (token before '/')
	// Consider only codes ending in "_HIT" as cache hits (e.g., TCP_HIT, MEM_HIT).
	statusToken := codeStatus
	if idx := strings.Index(codeStatus, "/"); idx >= 0 {
		statusToken = codeStatus[:idx]
	}
	isHit := strings.HasSuffix(statusToken, "_HIT")

	// Update Prometheus metrics
	e.mutex.Lock()
	defer e.mutex.Unlock()

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

	// Update hit ratio from Prometheus counters to keep alignment with exported metrics
	hits, _ := getCounterValue(squidHitTotal, hostname)
	reqs, _ := getCounterValue(squidRequestsTotal, hostname)
	if reqs > 0 {
		squidHitRatio.WithLabelValues(hostname).Set(hits / reqs)
	}
}

func (e *Exporter) readFromStdin() {
	// Fail fast if constructed without NewExporter()
	if e.parseFunc == nil {
		panic("Exporter not initialized correctly: use NewExporter() to set parseFunc")
	}
	log.Printf("Reading squid logs from stdin")
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			e.parseFunc(line)
			// Forward input to stdout so container logs still contain Squid access logs
			if _, err := os.Stdout.WriteString(line + "\n"); err != nil {
				log.Fatalf("Failed to forward log line to stdout: %v", err)
			}
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
	// Configuration with environment variable fallbacks for container-friendly deployment
	listenAddress := flag.String("web.listen-address",
		getEnvDefault("WEB_LISTEN_ADDRESS", ":9302"),
		"Address to listen on for web interface and telemetry. (Env: WEB_LISTEN_ADDRESS)")

	// TLS configuration flags with environment variable support
	tlsCertFile := flag.String("web.tls-cert-file",
		getEnvDefault("WEB_TLS_CERT_FILE", "/etc/squid/certs/tls.crt"),
		"Path to TLS certificate file (enables HTTPS). (Env: WEB_TLS_CERT_FILE)")
	tlsKeyFile := flag.String("web.tls-key-file",
		getEnvDefault("WEB_TLS_KEY_FILE", "/etc/squid/certs/tls.key"),
		"Path to TLS private key file (enables HTTPS). (Env: WEB_TLS_KEY_FILE)")
	// Require TLS by default; explicitly disable to allow HTTP
	tlsRequired := flag.Bool("web.tls-required",
		getEnvDefault("WEB_TLS_REQUIRED", "true") == "true",
		"Require TLS certificate and key to be present. If true and files are missing, the server will not start. (Env: WEB_TLS_REQUIRED)")

	// Health check options
	squidHealthAddr := flag.String("squid.health-addr",
		getEnvDefault("SQUID_HEALTH_ADDR", "127.0.0.1:3128"),
		"Address to check Squid health (host:port). (Env: SQUID_HEALTH_ADDR)")
	squidHealthTimeout := flag.Duration("squid.health-timeout",
		getEnvDurationDefault("SQUID_HEALTH_TIMEOUT", 500*time.Millisecond),
		"Timeout for Squid health dial (e.g., 500ms). (Env: SQUID_HEALTH_TIMEOUT)")

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

	// Health check endpoint: validates exporter process and Squid TCP port
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		conn, err := net.DialTimeout("tcp", *squidHealthAddr, *squidHealthTimeout)
		if err != nil {
			http.Error(w, "squid unreachable", http.StatusServiceUnavailable)
			return
		}
		_ = conn.Close()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Start server based on TLS configuration
	certPresent := fileExists(*tlsCertFile) && fileExists(*tlsKeyFile)
	if *tlsRequired {
		if certPresent {
			log.Printf("Starting HTTPS server on %s", *listenAddress)
			log.Printf("Using TLS cert: %s", *tlsCertFile)
			log.Printf("Using TLS key: %s", *tlsKeyFile)
			log.Fatal(http.ListenAndServeTLS(*listenAddress, *tlsCertFile, *tlsKeyFile, nil))
		}
		log.Fatalf("TLS required but certificate or key not found (cert: %s, key: %s).", *tlsCertFile, *tlsKeyFile)
	} else {
		if certPresent {
			log.Printf("TLS not required but certificates found; starting HTTPS on %s", *listenAddress)
			log.Fatal(http.ListenAndServeTLS(*listenAddress, *tlsCertFile, *tlsKeyFile, nil))
		}
		log.Printf("TLS disabled; starting HTTP server on %s", *listenAddress)
		log.Fatal(http.ListenAndServe(*listenAddress, nil))
	}
}
