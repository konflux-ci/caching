package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var _ = Describe("parseLogLine", func() {
	It("parses realistic squid access.log lines and classifies hits/misses", func() {
		exporter := NewExporter()

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
})

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

var _ = Describe("readFromStdin", func() {
	It("invokes the injected parseFunc with raw lines from stdin", func() {
		exp := NewExporter()

		ch := make(chan string, 1)
		exp.parseFunc = func(s string) { ch <- s }

		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		oldStdin := os.Stdin
		os.Stdin = r
		defer func() { os.Stdin = oldStdin; r.Close() }()

		go exp.readFromStdin()

		_, err = w.WriteString("sample-stdin-line\n")
		Expect(err).NotTo(HaveOccurred())
		w.Close()

		select {
		case got := <-ch:
			Expect(got).To(Equal("sample-stdin-line"))
		case <-time.After(2 * time.Second):
			Fail("timeout waiting for parseFunc to be called")
		}
	})
})
