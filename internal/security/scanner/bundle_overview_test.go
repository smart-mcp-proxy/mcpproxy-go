package scanner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestApplySecurityConfig_ConfiguresBundlePath wires spec 086 FR-019 end to end
// at the service seam: the configured path is honoured, and re-applying a
// different config swaps the corpus on the SAME live service (hot-reload, no
// restart).
func TestApplySecurityConfig_ConfiguresBundlePath(t *testing.T) {
	restoreEmbeddedBundle(t)
	svc, _, _ := newTestService(t)
	path := writeBundle(t, miniBundle)

	svc.ApplySecurityConfig(&config.SecurityConfig{TPABundlePath: path})
	info := svc.BundleStatus()
	assert.Equal(t, BundleSourceFile, info.Source)
	assert.Equal(t, path, info.Path)
	assert.Equal(t, 1, info.RunnableRules)

	// Hot-reload back to the embedded corpus without recreating the service.
	svc.ApplySecurityConfig(&config.SecurityConfig{})
	assert.Equal(t, BundleSourceEmbedded, svc.BundleStatus().Source)

	// A nil security block is nil-safe and means "embedded".
	svc.ApplySecurityConfig(nil)
	assert.Equal(t, BundleSourceEmbedded, svc.BundleStatus().Source)
}

// TestGetOverview_ReportsSignatureBundle is the operator-visible half of GH
// #938 finding 2: `security overview` (CLI + REST) must answer "which
// signatures is my proxy running, and how old are they?".
func TestGetOverview_ReportsSignatureBundle(t *testing.T) {
	restoreEmbeddedBundle(t)
	svc, _, _ := newTestService(t)
	ConfigureBundle("", zap.NewNop())

	overview, err := svc.GetOverview(context.Background())
	require.NoError(t, err)
	require.NotNil(t, overview.SignatureBundle, "the overview must carry the signature-bundle descriptor")
	assert.Equal(t, BundleSourceEmbedded, overview.SignatureBundle.Source)
	assert.Positive(t, overview.SignatureBundle.RunnableRules)
	assert.NotEmpty(t, overview.SignatureBundle.Fingerprint)
}
