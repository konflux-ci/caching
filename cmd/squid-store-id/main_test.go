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
		Entry("quay.io wrong host", "https://badcdn.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		Entry("quay.io hash too short", "https://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456789"),
		Entry("quay.io wrong protocol (http)", "http://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		Entry("quay.io wrong protocol (ftp)", "ftp://cdn01.quay.io/repository/sha256/ab/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		Entry("docker hub r2 wrong domain", "https://docker-images-wrong.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data"),
		Entry("docker hub r2 hash too short", "https://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b/data"),
		Entry("docker hub r2 wrong protocol", "http://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data"),
		Entry("docker hub cloudflare cdn hash too short", "https://production.cloudflare.docker.com/registry-v2/docker/registry/v2/blobs/sha256/24/24c63b8dcb66721062f32b893ef1027404afddd62aade87f3f39a3a6e70a74/data"),
		Entry("docker hub cloudflare cdn wrong protocol", "http://production.cloudflare.docker.com/registry-v2/docker/registry/v2/blobs/sha256/24/24c63b8dcb66721062f32b893ef1027404afddd62aade87f3f39a3a6e70a74d0/data"),
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

	When("given Docker Hub R2 CDN URLs", func() {
		const testDockerHubURL = "https://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=f1baa2dd9b876aeb89efebbfc9e5d5f4%2F20251202%2Fauto%2Fs3%2Faws4_request&X-Amz-Date=20251202T200117Z&X-Amz-Expires=1200&X-Amz-SignedHeaders=host&X-Amz-Signature=daa6b76aa13b45e9d3101f93651237f375fb7c1aac859dfaf6ef1208cdc1d9c3"

		It("should return normalized URL (without query params) when HTTP request succeeds", func() {
			mockClient := &MockHTTPClient{
				StatusCode: http.StatusOK,
			}

			expectedURL := "https://docker-images-prod.6aa30f8b08e16409b46e0173d6de2f56.r2.cloudflarestorage.com/registry-v2/docker/registry/v2/blobs/sha256/b5/b58899f069c47216f6002a6850143dc6fae0d35eb8b0df9300bbe6327b9c2171/data"
			Expect(normalizeStoreID(mockClient, testDockerHubURL)).To(Equal(expectedURL))
		})

		It("should handle non-200 HTTP responses by returning original URL", func() {
			mockClient := &MockHTTPClient{
				StatusCode: http.StatusUnauthorized,
			}

			Expect(normalizeStoreID(mockClient, testDockerHubURL)).To(Equal(testDockerHubURL))
		})

		It("should handle HTTP error responses by returning original URL", func() {
			mockClient := &MockHTTPClient{
				ShouldError: true,
				Error: &url.Error{
					Op:  "Get",
					URL: testDockerHubURL,
					Err: http.ErrServerClosed,
				},
			}

			Expect(normalizeStoreID(mockClient, testDockerHubURL)).To(Equal(testDockerHubURL))
		})
	})

	When("given Docker Hub Cloudflare CDN URLs (production.cloudflare.docker.com)", func() {
		const testCloudflareCDNURL = "https://production.cloudflare.docker.com/registry-v2/docker/registry/v2/blobs/sha256/24/24c63b8dcb66721062f32b893ef1027404afddd62aade87f3f39a3a6e70a74d0/data?verify=1717211225-SoFsY9MpCnMY8xiypN2ii7WhLsA%3D"

		It("should return normalized URL (without query params) when HTTP request succeeds", func() {
			mockClient := &MockHTTPClient{
				StatusCode: http.StatusOK,
			}

			expectedURL := "https://production.cloudflare.docker.com/registry-v2/docker/registry/v2/blobs/sha256/24/24c63b8dcb66721062f32b893ef1027404afddd62aade87f3f39a3a6e70a74d0/data"
			Expect(normalizeStoreID(mockClient, testCloudflareCDNURL)).To(Equal(expectedURL))
		})

		It("should handle non-200 HTTP responses by returning original URL", func() {
			mockClient := &MockHTTPClient{
				StatusCode: http.StatusUnauthorized,
			}

			Expect(normalizeStoreID(mockClient, testCloudflareCDNURL)).To(Equal(testCloudflareCDNURL))
		})

		It("should handle HTTP error responses by returning original URL", func() {
			mockClient := &MockHTTPClient{
				ShouldError: true,
				Error: &url.Error{
					Op:  "Get",
					URL: testCloudflareCDNURL,
					Err: http.ErrServerClosed,
				},
			}

			Expect(normalizeStoreID(mockClient, testCloudflareCDNURL)).To(Equal(testCloudflareCDNURL))
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
