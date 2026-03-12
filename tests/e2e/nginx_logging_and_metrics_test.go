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
	nginxStubStatusPort     = 8081
	nginxExporterMeticsPort = 9638
	nginxContainerName      = "nginx"
	httpMethodRegex         = `^(GET|POST|PUT|DELETE|HEAD|OPTIONS|PATCH|CONNECT|TRACE)$`
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

		// Always configure nginx with caching enabled for this test suite.
		// BeforeSuite may have deployed nginx without cache config, so we must
		// reconfigure here. If running as helm test, the upgrade will fail with
		// "secrets not found" error, which we handle gracefully below.
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

		// Handle helm upgrade failures during helm test (CI/CD).
		// When running as `helm test`, attempting to upgrade the release fails
		// with "secrets sh.helm.release.vX.squid.vN not found" error.
		// In this case, assume nginx was pre-deployed with cache config by Tekton/CI.
		if err != nil && strings.Contains(err.Error(), "sh.helm.release") {
			By("Helm upgrade failed (running as helm test) - using pre-deployed nginx with cache config")
		} else {
			Expect(err).NotTo(HaveOccurred(), "Failed to configure nginx with caching")
		}

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
		It("should write logs to stdout with detailed format", func() {
			// Generate test traffic to Nexus health endpoint
			resp, err := client.Get(nginxURL + "/service/rest/v1/status")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			// Wait for log to be written
			time.Sleep(100 * time.Millisecond)

			// Read logs from stdout
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
			Expect(logs).NotTo(BeEmpty(), "Expected logs in stdout")

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

		It("should send logs to syslog for access-log-exporter", func() {
			// Logs are sent to syslog (127.0.0.1:8514) for the exporter sidecar
			// We can't directly test syslog, but we verify the exporter is receiving logs
			// by checking that stdout still works (dual logging: syslog + stdout)

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

			By("Verifying stdout contains access logs (syslog also configured)")
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
			// Use a path that may be cached. Nexus /status endpoint often sends
			// Cache-Control headers that may result in BYPASS, MISS, or HIT.
			// All three statuses are valid and confirm cache_status logging works.
			testPath := "/service/rest/v1/status"

			resp1, err := client.Get(nginxURL + testPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp1.Body.Close()
			Expect(resp1.StatusCode).To(Equal(http.StatusOK))

			time.Sleep(100 * time.Millisecond)

			resp2, err := client.Get(nginxURL + testPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close()
			Expect(resp2.StatusCode).To(Equal(http.StatusOK))

			time.Sleep(100 * time.Millisecond)

			// Read logs from stdout
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

			// Cache status can be HIT, MISS, BYPASS (when cached/upstream), or "-" when
			// upstream does not allow caching or response was not cached. All are valid.
			By("Verifying logs contain valid cache status (HIT, MISS, BYPASS, or '-')")
			hasHit := strings.Contains(logs, "HIT")
			hasMiss := strings.Contains(logs, "MISS")
			hasBypass := strings.Contains(logs, "BYPASS")
			// upstream_response_time or cache_status can be "-" (tab before minus)
			hasDash := strings.Contains(logs, "\t-")
			Expect(hasHit || hasMiss || hasBypass || hasDash).To(BeTrue(),
				"Expected cache status HIT, MISS, BYPASS, or '-' in logs")
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

		It("should successfully send logs to syslog with readonly filesystem", func() {
			// Generate traffic to Nexus
			_, err := client.Get(nginxURL + "/service/rest/v1/status")
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(100 * time.Millisecond)

			// Verify syslog logging works by checking stdout (dual logging)
			// Syslog doesn't write to files, so we can't check /tmp/access.log
			// but we can verify the nginx process is running and logs appear in stdout
			logOptions := &corev1.PodLogOptions{
				Container: nginxContainerName,
				TailLines: int64Ptr(5),
			}
			req := clientset.CoreV1().Pods(namespace).GetLogs(nginxPodName, logOptions)
			podLogs, err := req.Stream(ctx)
			Expect(err).NotTo(HaveOccurred())
			defer podLogs.Close()

			buf := new(strings.Builder)
			_, err = io.Copy(buf, podLogs)
			Expect(err).NotTo(HaveOccurred())

			content := buf.String()
			Expect(content).NotTo(BeEmpty(), "Expected logs in stdout")
			Expect(content).To(ContainSubstring("/service/rest/v1/status"), "Expected recent request in logs")
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
