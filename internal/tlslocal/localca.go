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
)

type Options struct {
	Dir               string // e.g., ~/.mcpproxy/certs
	RequireClientCert bool
}

func EnsureServerTLSConfig(opts Options) (*tls.Config, error) {
	if opts.Dir == "" {
		home, _ := os.UserHomeDir()
		opts.Dir = filepath.Join(home, ".mcpproxy", "certs")
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}

	caCrt := filepath.Join(opts.Dir, "ca.pem")
	caKey := filepath.Join(opts.Dir, "ca.key")
	srvCrt := filepath.Join(opts.Dir, "localhost.pem")
	srvKey := filepath.Join(opts.Dir, "localhost.key")

	if !exists(caCrt) || !exists(caKey) {
		if err := genLocalCA(caCrt, caKey); err != nil {
			return nil, fmt.Errorf("generate CA: %w", err)
		}
	}
	if !exists(srvCrt) || !exists(srvKey) {
		if err := genServerCert(caCrt, caKey, srvCrt, srvKey); err != nil {
			return nil, fmt.Errorf("generate localhost cert: %w", err)
		}
	}

	cert, err := tls.LoadX509KeyPair(srvCrt, srvKey)
	if err != nil {
		return nil, err
	}
	caPool, err := loadPool(caCrt)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if opts.RequireClientCert {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
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

func genServerCert(caCrt, caKey, crtPath, keyPath string) error {
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
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
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
	if err := os.WriteFile(crtPath, crt, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return err
	}
	return nil
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
