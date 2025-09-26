package main

import (
	"bytes"
	"log"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/intra-sh/icap"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("reqmodHandler", func() {
	var mockWriter *MockResponseWriter

	BeforeEach(func() {
		mockWriter = &MockResponseWriter{
			HeaderMap: make(http.Header),
		}
	})

	When("handling OPTIONS requests", func() {
		It("should return 200 with proper headers", func() {
			mockRequest := &icap.Request{
				Method: "OPTIONS",
				Header: make(textproto.MIMEHeader),
			}

			reqmodHandler(mockWriter, mockRequest)

			// Check headers were set correctly
			Expect(mockWriter.Header().Get("ISTag")).To(Equal("\"SQUID-ICAP-REQMOD\""))
			Expect(mockWriter.Header().Get("Service")).To(Equal("Squid ICAP REQMOD"))
			Expect(mockWriter.Header().Get("Methods")).To(Equal("REQMOD"))
			Expect(mockWriter.Header().Get("Allow")).To(Equal("204"))
			Expect(mockWriter.Header().Get("Preview")).To(Equal("0"))
			Expect(mockWriter.StatusCode).To(Equal(200))
		})
	})

	When("handling REQMOD requests", func() {
		Context("with no encapsulated HTTP request", func() {
			It("should return 200 OK", func() {
				mockRequest := &icap.Request{
					Method:  "REQMOD",
					Header:  make(textproto.MIMEHeader),
					Request: nil, // No HTTP request
				}

				reqmodHandler(mockWriter, mockRequest)

				Expect(mockWriter.StatusCode).To(Equal(200))
				Expect(mockWriter.Header().Get("ISTag")).To(Equal("\"SQUID-ICAP-REQMOD\""))
				Expect(mockWriter.Header().Get("Service")).To(Equal("Squid ICAP REQMOD"))
			})
		})

		Context("with a CDN URL", func() {
			It("should remove Authorization header and return 200", func() {
				httpReq, _ := http.NewRequest("GET", "https://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", nil)
				httpReq.Header.Set("Authorization", "Bearer token123")
				httpReq.Header.Set("User-Agent", "test-agent")

				mockRequest := &icap.Request{
					Method:  "REQMOD",
					Header:  make(textproto.MIMEHeader),
					Request: httpReq,
				}

				reqmodHandler(mockWriter, mockRequest)

				Expect(mockWriter.StatusCode).To(Equal(200))
				Expect(httpReq.Header.Get("Authorization")).To(BeEmpty())
				Expect(httpReq.Header.Get("User-Agent")).To(Equal("test-agent"))
			})
		})

		Context("with non-CDN URLs", func() {
			Context("when client allows 204 responses", func() {
				It("should return 204", func() {
					httpReq, _ := http.NewRequest("GET", "https://example.com/some/path", nil)
					httpReq.Header.Set("Authorization", "Bearer token123")

					mockRequest := &icap.Request{
						Method:  "REQMOD",
						Header:  make(textproto.MIMEHeader),
						Request: httpReq,
					}
					// Simulate the Allow header being set by the client
					mockRequest.Header.Set("Allow", "204")

					reqmodHandler(mockWriter, mockRequest)

					Expect(mockWriter.StatusCode).To(Equal(204))
					Expect(httpReq.Header.Get("Authorization")).To(Equal("Bearer token123"))
				})
			})

			Context("when client does not allow 204 responses", func() {
				It("should return 200", func() {
					httpReq, _ := http.NewRequest("GET", "https://example.com/some/path", nil)
					httpReq.Header.Set("Authorization", "Bearer token123")

					mockRequest := &icap.Request{
						Method:  "REQMOD",
						Header:  make(textproto.MIMEHeader),
						Request: httpReq,
					}

					reqmodHandler(mockWriter, mockRequest)

					Expect(mockWriter.StatusCode).To(Equal(200))
					Expect(httpReq.Header.Get("Authorization")).To(Equal("Bearer token123"))
				})
			})
		})
	})

	When("handling unsupported methods", func() {
		It("should return 405", func() {
			mockRequest := &icap.Request{
				Method: "UNSUPPORTED",
				Header: make(textproto.MIMEHeader),
			}

			reqmodHandler(mockWriter, mockRequest)

			Expect(mockWriter.StatusCode).To(Equal(405))
		})
	})
})

var _ = Describe("writeHeaderAndLog", func() {
	var (
		logOutput   *bytes.Buffer
		mockWriter  *MockResponseWriter
		mockRequest *icap.Request
	)

	BeforeEach(func() {
		// Capture log output
		logOutput = &bytes.Buffer{}
		log.SetOutput(logOutput)

		// Create mock objects
		mockWriter = &MockResponseWriter{
			HeaderMap: make(http.Header),
		}
		mockRequest = &icap.Request{
			Method: "REQMOD",
		}
	})

	When("request has no HTTP request", func() {
		It("should log method and status code only", func() {
			writeHeaderAndLog(mockWriter, mockRequest, 200)
			Expect(strings.TrimSpace(logOutput.String())).To(HaveSuffix("REQMOD 200"))
		})

		It("should call WriteHeader with nil HTTP request", func() {
			writeHeaderAndLog(mockWriter, mockRequest, 405)
			Expect(mockWriter.StatusCode).To(Equal(405))
			Expect(mockWriter.HttpMessage).To(BeNil())
			Expect(mockWriter.HasBody).To(BeFalse())
		})
	})

	When("request has HTTP request", func() {
		var err error

		It("should log method, status code, and redacted URL", func() {
			mockRequest.Request, err = http.NewRequest("GET", "https://user:password@example.com/path?token=secret", nil)
			Expect(err).ToNot(HaveOccurred())
			writeHeaderAndLog(mockWriter, mockRequest, 200)
			Expect(strings.TrimSpace(logOutput.String())).To(HaveSuffix("REQMOD 200 https://user:xxxxx@example.com/path"))
		})

		It("should call WriteHeader with HTTP request for 200 status", func() {
			mockRequest.Request, err = http.NewRequest("GET", "https://example.com/path", nil)
			Expect(err).ToNot(HaveOccurred())
			writeHeaderAndLog(mockWriter, mockRequest, 200)
			Expect(mockWriter.StatusCode).To(Equal(200))
			Expect(mockWriter.HttpMessage).To(Equal(mockRequest.Request))
			Expect(mockWriter.HasBody).To(BeFalse())
		})
	})
})

// MockResponseWriter implements icap.ResponseWriter for testing
type MockResponseWriter struct {
	HeaderMap   http.Header
	StatusCode  int
	HttpMessage interface{}
	HasBody     bool
}

func (m *MockResponseWriter) Header() http.Header {
	return m.HeaderMap
}

func (m *MockResponseWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (m *MockResponseWriter) WriteRaw(p string) {
	// No-op for testing
}

func (m *MockResponseWriter) WriteHeader(code int, httpMessage interface{}, hasBody bool) {
	m.StatusCode = code
	m.HttpMessage = httpMessage
	m.HasBody = hasBody
}
