package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// UX-02 cross-review r10: a review-bound server approval validates the
// reviewed fingerprints (definition + safety hints) in its commit, but the
// unquarantine that activates the tools ran later, outside that critical
// section. Annotations are not part of the approval hash, so a capture between
// the two that only flips a reviewed read-only tool to destructive left the
// record "approved" and the unquarantine activated the unreviewed version.
// The activation must revalidate the binding under the same lock as the
// quarantine flip and fail closed.
func TestServerApproval_AnnotationFlipBetweenCommitAndUnquarantineFailsClosed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"listen":"127.0.0.1:0","mcpServers":[{"name":"srv","protocol":"stdio","command":"true","enabled":true,"quarantined":true}]}`), 0o600))
	srvCfg := func() *config.ServerConfig {
		return &config.ServerConfig{Name: "srv", Protocol: "stdio", Command: "true", Enabled: true, Quarantined: true}
	}
	cfg := &config.Config{DataDir: dir, Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{srvCfg()}}
	rt, err := New(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.NoError(t, rt.storageManager.SaveUpstreamServer(srvCfg()))

	readOnly := func() []*config.ToolMetadata {
		tools := ux02Tools("srv", 2)
		for _, tl := range tools {
			tl.Annotations = &config.ToolAnnotations{ReadOnlyHint: boolP(true)}
		}
		return tools
	}
	_, err = rt.checkToolApprovals("srv", readOnly())
	require.NoError(t, err)
	reviewed := map[string]string{
		"read_000": reviewFingerprint(ux02Record(t, rt, "srv", "read_000")),
		"read_001": reviewFingerprint(ux02Record(t, rt, "srv", "read_001")),
	}
	n, err := rt.CommitServerApprovalDecision("srv", nil, reviewed, "api", func() error { return nil })
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// Between the commit and the unquarantine, a quarantined capture changes
	// ONLY read_001's safety hints to destructive.
	flipped := readOnly()
	flipped[1].Annotations = &config.ToolAnnotations{DestructiveHint: boolP(true)}
	_, err = rt.checkToolApprovals("srv", flipped)
	require.NoError(t, err)
	rec := ux02Record(t, rt, "srv", "read_001")
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "annotations stay outside the approval hash")
	require.NotEqual(t, reviewed["read_001"], reviewFingerprint(rec))

	err = rt.UnquarantineServerKeepingToolDecisions("srv", nil, reviewed)
	var stale *storage.StaleToolReviewError
	require.True(t, errors.As(err, &stale), "activation of an unreviewed version must fail closed, got %v", err)
	require.Equal(t, []string{"read_001"}, stale.Changed)
	require.True(t, stale.AtActivation)
	require.Contains(t, err.Error(), "the server stays quarantined")
	srv, err := rt.storageManager.GetUpstreamServer("srv")
	require.NoError(t, err)
	require.True(t, srv.Quarantined, "the server must stay quarantined")

	// Re-reviewing the current hints makes the approval current again.
	reviewed["read_001"] = reviewFingerprint(rec)
	require.NoError(t, rt.UnquarantineServerKeepingToolDecisions("srv", nil, reviewed))
	srv, err = rt.storageManager.GetUpstreamServer("srv")
	require.NoError(t, err)
	require.False(t, srv.Quarantined)
}
