package e2e_test

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/konflux-ci/caching/tests/testhelpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	nginxAccessLogPath   = "/tmp/access.log"
	nginxStubStatusPort  = 8081
	nginxContainerName   = "nginx"
	httpMethodRegex     = `^(GET|POST|PUT|DELETE|HEAD|OPTIONS|PATCH|CONNECT|TRACE)$`
)

var _ = Describe("NGINX Logging and Metrics Tests", Label("nginx", "monitoring"), Ordered, Serial, func() {
	var (
		client       *http.Client
		nginxURL     string
		nginxPodName string
		restConfig   *rest.Config
	)

	BeforeAll(func() {
		var err error
		restConfig, err = testhelpers.GetRESTConfig()
		Expect(err).NotTo(HaveOccurred(), "Failed to get REST config")

		nexusConfig := testhelpers.NewNexusConfig()

		err = testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled:      true,
				ReplicaCount: 1,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: nexusConfig.URL,
				},
				Cache: &testhelpers.NginxCacheValues{
					AllowList: []string{"^/"},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		client = testhelpers.NewNginxClient()
		nginxURL = testhelpers.GetNginxURL()

		// Get NGINX pod name
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/component=nginx-caching",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).NotTo(BeEmpty(), "Expected at least one NGINX pod")
		nginxPodName = pods.Items[0].Name
	})

	Context("Access Log Format Validation", func() {
		It("should write logs to /tmp/access.log with detailed format", func() {
			// Generate test traffic to Nexus health endpoint
			resp, err := client.Get(nginxURL + "/service/rest/v1/status")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			// Wait for log to be written
			time.Sleep(100 * time.Millisecond)

			// Read logs from access log inside the pod
			logs, _, err := testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName,
				[]string{"cat", nginxAccessLogPath})
			Expect(err).NotTo(HaveOccurred(), "Failed to read %s", nginxAccessLogPath)
			Expect(logs).NotTo(BeEmpty(), "Expected logs in %s", nginxAccessLogPath)

			// Validate log format: METHOD\tSTATUS\tREQUEST_URI\tREQUEST_TIME\tUPSTREAM_TIME\tCACHE_STATUS
			logLines := strings.Split(strings.TrimSpace(logs), "\n")
			Expect(logLines).NotTo(BeEmpty(), "Expected at least one log line")

			lastLine := logLines[len(logLines)-1]
			fields := strings.Split(lastLine, "\t")

			By("Validating log has 6 tab-separated fields")
			Expect(fields).To(HaveLen(6), fmt.Sprintf("Expected 6 fields in log line: %s", lastLine))

			By("Validating field 1: request method (GET, POST, etc.)")
			Expect(fields[0]).To(MatchRegexp(httpMethodRegex))

			By("Validating field 2: HTTP status code (200, 404, etc.)")
			Expect(fields[1]).To(MatchRegexp(`^\d{3}$`))

			By("Validating field 3: request URI (path)")
			Expect(fields[2]).To(MatchRegexp(`^/.+`))

			By("Validating field 4: request time (float)")
			Expect(fields[3]).To(MatchRegexp(`^\d+\.\d+$`))

			By("Validating field 5: upstream response time (float or -)")
			Expect(fields[4]).To(MatchRegexp(`^(\d+\.\d+|-)$`))

			By("Validating field 6: cache status (HIT, MISS, BYPASS, etc.)")
			Expect(fields[5]).To(MatchRegexp(`^(HIT|MISS|BYPASS|EXPIRED|STALE|UPDATING|REVALIDATED|-)$`))
		})

		It("should also write logs to stdout for kubectl logs", func() {
			// Get pod logs from stdout
			logOptions := &corev1.PodLogOptions{
				Container: nginxContainerName,
				TailLines: int64Ptr(10),
			}
			req := clientset.CoreV1().Pods(namespace).GetLogs(nginxPodName, logOptions)
			podLogs, err := req.Stream(ctx)
			Expect(err).NotTo(HaveOccurred())
			defer podLogs.Close()

			buf := new(strings.Builder)
			_, err = io.Copy(buf, podLogs)
			Expect(err).NotTo(HaveOccurred())

			logs := buf.String()

			By("Verifying stdout contains access logs in detailed format")
			// Look for tab-separated log entries
			scanner := bufio.NewScanner(strings.NewReader(logs))
			foundDetailedLog := false
			for scanner.Scan() {
				line := scanner.Text()
				if strings.Count(line, "\t") == 5 { // 6 fields = 5 tabs
					fields := strings.Split(line, "\t")
					if len(fields) == 6 && isHTTPMethod(fields[0]) {
						foundDetailedLog = true
						break
					}
				}
			}
			Expect(foundDetailedLog).To(BeTrue(), "Expected to find detailed log format in stdout")
		})

		It("should handle cache HIT scenario correctly", func() {
			testPath := "/service/rest/v1/status" // Nexus health endpoint

			// First request - should be MISS
			resp1, err := client.Get(nginxURL + testPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp1.Body.Close()
			Expect(resp1.StatusCode).To(Equal(http.StatusOK))

			time.Sleep(100 * time.Millisecond)

			// Second request - should be HIT
			resp2, err := client.Get(nginxURL + testPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close()
			Expect(resp2.StatusCode).To(Equal(http.StatusOK))

			time.Sleep(100 * time.Millisecond)

			// Read logs and verify cache status
			logs, _, err := testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName,
				[]string{"tail", "-n", "5", nginxAccessLogPath})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying logs contain cache HIT entry")
			Expect(logs).To(ContainSubstring("HIT"), "Expected to find cache HIT in logs")
		})
	})

	Context("stub_status Endpoint Security", func() {
		It("should be accessible from localhost (127.0.0.1)", func() {
			// Execute curl from inside the pod to localhost
			output, _, err := testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName,
				[]string{"curl", "-s", fmt.Sprintf("http://127.0.0.1:%d/stub_status", nginxStubStatusPort)})
			Expect(err).NotTo(HaveOccurred(), "stub_status should be accessible from localhost")

			By("Validating stub_status response format")
			Expect(output).To(ContainSubstring("Active connections:"))
			Expect(output).To(ContainSubstring("server accepts handled requests"))
			Expect(output).To(MatchRegexp(`Reading: \d+ Writing: \d+ Waiting: \d+`))
		})

		It("should return 403 Forbidden from external IP", func() {
			// Try to access from pod's own IP (not 127.0.0.1)
			getPodIP := []string{"sh", "-c", "hostname -i"}
			podIP, _, err := testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName, getPodIP)
			Expect(err).NotTo(HaveOccurred())
			podIP = strings.TrimSpace(podIP)

			curlCmd := []string{"sh", "-c", fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' http://%s:%d/stub_status", podIP, nginxStubStatusPort)}
			statusCode, _, err := testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName, curlCmd)
			Expect(err).NotTo(HaveOccurred())

			statusCode = strings.TrimSpace(statusCode)
			By("Verifying access is denied (403 Forbidden)")
			Expect(statusCode).To(Equal("403"), "Expected 403 Forbidden from non-localhost access")
		})

		It("should expose port 8081 in container spec", func() {
			pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, nginxPodName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			nginxContainer, err := testhelpers.FindContainerByName(pod, "nginx")
			Expect(err).NotTo(HaveOccurred())

			foundMetricsPort := false
			for _, port := range nginxContainer.Ports {
				if port.ContainerPort == nginxStubStatusPort && port.Name == "metrics" {
					foundMetricsPort = true
					break
				}
			}
			Expect(foundMetricsPort).To(BeTrue(), "Expected port %d (metrics) to be exposed", nginxStubStatusPort)
		})
	})

	Context("Readonly Filesystem Compatibility", func() {
		It("should run with readOnlyRootFilesystem=true", func() {
			pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, nginxPodName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			nginxContainer, err := testhelpers.FindContainerByName(pod, "nginx")
			Expect(err).NotTo(HaveOccurred())
			Expect(nginxContainer.SecurityContext).NotTo(BeNil())

			By("Verifying readOnlyRootFilesystem is enabled")
			Expect(nginxContainer.SecurityContext.ReadOnlyRootFilesystem).NotTo(BeNil())
			Expect(*nginxContainer.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue())
		})

		It("should successfully write logs to /tmp with readonly filesystem", func() {
			// Generate traffic to Nexus
			_, err := client.Get(nginxURL + "/service/rest/v1/status")
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(100 * time.Millisecond)

			// Verify logs were written to /tmp
			_, _, err = testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName,
				[]string{"test", "-f", nginxAccessLogPath})
			Expect(err).NotTo(HaveOccurred(), "%s should exist", nginxAccessLogPath)

			// Verify file is readable
			content, _, err := testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, nginxPodName, nginxContainerName,
				[]string{"cat", nginxAccessLogPath})
			Expect(err).NotTo(HaveOccurred())
			Expect(content).NotTo(BeEmpty())
		})
	})
})

// Helper functions

func int64Ptr(i int64) *int64 {
	return &i
}

func isHTTPMethod(s string) bool {
	return regexp.MustCompile(httpMethodRegex).MatchString(s)
}
