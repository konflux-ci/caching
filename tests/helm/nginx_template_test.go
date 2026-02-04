package helm_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/caching/tests/testhelpers"
)

var _ = Describe("Helm Template Nginx Configuration", func() {
	Describe("Nginx Enabled/Disabled", func() {
		It("should not render nginx resources when nginx is disabled by default", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(BeEmpty(), "Should not contain nginx statefulset")

			cm := extractNginxConfigMapSection(output)
			Expect(cm).To(BeEmpty(), "Should not contain nginx configmap")

			svc := extractNginxServiceSection(output)
			Expect(svc).To(BeEmpty(), "Should not contain nginx service")

			hsvc := extractNginxHeadlessServiceSection(output)
			Expect(hsvc).To(BeEmpty(), "Should not contain nginx headless service")
		})

		It("should render all nginx resources when nginx is enabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify nginx StatefulSet
			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(ContainSubstring("kind: StatefulSet"), "Should contain StatefulSet")
			Expect(ss).To(ContainSubstring("name: "+testhelpers.NginxStatefulSetName), "Should contain nginx statefulset")

			// Verify nginx ConfigMap
			cm := extractNginxConfigMapSection(output)
			Expect(cm).To(ContainSubstring("kind: ConfigMap"), "Should contain ConfigMap")
			Expect(cm).To(ContainSubstring("name: nginx-config"), "Should contain nginx configmap")

			// Verify nginx Service
			svc := extractNginxServiceSection(output)
			Expect(svc).To(ContainSubstring("kind: Service"), "Should contain Service")
			Expect(svc).To(ContainSubstring("name: "+testhelpers.NginxServiceName), "Should contain nginx service")

			// Verify nginx headless Service
			hsvc := extractNginxHeadlessServiceSection(output)
			Expect(hsvc).To(ContainSubstring("nginx-headless"), "Should contain nginx headless service")
			Expect(hsvc).To(ContainSubstring("clusterIP: None"), "Headless service should have clusterIP: None")
		})
	})

	Describe("Nginx StatefulSet Configuration", func() {
		It("should configure replica count correctly", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled:      true,
					ReplicaCount: 3,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(ContainSubstring("replicas: 3"), "Should have 3 replicas")
		})

		It("should include topology spread constraints", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(ContainSubstring("topologySpreadConstraints"), "Should have topology spread constraints")
			Expect(ss).To(ContainSubstring("topology.kubernetes.io/zone"), "Should use zone topology key")
			Expect(ss).To(ContainSubstring("maxSkew: 1"), "Should have maxSkew of 1")
		})

		It("should include config checksum annotation for rolling updates", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(ContainSubstring("checksum/config:"), "Should have config checksum annotation")
		})

		It("should mount cache volume at correct path", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(ContainSubstring("mountPath: /var/cache/nginx"), "Should mount cache at /var/cache/nginx")
		})
	})

	Describe("Nginx Auth Configuration", func() {
		It("should not include init container when auth is disabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Auth: &testhelpers.NginxAuthValues{
						Enabled: false,
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).NotTo(ContainSubstring("initContainers"), "Should not have init containers when auth disabled")
			Expect(ss).NotTo(ContainSubstring("auth-secret"), "Should not mount auth secret when auth disabled")
		})

		It("should include init container and auth volumes when auth is enabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Auth: &testhelpers.NginxAuthValues{
						Enabled:    true,
						SecretName: "my-auth-secret",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)

			// Verify init container
			Expect(ss).To(ContainSubstring("initContainers"), "Should have init containers when auth enabled")
			Expect(ss).To(ContainSubstring("name: init-config"), "Should have init-config container")
			Expect(ss).To(ContainSubstring("sed \"s|__AUTH_HEADER__|${AUTH_VALUE}|g\""), "Should replace auth header placeholder")

			// Verify auth secret volume
			Expect(ss).To(ContainSubstring("auth-secret"), "Should have auth-secret volume")
			Expect(ss).To(ContainSubstring("secretName: my-auth-secret"), "Should reference correct secret")

			// Verify config volume (emptyDir for processed config)
			Expect(ss).To(ContainSubstring("name: config"), "Should have config volume")
			Expect(ss).To(ContainSubstring("emptyDir: {}"), "Config volume should be emptyDir")
		})

		It("should mount config differently based on auth setting", func() {
			// Without auth - mount config-template directly
			outputNoAuth, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(outputNoAuth)
			Expect(ss).To(ContainSubstring("name: config-template"), "Should mount config-template when auth disabled")

			// With auth - mount processed config
			outputWithAuth, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Auth: &testhelpers.NginxAuthValues{
						Enabled:    true,
						SecretName: "my-secret",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss = extractNginxStatefulSetSection(outputWithAuth)
			// Should mount the processed config from emptyDir, not config-template
			Expect(ss).To(MatchRegexp(`-\s+name: config\s+mountPath: /etc/nginx/nginx.conf`), "Should mount processed config when auth enabled")
		})
	})

	Describe("Nginx Cache Configuration", func() {
		It("should configure cache size in volumeClaimTemplate", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Cache: &testhelpers.NginxCacheValues{
						Size: 2048,
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			ss := extractNginxStatefulSetSection(output)
			Expect(ss).To(ContainSubstring("storage: 2048Mi"), "Should request 2048Mi storage for cache")
		})

		It("should configure cache size in nginx.conf", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Cache: &testhelpers.NginxCacheValues{
						Size: 512,
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			cm := extractNginxConfigMapSection(output)
			Expect(cm).To(ContainSubstring("max_size=512m"), "Should set max_size to 512m in nginx.conf")
		})

		It("should not create cached locations when allowList is empty", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Cache: &testhelpers.NginxCacheValues{
						AllowList: []string{},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			cm := extractNginxConfigMapSection(output)

			// Should only have the default location, not any cached location blocks
			Expect(cm).To(ContainSubstring("location / {"), "Should have default location")
			Expect(cm).To(ContainSubstring("proxy_no_cache 1"), "Default location should bypass cache")
			Expect(cm).NotTo(ContainSubstring("location ~ "), "Should not have regex cached locations")
		})

		It("should create cached location blocks for each allowList pattern", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Cache: &testhelpers.NginxCacheValues{
						AllowList: []string{
							`^/repository/maven-.*`,
							`^/repository/npm-.*`,
							`\.tar\.gz$`,
						},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			cm := extractNginxConfigMapSection(output)

			// Verify all three patterns create cached location blocks
			Expect(cm).To(ContainSubstring("location ~ ^/repository/maven-.*"), "Should have maven pattern cached location")
			Expect(cm).To(ContainSubstring("location ~ ^/repository/npm-.*"), "Should have npm pattern cached location")
			Expect(cm).To(ContainSubstring("location ~ \\.tar\\.gz$"), "Should have tar.gz pattern cached location")

			// Verify each cached location has cache directives
			Expect(strings.Count(cm, "proxy_cache backend_cache")).To(Equal(3), "Should have proxy_cache in 3 locations")
			Expect(strings.Count(cm, "proxy_cache_valid 200 1d")).To(Equal(3), "Should have cache_valid in 3 locations")

			// Verify default location still exists
			Expect(cm).To(ContainSubstring("location / {"), "Should still have default location")
		})

		It("should include auth header in both cached and default locations when auth is enabled", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Auth: &testhelpers.NginxAuthValues{
						Enabled:    true,
						SecretName: "my-secret",
					},
					Cache: &testhelpers.NginxCacheValues{
						AllowList: []string{`^/api/.*`},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			cm := extractNginxConfigMapSection(output)

			// Auth header should appear in both cached location and default location
			Expect(strings.Count(cm, `proxy_set_header Authorization "__AUTH_HEADER__"`)).To(Equal(2), "Should have auth header in both locations")
		})
	})

	Describe("Nginx Service trafficDistribution Configuration", func() {
		It("should not include trafficDistribution when not set", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			svc := extractNginxServiceSection(output)
			hsvc := extractNginxHeadlessServiceSection(output)

			Expect(svc).NotTo(ContainSubstring("trafficDistribution"), "Regular service should not have trafficDistribution when not set")
			Expect(hsvc).NotTo(ContainSubstring("trafficDistribution"), "Headless service should not have trafficDistribution when not set")
		})

		It("should include trafficDistribution on both services when set", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://backend:8080",
					},
					Service: &testhelpers.NginxServiceValues{
						TrafficDistribution: "PreferSameZone",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			svc := extractNginxServiceSection(output)
			hsvc := extractNginxHeadlessServiceSection(output)

			Expect(svc).To(ContainSubstring("trafficDistribution: PreferSameZone"), "Regular service should have trafficDistribution: PreferSameZone")
			Expect(hsvc).To(ContainSubstring("trafficDistribution: PreferSameZone"), "Headless service should have trafficDistribution: PreferSameZone")
		})
	})

	Describe("Nginx ConfigMap Upstream Configuration", func() {
		It("should configure upstream URL in all proxy_pass directives", func() {
			output, err := testhelpers.RenderHelmTemplate(chartPath, testhelpers.SquidHelmValues{
				Nginx: &testhelpers.NginxValues{
					Enabled: true,
					Upstream: &testhelpers.NginxUpstreamValues{
						URL: "http://nexus.example.com:8081",
					},
					Cache: &testhelpers.NginxCacheValues{
						AllowList: []string{`^/api/.*`},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			cm := extractNginxConfigMapSection(output)

			// Upstream URL should appear in both cached location and default location
			Expect(strings.Count(cm, "proxy_pass http://nexus.example.com:8081")).To(Equal(2), "Should have upstream URL in both locations")
		})
	})
})
