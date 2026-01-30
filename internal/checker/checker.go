// Package checker implements TLS health checking for federation servers.
package checker

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/joesiltberg/bowness/fedtls"
	"github.com/joesiltberg/bowness/util"
)

// Result represents the outcome of a health check
type Result struct {
	EntityID        string
	BaseURI         string
	IsHealthy       bool
	ErrorMessage    string
	CertExpires     *time.Time
	CertCN          string
	CertFingerprint string
	CheckedAt       time.Time
}

// Checker performs TLS health checks against servers
type Checker interface {
	Check(entityID string, server fedtls.Server) *Result
}

// RealChecker performs actual TLS health checks against servers
type RealChecker struct {
	timeout time.Duration
}

// NewRealChecker creates a new RealChecker with the given TLS timeout
func NewRealChecker(timeout time.Duration) *RealChecker {
	return &RealChecker{timeout: timeout}
}

// Check performs a health check against a server
func (c *RealChecker) Check(entityID string, server fedtls.Server) *Result {
	result := &Result{
		EntityID:  entityID,
		BaseURI:   server.BaseURI,
		CheckedAt: time.Now(),
	}

	// Parse the base URI to get host and port
	host, port, err := parseBaseURI(server.BaseURI)
	if err != nil {
		result.IsHealthy = false
		result.ErrorMessage = fmt.Sprintf("invalid base_uri: %v", err)
		return result
	}

	// Perform TLS handshake and get certificate
	cert, err := c.getTLSCertificate(host, port)
	if err != nil {
		result.IsHealthy = false
		result.ErrorMessage = fmt.Sprintf("TLS connection failed: %v", err)
		return result
	}

	// We got a certificate, verify it
	result.CertCN = cert.Subject.CommonName
	result.CertExpires = &cert.NotAfter
	result.CertFingerprint = util.Fingerprint(cert)

	// Check certificate expiry
	if time.Now().After(cert.NotAfter) {
		result.IsHealthy = false
		result.ErrorMessage = fmt.Sprintf("certificate expired on %s", cert.NotAfter.Format(time.RFC3339))
		return result
	}

	// Check if CN or SAN matches hostname
	if !matchesHostname(cert, host) {
		result.IsHealthy = false
		result.ErrorMessage = fmt.Sprintf("certificate CN (%s) and SANs do not match hostname (%s)", cert.Subject.CommonName, host)
		return result
	}

	// Verify fingerprint against metadata pins
	if !matchesPin(result.CertFingerprint, server.Pins) {
		result.IsHealthy = false
		result.ErrorMessage = fmt.Sprintf("certificate fingerprint (%s) does not match any pin in metadata", result.CertFingerprint)
		return result
	}

	result.IsHealthy = true
	return result
}

// parseBaseURI extracts host and port from a base URI
func parseBaseURI(baseURI string) (string, string, error) {
	u, err := url.Parse(baseURI)
	if err != nil {
		return "", "", err
	}

	host := u.Hostname()
	port := u.Port()

	if port == "" {
		port = "443"
	}

	if host == "" {
		return "", "", fmt.Errorf("no host in URI")
	}

	return host, port, nil
}

// getTLSCertificate connects to the server and retrieves its certificate.
// Uses VerifyPeerCertificate callback to capture the certificate regardless
// of whether the handshake succeeds (e.g., even if server requires client cert).
func (c *RealChecker) getTLSCertificate(host, port string) (*x509.Certificate, error) {
	addr := net.JoinHostPort(host, port)

	dialer := &net.Dialer{Timeout: c.timeout}
	rawConn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer rawConn.Close()

	rawConn.SetDeadline(time.Now().Add(c.timeout))

	var capturedCert *x509.Certificate

	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true, // We verify the cert ourselves against metadata
		ServerName:         host,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) > 0 {
				cert, err := x509.ParseCertificate(rawCerts[0])
				if err == nil {
					capturedCert = cert
				}
			}
			return nil
		},
	})

	// Attempt the handshake - we don't care if it fails, we just want the cert
	tlsConn.Handshake()
	tlsConn.Close()

	if capturedCert == nil {
		return nil, fmt.Errorf("no certificate received from server")
	}

	return capturedCert, nil
}

// matchesHostname checks if the certificate's CN or any SAN matches the hostname
func matchesHostname(cert *x509.Certificate, hostname string) bool {
	// Check CN
	if matchHostname(cert.Subject.CommonName, hostname) {
		return true
	}

	// Check DNS SANs
	for _, san := range cert.DNSNames {
		if matchHostname(san, hostname) {
			return true
		}
	}

	// Check IP SANs
	ip := net.ParseIP(hostname)
	if ip != nil {
		for _, sanIP := range cert.IPAddresses {
			if sanIP.Equal(ip) {
				return true
			}
		}
	}

	return false
}

// matchHostname checks if a pattern (possibly with wildcard) matches a hostname
func matchHostname(pattern, hostname string) bool {
	pattern = strings.ToLower(pattern)
	hostname = strings.ToLower(hostname)

	if pattern == hostname {
		return true
	}

	// Handle wildcard certificates (e.g., *.example.com)
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // Remove the *
		// Wildcard only matches one level
		if strings.HasSuffix(hostname, suffix) {
			// Make sure there's exactly one level before the suffix
			prefix := strings.TrimSuffix(hostname, suffix)
			if !strings.Contains(prefix, ".") && prefix != "" {
				return true
			}
		}
	}

	return false
}

// matchesPin checks if the fingerprint matches any of the pins
func matchesPin(fingerprint string, pins []fedtls.Pin) bool {
	for _, pin := range pins {
		if pin.Alg == "sha256" && pin.Digest == fingerprint {
			return true
		}
	}
	return false
}

// DummyChecker is a test implementation that always reports servers as healthy
type DummyChecker struct{}

// NewDummyChecker creates a new DummyChecker
func NewDummyChecker() *DummyChecker {
	return &DummyChecker{}
}

// Check returns a healthy result without performing any actual checks
func (c *DummyChecker) Check(entityID string, server fedtls.Server) *Result {
	return &Result{
		EntityID:  entityID,
		BaseURI:   server.BaseURI,
		IsHealthy: true,
		CheckedAt: time.Now(),
	}
}
