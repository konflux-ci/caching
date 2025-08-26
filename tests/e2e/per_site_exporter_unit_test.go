package e2e_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var _ = Describe("Per-Site Exporter Unit Checks", func() {
	It("returns a valid Prometheus content-type from the metrics handler", func() {
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
