package server

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// operatorSignedBundle renders a one-rule corpus whose manifest names keyID, at
// the given sequence and trust epoch 0.
func operatorSignedBundle(keyID string, seq uint64) []byte {
	return []byte(fmt.Sprintf(`{"bundle_version":"0.1.0","key_id":%q,"rules":[{"category":"rug-pull","confidence":0.9,"detector":"op","engine":"regex","id":"TPA-2099-9002","level":"high","pattern":"operator-bundle-canary","target":"tool_description"}],"schema_version":"0.1.0","sequence":"%d","signature_count":1,"trust_epoch":0}`, keyID, seq))
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
	bundle := operatorSignedBundle(bundlesig.KeyID(pub), 5)
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
