package e2e_test

import (
	"context"
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
	"k8s.io/apimachinery/pkg/util/wait"
)

const nginxContainerName = "nginx"

// detailedLogFormatRegex matches the NGINX "detailed" log format used for access-log-exporter:
// $request_method \t $status \t $request_uri \t $request_time \t $upstream_response_time \t $upstream_cache_status
// Example: GET\t200\t/health\t0.000\t-\t-
var detailedLogFormatRegex = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\t(\d{3})\t[^\t]+\t[\d.]+\t[\d.-]+\t(HIT|MISS|BYPASS|EXPIRED|STALE|UPDATING|-)$`)

var _ = Describe("NGINX access log format for access-log-exporter", Label("nginx", "monitoring"), Ordered, Serial, func() {
	var client *http.Client

	BeforeAll(func() {
		nexusConfig := testhelpers.NewNexusConfig()

		err := testhelpers.ConfigureSquidWithHelm(ctx, clientset, testhelpers.SquidHelmValues{
			Nginx: &testhelpers.NginxValues{
				Enabled: true,
				Upstream: &testhelpers.NginxUpstreamValues{
					URL: nexusConfig.URL,
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		client = testhelpers.NewNginxClient()
	})

	It("should write access logs in the detailed format for metrics extraction", func() {
		before := metav1.Now()

		By("Making a request through NGINX to generate an access log entry (use a path that is logged; /health has access_log off)")
		url := testhelpers.GetNginxURL() + "/service/rest/v1/status"
		resp, err := client.Get(url)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "request should succeed to produce a log line")

		By("Polling for the detailed format log line to appear")
		Eventually(func() bool {
			pods, err := testhelpers.GetPods(ctx, clientset, namespace, testhelpers.NginxStatefulSetName)
			if err != nil || len(pods) == 0 {
				return false
			}

			for _, pod := range pods {
				logs, err := testhelpers.GetPodLogsSince(ctx, clientset, namespace, pod.Name, nginxContainerName, &before)
				if err != nil {
					continue
				}

				for _, line := range strings.Split(string(logs), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					// Detailed format: 6 tab-separated fields
					fields := strings.Split(line, "\t")
					if len(fields) != 6 {
						continue
					}
					if detailedLogFormatRegex.MatchString(line) {
						GinkgoWriter.Printf("Found detailed-format log line: %q\n", line)
						return true
					}
				}
			}
			return false
		}, 30*time.Second, 2*time.Second).Should(BeTrue(),
			"NGINX access logs should contain at least one line in the detailed format (method\\tstatus\\turi\\trequest_time\\tupstream_response_time\\tupstream_cache_status) for access-log-exporter metrics extraction")
	})

	It("should return stub_status with expected format when requested from localhost", func() {
		restConfig, err := testhelpers.GetRESTConfig()
		Expect(err).NotTo(HaveOccurred())

		pods, err := testhelpers.GetPods(ctx, clientset, namespace, testhelpers.NginxStatefulSetName)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).NotTo(BeEmpty())

		// Request from inside the pod (localhost); nginx allows 127.0.0.1.
		url := "http://127.0.0.1:8081/stub_status"
		var stdout, stderr string
		stdout, stderr, err = testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, pods[0].Name, nginxContainerName, []string{"curl", "-s", url})
		if err != nil && (strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "not found in $PATH")) {
			stdout, stderr, err = testhelpers.ExecCommandInPod(ctx, clientset, restConfig, namespace, pods[0].Name, nginxContainerName, []string{"wget", "-qO-", url})
		}
		if err != nil {
			if strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "not found in $PATH") {
				Skip("nginx image does not include curl or wget; cannot verify stub_status response from localhost")
			}
			Expect(err).NotTo(HaveOccurred(), "curl/wget stub_status from localhost failed: stderr=%s", stderr)
		}
		// stub_status returns plaintext: "Active connections: N\n..." and "Reading: N Writing: N Waiting: N"
		Expect(stdout).To(ContainSubstring("Active connections"), "stub_status response should contain Active connections")
		Expect(stdout).To(ContainSubstring("Reading:"), "stub_status response should contain Reading:")
	})

	It("should not allow access to stub_status from outside the pod", func() {
		pods, err := testhelpers.GetPods(ctx, clientset, namespace, testhelpers.NginxStatefulSetName)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).NotTo(BeEmpty())
		nginxPodIP := pods[0].Status.PodIP
		Expect(nginxPodIP).NotTo(BeEmpty(), "nginx pod must have an IP")

		// Run a one-off pod that curls stub_status from outside (pod IP, not localhost); expect 403
		curlPodName := "curl-stub-status-test"
		curlPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: curlPodName, Namespace: namespace},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{
					Name:  "curl",
					Image: "curlimages/curl:8.11.1@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69",
					Command: []string{"sh", "-c"},
					Args:   []string{fmt.Sprintf("curl -s -o /dev/stdout -w '%%{http_code}' http://%s:8081/stub_status", nginxPodIP)},
				}},
			},
		}
		_, err = clientset.CoreV1().Pods(namespace).Create(ctx, curlPod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = clientset.CoreV1().Pods(namespace).Delete(ctx, curlPodName, metav1.DeleteOptions{})
		})

		By("Waiting for curl pod to complete")
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, testhelpers.Timeout, true, func(ctx context.Context) (bool, error) {
			p, getErr := clientset.CoreV1().Pods(namespace).Get(ctx, curlPodName, metav1.GetOptions{})
			if getErr != nil {
				return false, getErr
			}
			return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed, nil
		})
		Expect(err).NotTo(HaveOccurred(), "curl pod did not complete in time")

		logOpts := &corev1.PodLogOptions{Container: "curl"}
		logs, err := clientset.CoreV1().Pods(namespace).GetLogs(curlPodName, logOpts).DoRaw(ctx)
		Expect(err).NotTo(HaveOccurred())
		logStr := strings.TrimSpace(string(logs))
		// curl -w '%{http_code}' prints the code at the end; body may be empty or "403 Forbidden"
		Expect(logStr).To(HaveSuffix("403"), "stub_status must return 403 when requested from outside the pod (got output: %q)", logStr)
	})
})
