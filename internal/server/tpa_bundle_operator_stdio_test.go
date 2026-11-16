package server

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// operatorSignedBundle renders the embedded seed corpus as an operator manifest
// naming keyID, at the given sequence and trust epoch 0. It reuses the full
// corpus because the activation self-check (Spec 101 US3) scores every
// candidate on the eval corpus: a reduced operator corpus would miss the seed
// signatures' samples and be refused as gate_failure, which is not what this
// transport test is about.
func operatorSignedBundle(t *testing.T, keyID string, seq uint64) []byte {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(scanner.EmbeddedBundle(), &m))
	m["key_id"] = keyID
	m["sequence"] = strconv.FormatUint(seq, 10)
	m["trust_epoch"] = 0
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return out
}

// TestTPAOperatorSignedBundleActivatesInStdioMode: a stdio-mode server (no
// listen) with an operator-signed bundle configured through
// security.tpa_active_root / tpa_operator_signers / tpa_operator_baseline
// activates it with signature_verified=true (Spec 101 US1). The wiring is
// transport-independent for the same reason as FR-019 in
// tpa_bundle_transport_test.go.
func TestTPAOperatorSignedBundleActivatesInStdioMode(t *testing.T) {
	t.Cleanup(func() { scanner.ConfigureBundle("", zap.NewNop()) })

	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	pub := priv.Public().(ed25519.PublicKey)

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "scanner-bundle.json")
	bundle := operatorSignedBundle(t, bundlesig.KeyID(pub), 5)
	require.NoError(t, os.WriteFile(bundlePath, bundle, 0o600))
	require.NoError(t, os.WriteFile(bundlePath+".sig", bundlesig.SignSidecar(priv, bundle), 0o600))

	baseline := uint64(1)
	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Listen = "" // stdio transport
	cfg.Security = &config.SecurityConfig{
		TPABundlePath:       bundlePath,
		TPAActiveRoot:       config.TPARootOperator,
		TPAOperatorSigners:  []string{base64.StdEncoding.EncodeToString(pub)},
		TPAOperatorBaseline: &baseline,
	}

	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	info := scanner.BundleStatus()
	assert.Equal(t, scanner.BundleSourceFile, info.Source)
	assert.Equal(t, bundlePath, info.Path)
	assert.Equal(t, scanner.AuthorityOperator, info.Authority)
	assert.True(t, info.SignatureVerified, "an operator-signed bundle must verify")
	assert.Equal(t, "5", info.Sequence)
	assert.Empty(t, info.LoadError)
	assert.False(t, info.CoverageDegraded, "reason %q", info.DegradedReason)

	// The telemetry provider is wired on this transport too and, because the
	// authority is the operator's, reports "other" with no sequence.
	ts := srv.runtime.TelemetryService()
	require.NotNil(t, ts)
	payload := ts.BuildPayload()
	require.NotNil(t, payload.TPAScanner)
	assert.Equal(t, "file", payload.TPAScanner.BundleSource)
	assert.Equal(t, "other", payload.TPAScanner.BundleVersion)
	assert.Nil(t, payload.TPAScanner.BundleSequence)
}
