package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tlsFieldErrors(t *testing.T, tls *TLSConfig) map[string]string {
	t.Helper()
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.TLS = tls
	out := map[string]string{}
	for _, e := range cfg.ValidateDetailed() {
		if len(e.Field) >= 4 && e.Field[:4] == "tls." {
			out[e.Field] = e.Message
		}
	}
	return out
}

func TestValidateTLSCertSettings(t *testing.T) {
	abs := func(name string) string { return filepath.Join(t.TempDir(), name) }

	t.Run("valid extra_sans", func(t *testing.T) {
		assert.Empty(t, tlsFieldErrors(t, &TLSConfig{Enabled: true, ExtraSANs: []string{"mcp.internal", "10.0.0.5", "fd7a::1"}}))
	})
	t.Run("valid cert_file and key_file", func(t *testing.T) {
		// Readability is checked when the server starts, not here.
		assert.Empty(t, tlsFieldErrors(t, &TLSConfig{Enabled: true, CertFile: abs("c.pem"), KeyFile: abs("k.pem")}))
	})
	t.Run("nothing set", func(t *testing.T) {
		assert.Empty(t, tlsFieldErrors(t, &TLSConfig{Enabled: true}))
	})
	t.Run("not checked while TLS is off", func(t *testing.T) {
		assert.Empty(t, tlsFieldErrors(t, &TLSConfig{ExtraSANs: []string{"*.x"}, CertFile: "rel.pem"}))
	})

	t.Run("invalid extra_sans entries", func(t *testing.T) {
		for _, bad := range []string{"", "*.example.com", "https://mcp.example.com", "host:8080"} {
			errs := tlsFieldErrors(t, &TLSConfig{Enabled: true, ExtraSANs: []string{"ok.example", bad}})
			require.Contains(t, errs, "tls.extra_sans", "entry %q", bad)
			assert.Contains(t, errs["tls.extra_sans"], "extra_sans[1]")
		}
	})
	t.Run("only one of cert_file and key_file", func(t *testing.T) {
		errs := tlsFieldErrors(t, &TLSConfig{Enabled: true, CertFile: abs("c.pem")})
		assert.Equal(t, "requires tls.key_file; set both or neither", errs["tls.cert_file"])
		errs = tlsFieldErrors(t, &TLSConfig{Enabled: true, KeyFile: abs("k.pem")})
		assert.Equal(t, "requires tls.cert_file; set both or neither", errs["tls.key_file"])
	})
	t.Run("relative paths", func(t *testing.T) {
		errs := tlsFieldErrors(t, &TLSConfig{Enabled: true, CertFile: "certs/c.pem", KeyFile: "k.pem"})
		assert.Contains(t, errs["tls.cert_file"], "must be absolute")
		assert.Contains(t, errs["tls.key_file"], "must be absolute")
	})
	t.Run("extra_sans with cert_file", func(t *testing.T) {
		errs := tlsFieldErrors(t, &TLSConfig{Enabled: true, ExtraSANs: []string{"a.example"}, CertFile: abs("c.pem"), KeyFile: abs("k.pem")})
		assert.Contains(t, errs["tls.extra_sans"], "tls.cert_file")
	})
}

func TestTLSCertSettingsLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")

	// Built with json.Marshal so a Windows path is escaped correctly.
	write := func(t *testing.T, tls map[string]any) string {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"listen": "127.0.0.1:0", "data_dir": dir, "tls": tls})
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "mcp_config.json")
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		return path
	}

	t.Run("file values load", func(t *testing.T) {
		cfg, err := LoadFromFile(write(t, map[string]any{"enabled": true, "extra_sans": []string{"mcp.internal", "10.0.0.5"}}))
		require.NoError(t, err)
		assert.Equal(t, []string{"mcp.internal", "10.0.0.5"}, cfg.TLS.ExtraSANs)

		cfg, err = LoadFromFile(write(t, map[string]any{"enabled": true, "cert_file": certFile, "key_file": keyFile}))
		require.NoError(t, err)
		assert.Equal(t, certFile, cfg.TLS.CertFile)
		assert.Equal(t, keyFile, cfg.TLS.KeyFile)
	})

	t.Run("a bad value stops the load and names the setting", func(t *testing.T) {
		_, err := LoadFromFile(write(t, map[string]any{"enabled": true, "extra_sans": []string{"*.example.com"}}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tls.extra_sans")

		_, err = LoadFromFile(write(t, map[string]any{"enabled": true, "cert_file": certFile}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tls.cert_file")
	})

	t.Run("env wins over file and is not persisted", func(t *testing.T) {
		t.Cleanup(ResetProcessOverrides)
		path := write(t, map[string]any{"enabled": true, "extra_sans": []string{"file.example"}})
		t.Setenv("MCPPROXY_TLS_EXTRA_SANS", "a.example, 10.0.0.9 ,")
		cfg, err := LoadFromFile(path)
		require.NoError(t, err)
		assert.Equal(t, []string{"a.example", "10.0.0.9"}, cfg.TLS.ExtraSANs)

		persisted := PersistableConfig(cfg, path)
		assert.Equal(t, []string{"file.example"}, persisted.TLS.ExtraSANs)
	})

	t.Run("cert and key from env", func(t *testing.T) {
		t.Cleanup(ResetProcessOverrides)
		t.Setenv("MCPPROXY_TLS_CERT_FILE", certFile)
		t.Setenv("MCPPROXY_TLS_KEY_FILE", keyFile)
		cfg, err := LoadFromFile(write(t, map[string]any{"enabled": true}))
		require.NoError(t, err)
		assert.Equal(t, certFile, cfg.TLS.CertFile)
		assert.Equal(t, keyFile, cfg.TLS.KeyFile)
	})

	t.Run("only one from env is refused", func(t *testing.T) {
		t.Cleanup(ResetProcessOverrides)
		t.Setenv("MCPPROXY_TLS_CERT_FILE", certFile)
		_, err := LoadFromFile(write(t, map[string]any{"enabled": true}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tls.cert_file")
	})

	t.Run("env unset keeps defaults", func(t *testing.T) {
		cfg, err := LoadFromFile(write(t, map[string]any{"enabled": true}))
		require.NoError(t, err)
		assert.Empty(t, cfg.TLS.ExtraSANs)
		assert.Empty(t, cfg.TLS.CertFile)
		assert.Empty(t, cfg.TLS.KeyFile)
	})
}
