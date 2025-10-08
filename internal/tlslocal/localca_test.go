package tlslocal

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureServerTLSConfig(t *testing.T) {
	// Create a temporary directory for test certificates
	tempDir, err := os.MkdirTemp("", "tlslocal_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test options
	opts := Options{
		Dir:               tempDir,
		RequireClientCert: false,
	}

	// Test certificate generation and TLS config creation
	tlsConfig, err := EnsureServerTLSConfig(opts)
	if err != nil {
		t.Fatalf("EnsureServerTLSConfig failed: %v", err)
	}

	// Verify TLS config properties
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("Expected MinVersion TLS 1.2, got %v", tlsConfig.MinVersion)
	}

	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("Expected 1 certificate, got %d", len(tlsConfig.Certificates))
	}

	if tlsConfig.ClientAuth != tls.NoClientCert {
		t.Errorf("Expected NoClientCert, got %v", tlsConfig.ClientAuth)
	}

	// Verify certificate files exist
	expectedFiles := []string{"ca.pem", "ca.key", "localhost.pem", "localhost.key"}
	for _, filename := range expectedFiles {
		path := filepath.Join(tempDir, filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Expected file %s to exist", path)
		}
	}

	// Test certificate validation
	caCertPath := filepath.Join(tempDir, "ca.pem")
	serverCertPath := filepath.Join(tempDir, "localhost.pem")

	// Load and verify CA certificate
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("Failed to read CA cert: %v", err)
	}

	caCert, err := parseCertFromPEM(caCertPEM)
	if err != nil {
		t.Fatalf("Failed to parse CA cert: %v", err)
	}

	if !caCert.IsCA {
		t.Error("CA certificate should have IsCA set to true")
	}

	if caCert.Subject.CommonName != "mcpproxy local CA" {
		t.Errorf("Expected CA common name 'mcpproxy local CA', got %s", caCert.Subject.CommonName)
	}

	// Load and verify server certificate
	serverCertPEM, err := os.ReadFile(serverCertPath)
	if err != nil {
		t.Fatalf("Failed to read server cert: %v", err)
	}

	serverCert, err := parseCertFromPEM(serverCertPEM)
	if err != nil {
		t.Fatalf("Failed to parse server cert: %v", err)
	}

	if serverCert.Subject.CommonName != "localhost" {
		t.Errorf("Expected server common name 'localhost', got %s", serverCert.Subject.CommonName)
	}

	// Verify DNS names and IP addresses
	expectedDNSNames := []string{"localhost"}
	if len(serverCert.DNSNames) != len(expectedDNSNames) {
		t.Errorf("Expected %d DNS names, got %d", len(expectedDNSNames), len(serverCert.DNSNames))
	}

	for i, expected := range expectedDNSNames {
		if i >= len(serverCert.DNSNames) || serverCert.DNSNames[i] != expected {
			t.Errorf("Expected DNS name %s, got %s", expected, serverCert.DNSNames[i])
		}
	}

	expectedIPs := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if len(serverCert.IPAddresses) != len(expectedIPs) {
		t.Errorf("Expected %d IP addresses, got %d", len(expectedIPs), len(serverCert.IPAddresses))
	}

	for i, expected := range expectedIPs {
		if i >= len(serverCert.IPAddresses) || !serverCert.IPAddresses[i].Equal(expected) {
			t.Errorf("Expected IP address %s, got %s", expected, serverCert.IPAddresses[i])
		}
	}
}

func TestEnsureServerTLSConfigWithClientCert(t *testing.T) {
	// Create a temporary directory for test certificates
	tempDir, err := os.MkdirTemp("", "tlslocal_test_client")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test options with client certificate requirement
	opts := Options{
		Dir:               tempDir,
		RequireClientCert: true,
	}

	// Test certificate generation and TLS config creation
	tlsConfig, err := EnsureServerTLSConfig(opts)
	if err != nil {
		t.Fatalf("EnsureServerTLSConfig failed: %v", err)
	}

	// Verify client cert requirement
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("Expected RequireAndVerifyClientCert, got %v", tlsConfig.ClientAuth)
	}

	// Verify ClientCAs is set
	if tlsConfig.ClientCAs == nil {
		t.Error("Expected ClientCAs to be set when RequireClientCert is true")
	}
}

func TestEnsureServerTLSConfigReusesCerts(t *testing.T) {
	// Create a temporary directory for test certificates
	tempDir, err := os.MkdirTemp("", "tlslocal_test_reuse")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	opts := Options{
		Dir:               tempDir,
		RequireClientCert: false,
	}

	// First call - should generate certificates
	_, err = EnsureServerTLSConfig(opts)
	if err != nil {
		t.Fatalf("First EnsureServerTLSConfig failed: %v", err)
	}

	// Get modification times
	caCertPath := filepath.Join(tempDir, "ca.pem")
	serverCertPath := filepath.Join(tempDir, "localhost.pem")

	caStat1, err := os.Stat(caCertPath)
	if err != nil {
		t.Fatalf("Failed to stat CA cert: %v", err)
	}

	serverStat1, err := os.Stat(serverCertPath)
	if err != nil {
		t.Fatalf("Failed to stat server cert: %v", err)
	}

	// Sleep to ensure different modification times if files are regenerated
	time.Sleep(100 * time.Millisecond)

	// Second call - should reuse existing certificates
	_, err = EnsureServerTLSConfig(opts)
	if err != nil {
		t.Fatalf("Second EnsureServerTLSConfig failed: %v", err)
	}

	caStat2, err := os.Stat(caCertPath)
	if err != nil {
		t.Fatalf("Failed to stat CA cert after second call: %v", err)
	}

	serverStat2, err := os.Stat(serverCertPath)
	if err != nil {
		t.Fatalf("Failed to stat server cert after second call: %v", err)
	}

	// Verify modification times haven't changed (certificates were reused)
	if !caStat1.ModTime().Equal(caStat2.ModTime()) {
		t.Error("CA certificate was regenerated when it should have been reused")
	}

	if !serverStat1.ModTime().Equal(serverStat2.ModTime()) {
		t.Error("Server certificate was regenerated when it should have been reused")
	}
}

func TestServeWithTLS(t *testing.T) {
	// Create a temporary directory for test certificates
	tempDir, err := os.MkdirTemp("", "tlslocal_test_serve")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	opts := Options{
		Dir:               tempDir,
		RequireClientCert: false,
	}

	// Generate TLS config
	tlsConfig, err := EnsureServerTLSConfig(opts)
	if err != nil {
		t.Fatalf("EnsureServerTLSConfig failed: %v", err)
	}

	// Create a test HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	})

	server := &http.Server{
		Handler: mux,
	}

	// Create a listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	// Start the TLS server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		err := ServeWithTLS(server, listener, tlsConfig)
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

	// Test that the server is running with TLS
	addr := listener.Addr().String()

	// Create a client that trusts our local CA
	caCert, err := os.ReadFile(filepath.Join(tempDir, "ca.pem"))
	if err != nil {
		t.Fatalf("Failed to read CA cert: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
		Timeout: 5 * time.Second,
	}

	// Make a request to the server
	resp, err := client.Get("https://" + addr + "/test")
	if err != nil {
		t.Fatalf("Failed to make HTTPS request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "test response" {
		t.Errorf("Expected 'test response', got %s", string(body))
	}

	// Shutdown the server
	server.Close()

	// Check for server errors
	select {
	case err := <-serverErr:
		t.Fatalf("Server error: %v", err)
	default:
		// No error, which is what we expect
	}
}

// Helper function to parse a certificate from PEM data
func parseCertFromPEM(pemData []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	return x509.ParseCertificate(block.Bytes)
}
