package main

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isChannelID", func() {
	Context("with valid numeric strings", func() {
		It("should return true for positive integers", func() {
			Expect(isChannelID("0")).To(BeTrue())
			Expect(isChannelID("1")).To(BeTrue())
			Expect(isChannelID("123")).To(BeTrue())
			Expect(isChannelID("999999")).To(BeTrue())
		})

		It("should return false for other types of values", func() {
			Expect(isChannelID("-1")).To(BeFalse())
			Expect(isChannelID("")).To(BeFalse())
			Expect(isChannelID(" ")).To(BeFalse())
			Expect(isChannelID("\t")).To(BeFalse())
			Expect(isChannelID("\n")).To(BeFalse())
			Expect(isChannelID("abc")).To(BeFalse())
			Expect(isChannelID("123abc")).To(BeFalse())
			Expect(isChannelID("abc123")).To(BeFalse())
			Expect(isChannelID("12.34")).To(BeFalse())
		})
	})
})

var _ = Describe("parseLine", func() {
	var normalizeFunc = func(client HTTPClient, url string) string { return url }
	var normalizeFuncDifferent = func(client HTTPClient, url string) string { return "normalized-" + url }

	When("given a line with a channel-ID", func() {
		Context("and the normalized store-id is different from the original URL", func() {
			It("should return <CHANNEL-ID> OK store-id=<NORMALIZED-STORE-ID>", func() {
				result := parseLine("123 http://example.com/path", normalizeFuncDifferent)
				Expect(result).To(Equal("123 OK store-id=normalized-http://example.com/path"))
			})
		})

		Context("and the normalized store-id is the same as the original URL", func() {
			It("should return <CHANNEL-ID> OK", func() {
				result := parseLine("123 http://example.com/path", normalizeFunc)
				Expect(result).To(Equal("123 OK"))
			})
		})
	})

	When("given a line with no channel-ID", func() {
		Context("and the normalized store-id is different from the original URL", func() {
			It("should return OK store-id=<NORMALIZED-STORE-ID>", func() {
				result := parseLine("http://example.com/path", normalizeFuncDifferent)
				Expect(result).To(Equal("OK store-id=normalized-http://example.com/path"))
			})
		})

		Context("and the normalized store-id is the same as the original URL", func() {
			It("should return OK", func() {
				result := parseLine("http://example.com/path", normalizeFunc)
				Expect(result).To(Equal("OK"))
			})
		})
	})
})

var _ = Describe("normalizeStoreID", func() {
	DescribeTable("when given non-content addressable CDN URLs, should return the original URL unchanged",
		func(url string) {
			mockClient := &MockHTTPClient{}
			result := normalizeStoreID(mockClient, url)
			Expect(result).To(Equal(url), "URL should be unchanged")
		},
		Entry("wrong host", "https://badcdn.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		Entry("hash too short", "https://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456789"),
		Entry("wrong protocol (http)", "http://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		Entry("wrong protocol (ftp)", "ftp://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
	)

	When("given content addressable CDN URLs", func() {
		const testURL = "https://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890?token=abc123"

		It("should return normalized URL (without query params) when HTTP request succeeds", func() {
			mockClient := &MockHTTPClient{
				StatusCode: http.StatusOK,
			}

			expectedURL := "https://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
			Expect(normalizeStoreID(mockClient, testURL)).To(Equal(expectedURL))
		})

		It("should handle non-200 HTTP responses by returning original URL", func() {
			mockClient := &MockHTTPClient{
				StatusCode: http.StatusUnauthorized,
			}

			Expect(normalizeStoreID(mockClient, testURL)).To(Equal(testURL))
		})

		It("should handle HTTP error responses by returning original URL", func() {
			mockClient := &MockHTTPClient{
				ShouldError: true,
				Error: &url.Error{
					Op:  "Get",
					URL: testURL,
					Err: http.ErrServerClosed,
				},
			}

			Expect(normalizeStoreID(mockClient, testURL)).To(Equal(testURL))
		})
	})
})

var _ = Describe("processInput", func() {
	var normalizeFuncDifferent = func(client HTTPClient, url string) string { return "normalized-" + url }

	It("processes multiple lines concurrently", func() {
		in := strings.NewReader(
			"1 http://example.com/a\n" +
				"2 http://example.com/b\n" +
				"http://example.com/c\n" +
				"4 http://example.com/d\n" +
				"http://example.com/e\n" +
				"6 http://example.com/f\n",
		)
		out := &MockWriter{}

		err := processInput(in, out, normalizeFuncDifferent)
		Expect(err).To(BeNil())

		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		Expect(lines).To(ConsistOf(
			"1 OK store-id=normalized-http://example.com/a",
			"2 OK store-id=normalized-http://example.com/b",
			"OK store-id=normalized-http://example.com/c",
			"4 OK store-id=normalized-http://example.com/d",
			"OK store-id=normalized-http://example.com/e",
			"6 OK store-id=normalized-http://example.com/f",
		))
	})

	It("propagates scanner read errors", func() {
		in := MockErrorReader{err: io.ErrUnexpectedEOF}
		out := &MockWriter{}

		err := processInput(in, out, normalizeFuncDifferent)
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
	})
})

// MockHTTPClient implements HTTPClient interface for testing
type MockHTTPClient struct {
	StatusCode  int
	ShouldError bool
	Error       error
}

func (m *MockHTTPClient) Get(url string) (*http.Response, error) {
	if m.ShouldError {
		return nil, m.Error
	}

	// Create a mock response
	resp := &http.Response{
		StatusCode: m.StatusCode,
		Body:       io.NopCloser(strings.NewReader("")), // Empty body
		Header:     make(http.Header),
	}

	return resp, nil
}

// MockWriter implements io.Writer for testing
type MockWriter struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (m *MockWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}

func (m *MockWriter) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

// MockErrorReader implements io.Reader for testing and allows for injection of an error
type MockErrorReader struct {
	err error
}

func (e MockErrorReader) Read(p []byte) (int, error) {
	return 0, e.err
}
