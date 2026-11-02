package tlslocal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

type Options struct {
	Dir               string // e.g., ~/.mcpproxy/certs
	RequireClientCert bool

	// ExtraSANs are DNS names and IP literals added to the generated server
	// certificate (tls.extra_sans). Ignored when CertFile is set.
	ExtraSANs []string
	// CertFile and KeyFile (tls.cert_file, tls.key_file) name an operator-
	// provided certificate and key, served instead of a generated one. Both or
	// neither.
	CertFile string
	KeyFile  string
}

func EnsureServerTLSConfig(opts Options) (*tls.Config, error) {
	if (opts.CertFile == "") != (opts.KeyFile == "") {
		if opts.CertFile == "" {
			return nil, errors.New("tls.key_file is set but tls.cert_file is not; set both or neither")
		}
		return nil, errors.New("tls.cert_file is set but tls.key_file is not; set both or neither")
	}
	provided := opts.CertFile != ""
	if provided && len(opts.ExtraSANs) > 0 {
		return nil, errors.New("tls.extra_sans cannot be combined with tls.cert_file: the provided certificate decides the names")
	}
	extraDNS, extraIPs, err := ParseSANs(opts.ExtraSANs)
	if err != nil {
		return nil, fmt.Errorf("tls.%w", err)
	}

	if opts.Dir == "" {
		home, _ := os.UserHomeDir()
		opts.Dir = filepath.Join(home, ".mcpproxy", "certs")
	}

	caCrt := filepath.Join(opts.Dir, "ca.pem")
	caKey := filepath.Join(opts.Dir, "ca.key")
	srvCrt := filepath.Join(opts.Dir, "localhost.pem")
	srvKey := filepath.Join(opts.Dir, "localhost.key")

	// A provided certificate needs no local CA, unless mTLS verifies client
	// certificates against it.
	if !provided || opts.RequireClientCert {
		if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
			return nil, err
		}
		if !exists(caCrt) || !exists(caKey) {
			if err := genLocalCA(caCrt, caKey); err != nil {
				return nil, fmt.Errorf("generate CA: %w", err)
			}
		}
	}

	var cert tls.Certificate
	if provided {
		cert, err = loadProvidedKeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, err
		}
	} else {
		regen := !exists(srvCrt) || !exists(srvKey)
		if !regen {
			regen, err = serverCertNeedsRegen(srvCrt, caCrt, extraDNS, extraIPs)
			if err != nil {
				return nil, err
			}
		}
		if regen {
			if err := genServerCert(caCrt, caKey, srvCrt, srvKey, extraDNS, extraIPs); err != nil {
				return nil, fmt.Errorf("generate localhost cert: %w", err)
			}
		}
		cert, err = tls.LoadX509KeyPair(srvCrt, srvKey)
		if err != nil {
			return nil, err
		}
	}

	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if !provided || opts.RequireClientCert {
		caPool, err := loadPool(caCrt)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = caPool
	}
	if opts.RequireClientCert {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// loadProvidedKeyPair reads tls.cert_file and tls.key_file with the secret
// file rules (absolute path, regular file, size cap) and checks that the key
// belongs to the certificate. Errors name the setting and never include file
// content. A chain is served as it is in the file, leaf first.
func loadProvidedKeyPair(certFile, keyFile string) (tls.Certificate, error) {
	certPEM, err := secret.ReadFile(certFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls.cert_file: %w", err)
	}
	keyPEM, err := secret.ReadFile(keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls.key_file: %w", err)
	}
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls.cert_file %q and tls.key_file %q: %w", certFile, keyFile, err)
	}
	return cert, nil
}

// serverCertNeedsRegen reports whether the existing generated server
// certificate must be replaced to carry exactly localhost, 127.0.0.1, ::1 and
// the extra names. A certificate that already does is left alone, so a restart
// with an unchanged list rewrites nothing. A certificate with other names that
// the local CA did not sign is not overwritten: it is most likely one the
// operator put there, so the error points at tls.cert_file instead. With no
// extra names such a certificate is served as it is, as before extra_sans
// existed; a local-CA leaf that still carries names removed from the list is
// replaced by a plain localhost one.
func serverCertNeedsRegen(srvCrt, caCrt string, extraDNS []string, extraIPs []net.IP) (bool, error) {
	noExtras := len(extraDNS)+len(extraIPs) == 0
	leaf, err := readCert(srvCrt)
	if err != nil {
		if noExtras {
			return false, nil
		}
		return false, err
	}
	want := sanSet(append(append([]string{}, defaultDNSNames...), extraDNS...), append(append([]net.IP{}, defaultIPs...), extraIPs...))
	if sameSet(sanSet(leaf.DNSNames, leaf.IPAddresses), want) {
		return false, nil
	}
	ca, err := readCert(caCrt)
	if err != nil {
		if noExtras {
			return false, nil
		}
		return false, err
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		if noExtras {
			return false, nil
		}
		return false, fmt.Errorf("%s was not issued by the local CA and does not carry tls.extra_sans; "+
			"set tls.cert_file and tls.key_file to serve it, or remove %s and its key to have a new one generated", srvCrt, srvCrt)
	}
	return true, nil
}

func readCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM certificate found", path)
	}
	return x509.ParseCertificate(blk.Bytes)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func genLocalCA(crtPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          bigIntNow(),
		Subject:               pkix.Name{CommonName: "mcpproxy local CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	return writeCertKey(crtPath, keyPath, der, priv)
}

func genServerCert(caCrt, caKey, crtPath, keyPath string, extraDNS []string, extraIPs []net.IP) error {
	capem, err := os.ReadFile(caCrt)
	if err != nil {
		return err
	}
	cakey, err := os.ReadFile(caKey)
	if err != nil {
		return err
	}
	cb, _ := pem.Decode(capem)
	kb, _ := pem.Decode(cakey)
	if cb == nil || kb == nil {
		return errors.New("invalid CA files")
	}
	ca, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return err
	}
	caPriv, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: bigIntNow(),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     append(append([]string{}, defaultDNSNames...), extraDNS...),
		IPAddresses:  append(append([]net.IP{}, defaultIPs...), extraIPs...),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &leafKey.PublicKey, caPriv)
	if err != nil {
		return err
	}
	return writeCertKey(crtPath, keyPath, der, leafKey)
}

func writeCertKey(crtPath, keyPath string, certDER []byte, priv *ecdsa.PrivateKey) error {
	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	// Key first: a crash between the two writes leaves the old certificate
	// with a new key, which the next start finds lacking the configured names
	// and replaces, instead of a new certificate nothing can load.
	if err := writeFileAtomic(keyPath, key); err != nil {
		return err
	}
	return writeFileAtomic(crtPath, crt)
}

// writeFileAtomic writes data through a temporary file in the
// same directory (CreateTemp creates it 0600) and renames it into place, so a reader never sees a partial
// file and an existing file is replaced whole.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func loadPool(caPath string) (*x509.CertPool, error) {
	b, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(b) {
		return nil, errors.New("append CA failed")
	}
	return p, nil
}

func bigIntNow() *big.Int { return new(big.Int).SetInt64(time.Now().UnixNano()) }
