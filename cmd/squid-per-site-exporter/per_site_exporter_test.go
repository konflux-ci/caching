package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

var _ = Describe("hostname allow-list configuration", func() {
	DescribeTable("applies safe defaults and flag precedence over the environment",
		func(env string, args []string, expectedLabels []string) {
			GinkgoT().Setenv("METRICS_ALLOWED_HOSTNAMES", env)
			flags := flag.NewFlagSet("per-site-exporter", flag.ContinueOnError)
			allowedHostnames := registerAllowedHostnamesFlag(flags)
			Expect(flags.Parse(args)).To(Succeed())
			exporter, err := NewExporter(strings.Split(*allowedHostnames, ",")...)
			Expect(err).NotTo(HaveOccurred())

			labels := []string{
				exporter.hostnameLabel("quay.io"),
				exporter.hostnameLabel("registry.example.com"),
				exporter.hostnameLabel("unlisted.attacker.test"),
			}
			Expect(labels).To(Equal(expectedLabels))
		},
		Entry("empty configuration aggregates all hosts", "", []string(nil), []string{"other", "other", "other"}),
		Entry("normalizes hosts from the environment", " Quay.IO.,registry.example.com ", []string(nil),
			[]string{"quay.io", "registry.example.com", "other"}),
		Entry("accepts a flag without an environment value", "", []string{"-metrics.allowed-hostnames=quay.io"},
			[]string{"quay.io", "other", "other"}),
		Entry("flag replaces the environment list", "quay.io", []string{"-metrics.allowed-hostnames=registry.example.com"},
			[]string{"other", "registry.example.com", "other"}),
		Entry("empty flag overrides a populated environment list", "quay.io", []string{"-metrics.allowed-hostnames="},
			[]string{"other", "other", "other"}),
	)

	DescribeTable("rejects entries that cannot be tracked independently",
		func(host, message string) {
			exporter, err := NewExporter("quay.io", host)
			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(exporter).To(BeNil())
		},
		Entry("hostname with port", "quay.io:443", "without a port"),
		Entry("hexadecimal hostname with port", "cafe:443", "without a port"),
		Entry("IPv4 with port", "192.0.2.1:8080", "without a port"),
		Entry("IPv6 with port", "[2001:db8::1]:443", "without a port"),
		Entry("bracketed IPv6", "[2001:db8::1]", "unbracketed IP"),
		Entry("URL", "https://quay.io", "exact hostname"),
		Entry("wildcard", "*.example.com", "exact hostname"),
		Entry("quote", `registry".example.com`, "exact hostname"),
		Entry("backslash", `registry\.example.com`, "exact hostname"),
		Entry("dollar sign", "registry$.example.com", "exact hostname"),
		Entry("braces", "registry{cache}.example.com", "exact hostname"),
		Entry("empty DNS label", "registry..example.com", "exact hostname"),
		Entry("DNS root without a hostname", ".", "exact hostname"),
		Entry("reserved label", "other", "reserved"),
		Entry("reserved label after normalization", " OTHER. ", "reserved"),
	)
})

var _ = Describe("parseLogLine", func() {
	BeforeEach(func() {
		squidHitRatio.Reset()
		squidHitTotal.Reset()
		squidMissTotal.Reset()
		squidRequestsTotal.Reset()
		squidBytesTotal.Reset()
		squidResponseTime.Reset()
	})

	It("parses realistic squid access.log lines and classifies hits/misses", func() {
		exporter, err := NewExporter(
			"example.com", "assets.cdn.com", "notfound.example.com", "post.example.com", "patch.example.com")
		Expect(err).NotTo(HaveOccurred())

		lines := []string{
			// HIT for example.com
			"1732700000 120 10.0.0.1 TCP_HIT/200 1234 GET http://example.com/index.html - DIRECT/- text/html",
			// MISS for example.com
			"1732700050 90 10.0.0.2 TCP_MISS/200 200 GET http://example.com/other - DIRECT/- text/html",
			// MEM_HIT for CDN asset
			"1732700100 50 10.0.0.3 MEM_HIT/200 512 GET http://assets.cdn.com/img.png - DIRECT/- image/png",
			// CONNECT (should be ignored by method filter)
			// Note: such requests don't include the protocol in the URL
			"1732700200 10 10.0.0.4 NONE_NONE/200 0 CONNECT secure.example.com:443 - DIRECT/- -",
			// HEAD allowed; counts as MISS
			"1732700300 5 10.0.0.5 TCP_MISS/404 0 HEAD http://notfound.example.com/ - DIRECT/- text/plain",
			// POST allowed; counts as HIT
			"1732700350 200 10.0.0.6 TCP_HIT/200 2048 POST http://post.example.com/ - DIRECT/- application/json",
			// Invalid URL (ignored)
			"1732700400 5 10.0.0.7 TCP_HIT/200 10 GET ://bad - DIRECT/- -",
			// PATCH allowed; counts as HIT
			"1732700450 5 10.0.0.8 TCP_HIT/200 2048 PATCH http://patch.example.com/ - DIRECT/- text/plain",
			// PUT (should be ignored by method filter)
			"1732700500 5 10.0.0.9 TCP_MISS/200 2048 PUT http://put.example.com/ - DIRECT/- text/plain",
		}

		for _, l := range lines {
			exporter.parseLogLine(l)
		}

		// helper to read counter value via the main package function
		get := func(vec *prometheus.CounterVec, host string) float64 {
			v, err := getCounterValue(vec, host)
			Expect(err).NotTo(HaveOccurred())
			return v
		}

		// example.com: 1 HIT + 1 MISS, 2 requests, bytes 1234+200
		Expect(get(squidRequestsTotal, "example.com")).To(Equal(2.0))
		Expect(get(squidHitTotal, "example.com")).To(Equal(1.0))
		Expect(get(squidMissTotal, "example.com")).To(Equal(1.0))
		Expect(get(squidBytesTotal, "example.com")).To(Equal(1434.0))

		// assets.cdn.com: 1 MEM_HIT
		Expect(get(squidRequestsTotal, "assets.cdn.com")).To(Equal(1.0))
		Expect(get(squidHitTotal, "assets.cdn.com")).To(Equal(1.0))
		Expect(get(squidMissTotal, "assets.cdn.com")).To(Equal(0.0))
		Expect(get(squidBytesTotal, "assets.cdn.com")).To(Equal(512.0))

		// notfound.example.com: 1 MISS via HEAD
		Expect(get(squidRequestsTotal, "notfound.example.com")).To(Equal(1.0))
		Expect(get(squidHitTotal, "notfound.example.com")).To(Equal(0.0))
		Expect(get(squidMissTotal, "notfound.example.com")).To(Equal(1.0))
		Expect(get(squidBytesTotal, "notfound.example.com")).To(Equal(0.0))

		// post.example.com: 1 HIT via POST
		Expect(get(squidRequestsTotal, "post.example.com")).To(Equal(1.0))
		Expect(get(squidHitTotal, "post.example.com")).To(Equal(1.0))
		Expect(get(squidMissTotal, "post.example.com")).To(Equal(0.0))
		Expect(get(squidBytesTotal, "post.example.com")).To(Equal(2048.0))

		// patch.example.com: 1 HIT via PATCH
		Expect(get(squidRequestsTotal, "patch.example.com")).To(Equal(1.0))
		Expect(get(squidHitTotal, "patch.example.com")).To(Equal(1.0))
		Expect(get(squidMissTotal, "patch.example.com")).To(Equal(0.0))
		Expect(get(squidBytesTotal, "patch.example.com")).To(Equal(2048.0))

		// put.example.com: uncacheable (0 request metrics)
		Expect(get(squidRequestsTotal, "put.example.com")).To(Equal(0.0))
		Expect(get(squidHitTotal, "put.example.com")).To(Equal(0.0))
		Expect(get(squidMissTotal, "put.example.com")).To(Equal(0.0))
		Expect(get(squidBytesTotal, "put.example.com")).To(Equal(0.0))

		// secure.example.com: uncacheable (0 request metrics)
		Expect(get(squidRequestsTotal, "secure.example.com")).To(Equal(0.0))
		Expect(get(squidHitTotal, "secure.example.com")).To(Equal(0.0))
		Expect(get(squidMissTotal, "secure.example.com")).To(Equal(0.0))
		Expect(get(squidBytesTotal, "secure.example.com")).To(Equal(0.0))

		// Malformed line (<7 fields) should log and be ignored
		var buf bytes.Buffer
		old := log.Writer()
		log.SetOutput(&buf)
		exporter.parseFunc("one two three four five six")
		log.SetOutput(old)
		Expect(buf.String()).To(ContainSubstring("Malformed access log entry"))
	})

	It("normalizes configured hosts and URLs while matching only exact hosts", func() {
		exporter, err := NewExporter(" EXAMPLE.COM. ", "example.com", "", " ")
		Expect(err).NotTo(HaveOccurred())
		hosts := []string{
			"EXAMPLE.COM.:8080", "Example.Com", "example.com",
			"sub.example.com", "example.com.attacker.test", "other",
		}
		for _, host := range hosts {
			exporter.parseLogLine("1732700000 100 10.0.0.1 TCP_HIT/200 10 GET http://" + host + "/ - DIRECT/- text/plain")
		}

		for _, family := range gatherSiteMetrics() {
			Expect(family.GetMetric()).To(HaveLen(2), family.GetName())
			hostnames := make([]string, 0, len(family.GetMetric()))
			for _, metric := range family.GetMetric() {
				Expect(metric.GetLabel()).To(HaveLen(1))
				hostnames = append(hostnames, metric.GetLabel()[0].GetValue())
			}
			Expect(hostnames).To(ConsistOf("example.com", "other"), family.GetName())
		}
		for _, host := range []string{"example.com", "other"} {
			requests, err := getCounterValue(squidRequestsTotal, host)
			Expect(err).NotTo(HaveOccurred())
			Expect(requests).To(Equal(3.0))
		}
	})

	DescribeTable("bounds all metric families when requests use thousands of distinct hosts",
		func(allowedHostnames []string, expectedHostnames []string) {
			exporter, err := NewExporter(allowedHostnames...)
			Expect(err).NotTo(HaveOccurred())
			if len(allowedHostnames) > 0 {
				exporter.parseLogLine("1732700000 100 10.0.0.1 TCP_HIT/200 10 GET http://example.com/ - DIRECT/- text/plain")
			}
			const requests = 5000
			for i := range requests {
				status := "TCP_MISS/200"
				if i%2 == 0 {
					status = "TCP_HIT/200"
				}
				exporter.parseLogLine(fmt.Sprintf(
					"1732700000 100 10.0.0.1 %s 10 GET http://host-%d.attacker.test/ - DIRECT/- text/plain", status, i))
			}

			for _, family := range gatherSiteMetrics() {
				hostnames := make([]string, 0, len(family.GetMetric()))
				for _, metric := range family.GetMetric() {
					Expect(metric.GetLabel()).To(HaveLen(1))
					hostname := metric.GetLabel()[0].GetValue()
					hostnames = append(hostnames, hostname)
					if hostname == "other" {
						expectOtherMetric(family.GetName(), metric, requests)
					}
				}
				Expect(hostnames).To(ConsistOf(expectedHostnames), family.GetName())
			}
		},
		Entry("with an empty allow-list", []string(nil), []string{"other"}),
		Entry("with an explicit allow-list", []string{"example.com"}, []string{"example.com", "other"}),
	)

	DescribeTable("tracks configured IP literals from request URLs with ports",
		func(host, requestURL, expectedLabel string) {
			exporter, err := NewExporter(host)
			Expect(err).NotTo(HaveOccurred())
			exporter.parseLogLine("1732700000 100 10.0.0.1 TCP_HIT/200 10 GET " + requestURL + " - DIRECT/- text/plain")
			for _, family := range gatherSiteMetrics() {
				Expect(family.GetMetric()).To(HaveLen(1))
				Expect(family.GetMetric()[0].GetLabel()[0].GetValue()).To(Equal(expectedLabel))
			}
		},
		Entry("IPv4", "192.0.2.1", "http://192.0.2.1:8080/", "192.0.2.1"),
		Entry("expanded IPv6 in configuration", "2001:0DB8:0:0:0:0:0:1", "http://[2001:db8::1]:8080/", "2001:db8::1"),
		Entry("expanded IPv6 in the request", "2001:db8::1", "http://[2001:0DB8:0:0:0:0:0:1]:8080/", "2001:db8::1"),
		Entry("IPv4-mapped IPv6", "::ffff:192.0.2.1", "http://[::ffff:c000:201]:8080/", "::ffff:192.0.2.1"),
	)
})

// Gather without calling WithLabelValues, which would create missing series.
func gatherSiteMetrics() []*dto.MetricFamily {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		squidHitRatio, squidHitTotal, squidMissTotal, squidRequestsTotal, squidBytesTotal, squidResponseTime)
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	Expect(families).To(HaveLen(6))
	return families
}

func expectOtherMetric(name string, metric *dto.Metric, requests uint64) {
	switch name {
	case "squid_site_requests_total":
		Expect(metric.GetCounter().GetValue()).To(Equal(float64(requests)))
	case "squid_site_hits_total", "squid_site_misses_total":
		Expect(metric.GetCounter().GetValue()).To(Equal(float64(requests) / 2))
	case "squid_site_bytes_total":
		Expect(metric.GetCounter().GetValue()).To(Equal(float64(requests) * 10))
	case "squid_site_hit_ratio":
		Expect(metric.GetGauge().GetValue()).To(Equal(0.5))
	case "squid_site_response_time_seconds":
		Expect(metric.GetHistogram().GetSampleCount()).To(Equal(requests))
		Expect(metric.GetHistogram().GetSampleSum()).To(BeNumerically("~", float64(requests)*0.1, 0.000001))
		for _, bucket := range metric.GetHistogram().GetBucket() {
			if bucket.GetUpperBound() < 0.1 {
				Expect(bucket.GetCumulativeCount()).To(BeZero())
			} else {
				Expect(bucket.GetCumulativeCount()).To(Equal(requests))
			}
		}
	default:
		Fail("Unexpected per-site metric family: " + name)
	}
}

var _ = Describe("metrics handler", func() {
	It("returns a valid Prometheus content-type from the handler", func() {
		h := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{EnableOpenMetrics: false})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		resp := httptest.NewRecorder()

		h.ServeHTTP(resp, req)

		Expect(resp.Code).To(Equal(http.StatusOK))
		ct := resp.Header().Get("Content-Type")
		Expect([]string{
			"text/plain; version=0.0.4; charset=utf-8",
			"text/plain; version=0.0.4; charset=utf-8; escaping=values",
			"text/plain; version=0.0.4; charset=utf-8; escaping=underscores",
		}).To(ContainElement(ct))
	})
})

var _ = Describe("HTTP handlers", func() {
	It("serves the index page", func() {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		indexPageHandler(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK))
		Expect(rr.Body.String()).To(ContainSubstring("Squid Per-Site Exporter"))
		Expect(rr.Body.String()).To(ContainSubstring("/metrics"))
	})

	It("reports healthy when the squid address is reachable", func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = ln.Close() }()

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		healthCheckHandler(ln.Addr().String(), time.Second)(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK))
		Expect(rr.Body.String()).To(Equal("OK"))
	})

	It("reports unhealthy when the squid address is unreachable", func() {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		healthCheckHandler("127.0.0.1:1", 100*time.Millisecond)(rr, req)
		Expect(rr.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(rr.Body.String()).To(ContainSubstring("squid unreachable"))
	})
})

var _ = Describe("readFromStdin", func() {
	It("invokes the injected parseFunc with raw lines from stdin", func() {
		exp, err := NewExporter()
		Expect(err).NotTo(HaveOccurred())

		ch := make(chan string, 1)
		exp.parseFunc = func(s string) { ch <- s }

		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		oldStdin := os.Stdin
		os.Stdin = r
		defer func() { os.Stdin = oldStdin; _ = r.Close() }()

		go exp.readFromStdin()

		_, err = w.WriteString("sample-stdin-line\n")
		Expect(err).NotTo(HaveOccurred())
		_ = w.Close()

		select {
		case got := <-ch:
			Expect(got).To(Equal("sample-stdin-line"))
		case <-time.After(2 * time.Second):
			Fail("timeout waiting for parseFunc to be called")
		}
	})
})
