package testhelpers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagermeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	certmanagerclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NginxValues holds Helm values for nginx configuration
type NginxValues struct {
	// Enabled must NOT have omitempty since we need to explicitly set false to disable
	Enabled      bool                 `json:"enabled"`
	ReplicaCount int                  `json:"replicaCount,omitempty"`
	TLS          *NginxTLSValues      `json:"tls,omitempty"`
	Upstream     *NginxUpstreamValues `json:"upstream,omitempty"`
	Auth         *NginxAuthValues     `json:"auth,omitempty"`
	Cache        *NginxCacheValues    `json:"cache,omitempty"`
	Service      *NginxServiceValues  `json:"service,omitempty"`
}

// NginxTLSValues holds TLS configuration
type NginxTLSValues struct {
	Enabled    bool   `json:"enabled"`
	SecretName string `json:"secretName,omitempty"`
}

// NginxUpstreamValues holds upstream server configuration
type NginxUpstreamValues struct {
	URL string `json:"url,omitempty"`
}

// NginxAuthValues holds authorization header injection configuration
type NginxAuthValues struct {
	Enabled    bool   `json:"enabled"`
	SecretName string `json:"secretName,omitempty"`
}

// NginxCacheValues holds cache configuration
type NginxCacheValues struct {
	AllowList []string `json:"allowList,omitempty"`
	Size      int      `json:"size,omitempty"`
}

// NginxServiceValues holds service configuration
type NginxServiceValues struct {
	Type                string            `json:"type,omitempty"`
	Port                int               `json:"port,omitempty"`
	TrafficDistribution string            `json:"trafficDistribution,omitempty"`
	Annotations         map[string]string `json:"annotations,omitempty"`
}

// NewNginxClient creates an HTTP client for requests to nginx
func NewNginxClient() *http.Client {
	transport := &http.Transport{
		DisableKeepAlives: true,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   Timeout,
	}
}

// GetNginxURL returns the URL for the nginx service
func GetNginxURL() string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", NginxServiceName, Namespace, NginxPort)
}

// NewNginxHTTPSClient creates HTTPS client with custom CA
func NewNginxHTTPSClient(caCert []byte) (*http.Client, error) {
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA cert to pool")
	}
	tlsConfig := &tls.Config{
		RootCAs: caCertPool,
	}
	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		DisableKeepAlives: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   Timeout,
	}, nil
}

// GetNginxHTTPSURL returns HTTPS URL for nginx service
func GetNginxHTTPSURL() string {
	return fmt.Sprintf("https://%s.%s.svc.cluster.local:%d", NginxServiceName, Namespace, NginxHTTPSPort)
}

// CreateNginxCertificate creates a cert-manager Certificate resource for nginx
func CreateNginxCertificate(ctx context.Context, client *certmanagerclient.Clientset, secretName string) error {
	cert := &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-cert",
			Namespace: Namespace,
		},
		Spec: certmanagerv1.CertificateSpec{
			SecretName:  secretName,
			Duration:    &metav1.Duration{Duration: time.Hour * 24},
			RenewBefore: &metav1.Duration{Duration: time.Hour * 12},
			Subject: &certmanagerv1.X509Subject{
				Organizations: []string{"konflux"},
			},
			DNSNames: []string{
				NginxServiceName,
				fmt.Sprintf("%s.%s.svc", NginxServiceName, Namespace),
				fmt.Sprintf("%s.%s.svc.cluster.local", NginxServiceName, Namespace),
			},
			PrivateKey: &certmanagerv1.CertificatePrivateKey{
				Algorithm: certmanagerv1.ECDSAKeyAlgorithm,
				Size:      256,
			},
			IssuerRef: certmanagermeta.ObjectReference{
				Name:  Namespace + "-ca-issuer",
				Kind:  "ClusterIssuer",
				Group: "cert-manager.io",
			},
		},
	}

	_, err := client.CertmanagerV1().Certificates(Namespace).Create(ctx, cert, metav1.CreateOptions{})
	return err
}

// DeleteNginxCertificate deletes the cert-manager Certificate resource for nginx
func DeleteNginxCertificate(ctx context.Context, client *certmanagerclient.Clientset) error {
	return client.CertmanagerV1().Certificates(Namespace).Delete(ctx, "nginx-cert", metav1.DeleteOptions{})
}
