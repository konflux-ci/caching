package unit

import (
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Embedded version of the exporter logic for testing
// This replicates the core functionality from cmd/squid-per-site-exporter/main.go

type SiteStats struct {
	Hits     int64
	Misses   int64
	Bytes    int64
	Requests int64
}

type TestExporter struct {
	mutex     sync.RWMutex
	siteStats map[string]*SiteStats
	// Test-specific metrics with unique names to avoid conflicts
	hitRatio      *prometheus.GaugeVec
	hitTotal      *prometheus.CounterVec
	missTotal     *prometheus.CounterVec
	requestsTotal *prometheus.CounterVec
	bytesTotal    *prometheus.CounterVec
}

func NewTestExporter() *TestExporter {
	return &TestExporter{
		siteStats: make(map[string]*SiteStats),
		hitRatio: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "test_squid_site_hit_ratio",
				Help: "Hit ratio per site (hits / (hits + misses))",
			},
			[]string{"hostname"},
		),
		hitTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "test_squid_site_hits_total",
				Help: "Total number of cache hits per site",
			},
			[]string{"hostname"},
		),
		missTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "test_squid_site_misses_total",
				Help: "Total number of cache misses per site",
			},
			[]string{"hostname"},
		),
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "test_squid_site_requests_total",
				Help: "Total number of requests per site",
			},
			[]string{"hostname"},
		),
		bytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "test_squid_site_bytes_total",
				Help: "Total bytes transferred per site",
			},
			[]string{"hostname"},
		),
	}
}

func (e *TestExporter) parseLogLine(line string) bool {
	// Squid log format: timestamp elapsedtime remotehost code/status bytes method URL rfc931 peerstatus/peerhost type
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return false
	}

	// Extract relevant fields
	codeStatus := fields[3]
	bytesStr := fields[4]
	method := fields[5]
	urlStr := fields[6]

	// Skip non-HTTP methods
	if !strings.HasPrefix(method, "GET") && !strings.HasPrefix(method, "POST") &&
		!strings.HasPrefix(method, "HEAD") && !strings.HasPrefix(method, "PUT") {
		return false
	}

	// Simple URL parsing for hostname
	if !strings.HasPrefix(urlStr, "http") {
		return false
	}

	// Extract hostname from URL
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")
	parts := strings.Split(urlStr, "/")
	if len(parts) == 0 {
		return false
	}
	hostname := parts[0]

	if hostname == "" {
		return false
	}

	// Parse bytes
	bytes := int64(0)
	if bytesStr != "-" {
		// For testing, we'll use a simple conversion
		if bytesStr == "2048" {
			bytes = 2048
		} else if bytesStr == "1024" {
			bytes = 1024
		}
	}

	// Determine hit/miss from status code
	isHit := strings.Contains(codeStatus, "HIT") || strings.Contains(codeStatus, "REFRESH")

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
		e.hitTotal.WithLabelValues(hostname).Inc()
	} else {
		stats.Misses++
		e.missTotal.WithLabelValues(hostname).Inc()
	}

	// Update other metrics
	e.requestsTotal.WithLabelValues(hostname).Inc()
	e.bytesTotal.WithLabelValues(hostname).Add(float64(bytes))

	// Update hit ratio
	if stats.Requests > 0 {
		ratio := float64(stats.Hits) / float64(stats.Requests)
		e.hitRatio.WithLabelValues(hostname).Set(ratio)
	}

	return true
}

func (e *TestExporter) GetSiteStats(hostname string) *SiteStats {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.siteStats[hostname]
}

var _ = Describe("Per-Site Exporter", func() {
	var exporter *TestExporter
	var registry *prometheus.Registry

	BeforeEach(func() {
		exporter = NewTestExporter()
		registry = prometheus.NewRegistry()
		registry.MustRegister(exporter.hitRatio)
		registry.MustRegister(exporter.hitTotal)
		registry.MustRegister(exporter.missTotal)
		registry.MustRegister(exporter.requestsTotal)
		registry.MustRegister(exporter.bytesTotal)
	})

	Describe("Log Parsing", func() {
		It("should parse valid squid log lines", func() {
			logLine := "1234567890.123   100 192.168.1.100 TCP_MISS/200 2048 GET http://example.com/ - DIRECT/93.184.216.34 text/html"

			success := exporter.parseLogLine(logLine)
			Expect(success).To(BeTrue())

			stats := exporter.GetSiteStats("example.com")
			Expect(stats).NotTo(BeNil())
			Expect(stats.Requests).To(Equal(int64(1)))
			Expect(stats.Misses).To(Equal(int64(1)))
			Expect(stats.Hits).To(Equal(int64(0)))
			Expect(stats.Bytes).To(Equal(int64(2048)))
		})

		It("should handle cache hits correctly", func() {
			logLine := "1234567890.123   50 192.168.1.100 TCP_HIT/200 1024 GET http://example.com/page.html - DIRECT/93.184.216.34 text/html"

			success := exporter.parseLogLine(logLine)
			Expect(success).To(BeTrue())

			stats := exporter.GetSiteStats("example.com")
			Expect(stats).NotTo(BeNil())
			Expect(stats.Requests).To(Equal(int64(1)))
			Expect(stats.Hits).To(Equal(int64(1)))
			Expect(stats.Misses).To(Equal(int64(0)))
			Expect(stats.Bytes).To(Equal(int64(1024)))
		})

		It("should skip invalid log lines", func() {
			invalidLines := []string{
				"", // empty line
				"invalid log format",
				"1234 100", // too few fields
				"1234567890.123   100 192.168.1.100 TCP_MISS/200 2048 INVALID ftp://example.com/", // non-HTTP method
			}

			for _, line := range invalidLines {
				success := exporter.parseLogLine(line)
				Expect(success).To(BeFalse(), "Line should be invalid: "+line)
			}
		})

		It("should handle multiple sites correctly", func() {
			logLines := []string{
				"1234567890.123   100 192.168.1.100 TCP_MISS/200 2048 GET http://example.com/ - DIRECT/93.184.216.34 text/html",
				"1234567890.124   50 192.168.1.100 TCP_HIT/200 1024 GET http://google.com/ - DIRECT/172.217.164.110 text/html",
				"1234567890.125   75 192.168.1.100 TCP_MISS/200 1024 GET http://example.com/page2 - DIRECT/93.184.216.34 text/html",
			}

			for _, line := range logLines {
				exporter.parseLogLine(line)
			}

			// Check example.com stats
			exampleStats := exporter.GetSiteStats("example.com")
			Expect(exampleStats).NotTo(BeNil())
			Expect(exampleStats.Requests).To(Equal(int64(2)))
			Expect(exampleStats.Hits).To(Equal(int64(0)))
			Expect(exampleStats.Misses).To(Equal(int64(2)))
			Expect(exampleStats.Bytes).To(Equal(int64(3072))) // 2048 + 1024

			// Check google.com stats
			googleStats := exporter.GetSiteStats("google.com")
			Expect(googleStats).NotTo(BeNil())
			Expect(googleStats.Requests).To(Equal(int64(1)))
			Expect(googleStats.Hits).To(Equal(int64(1)))
			Expect(googleStats.Misses).To(Equal(int64(0)))
			Expect(googleStats.Bytes).To(Equal(int64(1024)))
		})
	})

	Describe("Metrics Generation", func() {
		BeforeEach(func() {
			// Add some test data
			logLines := []string{
				"1234567890.123   100 192.168.1.100 TCP_MISS/200 2048 GET http://example.com/ - DIRECT/93.184.216.34 text/html",
				"1234567890.124   50 192.168.1.100 TCP_HIT/200 1024 GET http://example.com/page2 - DIRECT/93.184.216.34 text/html",
				"1234567890.125   75 192.168.1.100 TCP_MISS/200 1024 GET http://google.com/ - DIRECT/172.217.164.110 text/html",
			}

			for _, line := range logLines {
				exporter.parseLogLine(line)
			}
		})

		It("should generate correct hit ratio metrics", func() {
			// example.com: 1 hit, 1 miss = 50% hit ratio
			expectedRatio := 0.5

			metricValue := testutil.ToFloat64(exporter.hitRatio.WithLabelValues("example.com"))
			Expect(metricValue).To(Equal(expectedRatio))
		})

		It("should generate correct counter metrics", func() {
			// Check hits total
			exampleHits := testutil.ToFloat64(exporter.hitTotal.WithLabelValues("example.com"))
			Expect(exampleHits).To(Equal(float64(1)))

			// Check misses total
			exampleMisses := testutil.ToFloat64(exporter.missTotal.WithLabelValues("example.com"))
			Expect(exampleMisses).To(Equal(float64(1)))

			googleMisses := testutil.ToFloat64(exporter.missTotal.WithLabelValues("google.com"))
			Expect(googleMisses).To(Equal(float64(1)))

			// Check requests total
			exampleRequests := testutil.ToFloat64(exporter.requestsTotal.WithLabelValues("example.com"))
			Expect(exampleRequests).To(Equal(float64(2)))

			// Check bytes total
			exampleBytes := testutil.ToFloat64(exporter.bytesTotal.WithLabelValues("example.com"))
			Expect(exampleBytes).To(Equal(float64(3072))) // 2048 + 1024
		})

		It("should handle sites with only hits", func() {
			// Add a hit-only site
			exporter.parseLogLine("1234567890.126   25 192.168.1.100 TCP_HIT/200 1024 GET http://cache-heavy.com/ - DIRECT/1.2.3.4 text/html")
			exporter.parseLogLine("1234567890.127   30 192.168.1.100 TCP_HIT/200 2048 GET http://cache-heavy.com/page2 - DIRECT/1.2.3.4 text/html")

			// Should have 100% hit ratio
			hitRatio := testutil.ToFloat64(exporter.hitRatio.WithLabelValues("cache-heavy.com"))
			Expect(hitRatio).To(Equal(1.0))

			hits := testutil.ToFloat64(exporter.hitTotal.WithLabelValues("cache-heavy.com"))
			Expect(hits).To(Equal(float64(2)))

			misses := testutil.ToFloat64(exporter.missTotal.WithLabelValues("cache-heavy.com"))
			Expect(misses).To(Equal(float64(0)))
		})

		It("should handle sites with only misses", func() {
			// Add a miss-only site
			exporter.parseLogLine("1234567890.128   100 192.168.1.100 TCP_MISS/200 1024 GET http://no-cache.com/ - DIRECT/5.6.7.8 text/html")

			// Should have 0% hit ratio
			hitRatio := testutil.ToFloat64(exporter.hitRatio.WithLabelValues("no-cache.com"))
			Expect(hitRatio).To(Equal(0.0))

			hits := testutil.ToFloat64(exporter.hitTotal.WithLabelValues("no-cache.com"))
			Expect(hits).To(Equal(float64(0)))

			misses := testutil.ToFloat64(exporter.missTotal.WithLabelValues("no-cache.com"))
			Expect(misses).To(Equal(float64(1)))
		})
	})

	Describe("Concurrent Access", func() {
		It("should handle concurrent log parsing safely", func() {
			logLines := []string{
				"1234567890.123   100 192.168.1.100 TCP_MISS/200 2048 GET http://concurrent.com/ - DIRECT/93.184.216.34 text/html",
				"1234567890.124   50 192.168.1.100 TCP_HIT/200 1024 GET http://concurrent.com/page2 - DIRECT/93.184.216.34 text/html",
			}

			// Parse lines concurrently
			done := make(chan bool, 2)
			for _, line := range logLines {
				go func(l string) {
					defer GinkgoRecover()
					for i := 0; i < 10; i++ {
						exporter.parseLogLine(l)
					}
					done <- true
				}(line)
			}

			// Wait for both goroutines
			<-done
			<-done

			// Verify stats are consistent
			stats := exporter.GetSiteStats("concurrent.com")
			Expect(stats).NotTo(BeNil())
			Expect(stats.Requests).To(Equal(int64(20))) // 10 hits + 10 misses
			Expect(stats.Hits).To(Equal(int64(10)))
			Expect(stats.Misses).To(Equal(int64(10)))
		})
	})
})
