package tlslocal

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSANs(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		dns, ips, err := ParseSANs([]string{
			"mcp.internal", " MCP-Proxy.Example.COM ", "my_service", "host1",
			"192.168.1.10", "fd7a:115c:a1e0::1",
			"localhost", "127.0.0.1", "::1", "mcp.internal", "192.168.1.10", // already carried, or repeated
		})
		if err != nil {
			t.Fatalf("ParseSANs: %v", err)
		}
		if got, want := strings.Join(dns, ","), "mcp.internal,mcp-proxy.example.com,my_service,host1"; got != want {
			t.Errorf("dns = %s, want %s", got, want)
		}
		if len(ips) != 2 || ips[0].String() != "192.168.1.10" || ips[1].String() != "fd7a:115c:a1e0::1" {
			t.Errorf("ips = %v", ips)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		dns, ips, err := ParseSANs(nil)
		if err != nil || len(dns) != 0 || len(ips) != 0 {
			t.Errorf("ParseSANs(nil) = %v, %v, %v", dns, ips, err)
		}
	})

	invalid := map[string]struct{ entry, wantErr string }{
		"empty":          {"", "extra_sans[1] is empty"},
		"blank":          {"   ", "extra_sans[1] is empty"},
		"wildcard":       {"*.example.com", "wildcard"},
		"url":            {"https://mcp.example.com", "not a valid host name"},
		"port":           {"mcp.example.com:8080", "not a valid host name"},
		"path":           {"mcp.example.com/x", "not a valid host name"},
		"bracketed ipv6": {"[::1]", "not a valid host name"},
		"ipv6 zone":      {"fe80::1%eth0", "not a valid host name"},
		"trailing dot":   {"mcp.example.com.", "empty label"},
		"empty label":    {"a..b", "empty label"},
		"leading hyphen": {"-a.example", "starts or ends with '-'"},
		"non-ascii":      {"mcp.exämple.com", "not a valid host name"},
		"space inside":   {"mcp example", "not a valid host name"},
		"long label":     {strings.Repeat("a", 64) + ".example", "longer than 63"},
		"long name":      {strings.Repeat("a.", 130) + "b", "longer than 253"},
	}
	for name, tc := range invalid {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseSANs([]string{"ok.example", tc.entry})
			if err == nil {
				t.Fatalf("ParseSANs(%q) succeeded, want an error", tc.entry)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "extra_sans[1]") {
				t.Errorf("error %q does not start with the entry index", err)
			}
		})
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func leafOf(t *testing.T, cfg *tls.Config) *x509.Certificate {
	t.Helper()
	if len(cfg.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(cfg.Certificates))
	}
	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf
}

func sansOf(c *x509.Certificate) []string {
	return sanSet(c.DNSNames, c.IPAddresses)
}

func TestExtraSANsRegenerateLeafFromSameCA(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureServerTLSConfig(Options{Dir: dir}); err != nil {
		t.Fatalf("initial: %v", err)
	}
	caCrt, caKey := readFile(t, filepath.Join(dir, "ca.pem")), readFile(t, filepath.Join(dir, "ca.key"))
	oldLeaf, oldKey := readFile(t, filepath.Join(dir, "localhost.pem")), readFile(t, filepath.Join(dir, "localhost.key"))

	extras := []string{"mcp.internal", "10.1.2.3"}
	cfg, err := EnsureServerTLSConfig(Options{Dir: dir, ExtraSANs: extras})
	if err != nil {
		t.Fatalf("with extra SANs: %v", err)
	}

	leaf := leafOf(t, cfg)
	want := []string{"dns:localhost", "dns:mcp.internal", "ip:10.1.2.3", "ip:127.0.0.1", "ip:::1"}
	if got := sansOf(leaf); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("SANs = %v, want %v", got, want)
	}
	if !bytes.Equal(caCrt, readFile(t, filepath.Join(dir, "ca.pem"))) || !bytes.Equal(caKey, readFile(t, filepath.Join(dir, "ca.key"))) {
		t.Error("the CA changed; clients that trust ca.pem would stop working")
	}
	if bytes.Equal(oldLeaf, readFile(t, filepath.Join(dir, "localhost.pem"))) || bytes.Equal(oldKey, readFile(t, filepath.Join(dir, "localhost.key"))) {
		t.Error("the leaf certificate and key were not replaced")
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCrt)
	for _, name := range []string{"localhost", "mcp.internal"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name}); err != nil {
			t.Errorf("leaf does not verify for %s against the unchanged CA: %v", name, err)
		}
	}

	t.Run("idempotent", func(t *testing.T) {
		before := readFile(t, filepath.Join(dir, "localhost.pem"))
		// Same names in another order and spelling: nothing to change.
		if _, err := EnsureServerTLSConfig(Options{Dir: dir, ExtraSANs: []string{"10.1.2.3", "MCP.internal", "localhost"}}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, readFile(t, filepath.Join(dir, "localhost.pem"))) {
			t.Error("leaf was regenerated although it already carried every name")
		}
	})

	t.Run("changed list regenerates", func(t *testing.T) {
		before := readFile(t, filepath.Join(dir, "localhost.pem"))
		cfg, err := EnsureServerTLSConfig(Options{Dir: dir, ExtraSANs: []string{"mcp.internal", "10.1.2.3", "other.internal"}})
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(before, readFile(t, filepath.Join(dir, "localhost.pem"))) {
			t.Error("leaf was not regenerated for a new name")
		}
		if got := leafOf(t, cfg).DNSNames; len(got) != 3 {
			t.Errorf("DNSNames = %v", got)
		}
		// Removing a name from a non-empty list also regenerates.
		cfg, err = EnsureServerTLSConfig(Options{Dir: dir, ExtraSANs: []string{"other.internal"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(sansOf(leafOf(t, cfg)), ","); strings.Contains(got, "mcp.internal") || !strings.Contains(got, "other.internal") {
			t.Errorf("SANs after removal = %s", got)
		}
	})

	t.Run("emptying the list drops the extra names", func(t *testing.T) {
		cfg, err := EnsureServerTLSConfig(Options{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		want := "dns:localhost,ip:127.0.0.1,ip:::1"
		if got := strings.Join(sansOf(leafOf(t, cfg)), ","); got != want {
			t.Errorf("SANs after emptying extra_sans = %s, want %s", got, want)
		}
		before := readFile(t, filepath.Join(dir, "localhost.pem"))
		if _, err := EnsureServerTLSConfig(Options{Dir: dir}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, readFile(t, filepath.Join(dir, "localhost.pem"))) {
			t.Error("a plain localhost leaf was rewritten on a restart without extra_sans")
		}
	})

	if entries, _ := os.ReadDir(dir); len(entries) != 4 {
		t.Errorf("certs dir holds %d entries, want exactly ca/leaf pem+key (temporary files left behind?)", len(entries))
	}
}

func TestExtraSANsFirstStartGeneratesNames(t *testing.T) {
	cfg, err := EnsureServerTLSConfig(Options{Dir: t.TempDir(), ExtraSANs: []string{"svc.internal"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sansOf(leafOf(t, cfg)), ","); !strings.Contains(got, "dns:svc.internal") {
		t.Errorf("SANs = %s", got)
	}
}

func TestExtraSANsRefuseToOverwriteForeignLeaf(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureServerTLSConfig(Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	// Replace the generated leaf with one another CA signed, as an operator
	// who followed the old "drop a certificate in as localhost.pem" way has.
	foreign := t.TempDir()
	writeServerPair(t, foreign, "other.example")
	for _, f := range []string{"localhost.pem", "localhost.key"} {
		if err := os.WriteFile(filepath.Join(dir, f), readFile(t, filepath.Join(foreign, f)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := readFile(t, filepath.Join(dir, "localhost.pem"))

	_, err := EnsureServerTLSConfig(Options{Dir: dir, ExtraSANs: []string{"mcp.internal"}})
	if err == nil || !strings.Contains(err.Error(), "tls.cert_file") {
		t.Fatalf("error = %v, want one that points at tls.cert_file", err)
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(dir, "localhost.pem"))) {
		t.Error("the operator's certificate was overwritten")
	}
	// Without extra_sans the old behaviour holds: the hand-made leaf is served.
	cfg, err := EnsureServerTLSConfig(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := leafOf(t, cfg).DNSNames; len(got) != 2 || got[1] != "other.example" {
		t.Errorf("DNSNames = %v", got)
	}
}

// writeServerPair writes ca.pem, ca.key, localhost.pem and localhost.key for
// the given names into dir and returns the CA certificate pool.
func writeServerPair(t *testing.T, dir string, names ...string) *x509.CertPool {
	t.Helper()
	caCrt, caKey := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := genLocalCA(caCrt, caKey); err != nil {
		t.Fatal(err)
	}
	var dns []string
	var ips []net.IP
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
		} else {
			dns = append(dns, n)
		}
	}
	if err := genServerCert(caCrt, caKey, filepath.Join(dir, "localhost.pem"), filepath.Join(dir, "localhost.key"), dns, ips); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(readFile(t, caCrt))
	return pool
}

func TestProvidedCertificate(t *testing.T) {
	src := t.TempDir()
	writeServerPair(t, src, "mcp.example.org")
	certFile, keyFile := filepath.Join(src, "localhost.pem"), filepath.Join(src, "localhost.key")

	t.Run("served as is, no local CA generated", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "certs")
		cfg, err := EnsureServerTLSConfig(Options{Dir: dir, CertFile: certFile, KeyFile: keyFile})
		if err != nil {
			t.Fatal(err)
		}
		if got := leafOf(t, cfg).DNSNames; len(got) != 2 || got[1] != "mcp.example.org" {
			t.Errorf("DNSNames = %v", got)
		}
		if cfg.ClientAuth != tls.NoClientCert || cfg.ClientCAs != nil {
			t.Errorf("ClientAuth = %v, ClientCAs set = %v", cfg.ClientAuth, cfg.ClientCAs != nil)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("certs dir was created (%v); nothing should be generated", err)
		}
	})

	t.Run("mTLS verifies clients against the local CA", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := EnsureServerTLSConfig(Options{Dir: dir, CertFile: certFile, KeyFile: keyFile, RequireClientCert: true})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ClientAuth != tls.RequireAndVerifyClientCert || cfg.ClientCAs == nil {
			t.Errorf("ClientAuth = %v, ClientCAs set = %v", cfg.ClientAuth, cfg.ClientCAs != nil)
		}
		for _, f := range []string{"ca.pem", "ca.key"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "localhost.pem")); !os.IsNotExist(err) {
			t.Errorf("a local server certificate was generated next to the provided one (%v)", err)
		}
	})

	t.Run("trailing newline and CRLF are accepted", func(t *testing.T) {
		crlf := filepath.Join(t.TempDir(), "cert.pem")
		if err := os.WriteFile(crlf, bytes.ReplaceAll(readFile(t, certFile), []byte("\n"), []byte("\r\n")), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := EnsureServerTLSConfig(Options{Dir: t.TempDir(), CertFile: crlf, KeyFile: keyFile}); err != nil {
			t.Fatal(err)
		}
	})

	other := t.TempDir()
	writeServerPair(t, other, "mcp.example.org")
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a pem file, secret-marker-123"), 0o600); err != nil {
		t.Fatal(err)
	}
	errCases := map[string]struct {
		opts    Options
		wantErr []string
	}{
		"only cert_file":    {Options{CertFile: certFile}, []string{"tls.cert_file is set but tls.key_file is not"}},
		"only key_file":     {Options{KeyFile: keyFile}, []string{"tls.key_file is set but tls.cert_file is not"}},
		"missing cert file": {Options{CertFile: filepath.Join(src, "nope.pem"), KeyFile: keyFile}, []string{"tls.cert_file", "nope.pem"}},
		"missing key file":  {Options{CertFile: certFile, KeyFile: filepath.Join(src, "nope.key")}, []string{"tls.key_file", "nope.key"}},
		"relative cert":     {Options{CertFile: "certs/server.pem", KeyFile: keyFile}, []string{"tls.cert_file", "must be absolute"}},
		"relative key":      {Options{CertFile: certFile, KeyFile: "server.key"}, []string{"tls.key_file", "must be absolute"}},
		"directory":         {Options{CertFile: src, KeyFile: keyFile}, []string{"tls.cert_file", "not a regular file"}},
		"key of another certificate": {
			Options{CertFile: certFile, KeyFile: filepath.Join(other, "localhost.key")},
			[]string{"tls.cert_file", "tls.key_file", "does not match"},
		},
		"not PEM":         {Options{CertFile: garbage, KeyFile: keyFile}, []string{"tls.cert_file", "tls.key_file"}},
		"with extra_sans": {Options{CertFile: certFile, KeyFile: keyFile, ExtraSANs: []string{"a.example"}}, []string{"tls.extra_sans", "tls.cert_file"}},
		"invalid extra":   {Options{ExtraSANs: []string{"*.example.com"}}, []string{"tls.extra_sans[0]", "wildcard"}},
	}
	for name, tc := range errCases {
		t.Run(name, func(t *testing.T) {
			tc.opts.Dir = t.TempDir()
			_, err := EnsureServerTLSConfig(tc.opts)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "secret-marker-123") || strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Errorf("error leaks file content: %q", err)
			}
		})
	}
}

// serveTLS serves a fixed body over TLS on a loopback port and returns the
// listener address. Any host name in a URL reaches it through dialer.
func serveTLS(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0), // expected handshake failures
	}
	go func() { _ = ServeWithTLS(srv, ln, cfg) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// clientFor returns an HTTP client that trusts roots and sends every
// connection to addr whatever host name the URL carries, the way a Docker
// service name or a tailnet name reaches the listener.
func clientFor(addr string, roots *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}
}

func get(c *http.Client, url string) error {
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func TestHandshakeVerifiesExtraNames(t *testing.T) {
	dir := t.TempDir()

	// Before: a client that trusts the CA cannot verify the other name.
	cfg, err := EnsureServerTLSConfig(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(readFile(t, filepath.Join(dir, "ca.pem")))
	addr := serveTLS(t, cfg)
	var unknown x509.HostnameError
	if err := get(clientFor(addr, roots), "https://mcp.internal/"); !errors.As(err, &unknown) {
		t.Fatalf("before extra_sans: err = %v, want a host name verification error", err)
	}

	// After: same CA file, regenerated leaf.
	cfg, err = EnsureServerTLSConfig(Options{Dir: dir, ExtraSANs: []string{"mcp.internal", "10.9.8.7"}})
	if err != nil {
		t.Fatal(err)
	}
	addr = serveTLS(t, cfg)
	c := clientFor(addr, roots)
	for _, url := range []string{"https://mcp.internal/", "https://10.9.8.7/", "https://localhost/", "https://127.0.0.1/"} {
		if err := get(c, url); err != nil {
			t.Errorf("GET %s: %v", url, err)
		}
	}
	if err := get(c, "https://not-listed.internal/"); !errors.As(err, &unknown) {
		t.Errorf("a name outside extra_sans: err = %v, want a host name verification error", err)
	}
}

func TestHandshakeProvidedCertificate(t *testing.T) {
	src := t.TempDir()
	roots := writeServerPair(t, src, "mcp.example.org")
	cfg, err := EnsureServerTLSConfig(Options{
		Dir:      filepath.Join(t.TempDir(), "unused"),
		CertFile: filepath.Join(src, "localhost.pem"),
		KeyFile:  filepath.Join(src, "localhost.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := clientFor(serveTLS(t, cfg), roots)
	if err := get(c, "https://mcp.example.org/"); err != nil {
		t.Errorf("provided certificate: %v", err)
	}
	var unknown x509.HostnameError
	if err := get(c, "https://other.example.org/"); !errors.As(err, &unknown) {
		t.Errorf("other name: err = %v, want a host name verification error", err)
	}
}
