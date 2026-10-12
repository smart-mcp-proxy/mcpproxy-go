package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// Spec 116 PR2 surface tests (quickstart.md §T2): removal through REST and MCP
// purges everything keyed by the server name, and a re-add of the same name
// starts clean; approval writers bound to the removed incarnation write
// nothing on the replacement.

func pr2Manual(name string, quarantined bool) *config.ServerConfig {
	sc := &config.ServerConfig{Name: name, Command: "true", Protocol: "stdio", Enabled: false,
		TrustMode: string(config.TrustModeManual), Quarantined: quarantined}
	sc.MarkQuarantineExplicitlySet(true)
	return sc
}

func pr2ParkOnce(name string) (hook func(string, int64), entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	hook = func(server string, _ int64) {
		if server != name {
			return
		}
		park := false
		once.Do(func() { park = true })
		if park {
			close(entered)
			<-release
		}
	}
	return hook, entered, release
}

// T2.2 (with T2.1 for REST and MCP): a different "acme" added under manual
// trust after the removal inherits nothing: no tool, prompt or OAuth record,
// no scan report or baseline, and an identical prompt is pending and withheld.
func TestReAdd_T2_2_StartsClean(t *testing.T) {
	const prompt, desc = "greet", "Say hi"
	for _, path := range []string{"rest", "mcp"} {
		t.Run(path, func(t *testing.T) {
			srv, _ := newSpec116Server(t, pr2Manual("acme", false), pr2Manual("acme_x", false))
			sm := srv.runtime.StorageManager()
			purgetest.Seed(t, sm, "acme")
			hash := calculatePromptApprovalHash(prompt, desc, promptArgsJSON(nil))
			require.NoError(t, sm.SavePromptApproval(&storage.PromptApprovalRecord{
				ServerName: "acme", PromptName: prompt, Status: promptStatusApproved,
				ApprovedHash: hash, CurrentHash: hash, ApprovedAt: time.Now(), ApprovedBy: "seed",
			}))
			purgetest.AssertSeeded(t, sm, "acme")

			switch path {
			case "rest":
				require.NoError(t, srv.RemoveServer(context.Background(), "acme"))
			case "mcp":
				res, err := srv.mcpProxy.handleRemoveUpstream(context.Background(), mcpCall(map[string]interface{}{"name": "acme"}))
				require.NoError(t, err)
				require.False(t, res.IsError, "MCP remove failed: %v", res.Content)
			}
			purgetest.AssertPurged(t, sm, "acme", path)

			require.NoError(t, srv.AddServer(context.Background(), pr2Manual("acme", false)))
			c := purgetest.Counts(t, sm, "acme")
			delete(c, purgetest.Upstream)
			for bucket, n := range c {
				assert.Zero(t, n, "%s: the re-added server inherited %d key(s) in %s", path, n, bucket)
			}

			got := srv.mcpProxy.checkPromptApprovals([]mcp.Prompt{{Name: "acme:" + prompt, Description: desc}})
			_, withheld := got.blocked["acme:"+prompt]
			assert.True(t, withheld, "an identical prompt on the replacement must be withheld for review")
			rec, err := sm.GetPromptApproval("acme", prompt)
			require.NoError(t, err)
			assert.Equal(t, promptStatusPending, rec.Status)
		})
	}
}

// T054: a prompt approval parked after it captured the incarnation, released
// after a remove and re-add, writes nothing and reports server_replaced.
func TestPromptApproval_T054_BoundToIncarnation(t *testing.T) {
	srv, _ := newSpec116Server(t, pr2Manual("acme", false))
	sm := srv.runtime.StorageManager()
	pending := func() {
		require.NoError(t, sm.SavePromptApproval(&storage.PromptApprovalRecord{
			ServerName: "acme", PromptName: "greet", Status: promptStatusPending, CurrentHash: "h",
		}))
	}
	pending()
	hook, entered, release := pr2ParkOnce("acme")
	srv.runtime.SetApprovalAfterCaptureHookForTest(hook)
	done := make(chan error, 1)
	go func() { done <- srv.mcpProxy.ApprovePrompt("acme", "greet", "mcp") }()
	awaitSignal(t, entered, "the prompt approval to park after its capture")

	require.NoError(t, srv.RemoveServer(context.Background(), "acme"))
	require.NoError(t, srv.AddServer(context.Background(), pr2Manual("acme", false)))
	pending()
	close(release)
	err := awaitErr(t, done, "ApprovePrompt")
	require.ErrorIs(t, err, runtime.ErrServerReplaced)
	rec, gerr := sm.GetPromptApproval("acme", "greet")
	require.NoError(t, gerr)
	assert.Equal(t, promptStatusPending, rec.Status)
}

// T2.5 (review R5.6): a scanner approval parked after it captured the
// incarnation does not block a remove and re-add; released, it fails with
// server_replaced, the replacement stays quarantined and inherits no
// baseline. Since Spec 117 PR-2 the runtime's reviewed approval is one
// locked commit section (baseline, promotion and unquarantine together), so
// there is no longer a window between the baseline save and the unquarantine
// to park in; the park point is the capture, and the incarnation is
// re-checked under the approval lock before anything is recorded. (The
// scanner's baseline-to-unquarantine seam only exists on the legacy path for
// unquarantiners without the reviewed capability.)
func TestScannerApproval_T2_5_ReAddKeepsReplacementQuarantined(t *testing.T) {
	srv, _ := newSpec116Server(t, pr2Manual("acme", true))
	dir := t.TempDir()
	logger := zap.NewNop()
	svc := scanner.NewService(srv.runtime.StorageManager(), scanner.NewRegistry(dir, logger), scanner.NewDockerRunner(logger), dir, logger)
	svc.SetServerUnquarantiner(&serverUnquarantinerAdapter{server: srv})
	hook, entered, release := pr2ParkOnce("acme")
	srv.runtime.SetApprovalAfterCaptureHookForTest(hook)

	done := make(chan error, 1)
	go func() { done <- svc.ApproveServer(context.Background(), "acme", true, "api") }()
	awaitSignal(t, entered, "the scanner approval to park after capturing the incarnation")

	readd := make(chan error, 1)
	go func() {
		if err := srv.RemoveServer(context.Background(), "acme"); err != nil {
			readd <- err
			return
		}
		readd <- srv.AddServer(context.Background(), pr2Manual("acme", true))
	}()
	select {
	case err := <-readd:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("remove and re-add must not wait for the parked approval (it holds no lock while parked)")
	}
	assert.Zero(t, srv.runtime.ApprovalLockWaitersForTest("acme"))
	close(release)
	err := awaitErr(t, done, "ApproveServer")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "server_replaced"), "got %v", err)
	assert.True(t, liveServer(srv, "acme").Quarantined, "the replacement stays quarantined")
	assert.Zero(t, purgetest.Counts(t, srv.runtime.StorageManager(), "acme")[purgetest.Baselines],
		"the replacement inherits no baseline")
}

// FR-017: the server payloads expose the committed generation and incarnation.
func TestServersPayload_FR017_GenerationAndIncarnation(t *testing.T) {
	srv, _ := newSpec116Server(t, pr2Manual("acme", false))
	sg, ok := srv.runtime.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	servers, err := srv.runtime.GetAllServers()
	require.NoError(t, err)
	var found bool
	for _, m := range servers {
		if m["name"] == "acme" {
			found = true
			assert.EqualValues(t, sg.Gen, m["generation"])
			assert.EqualValues(t, sg.Incarnation, m["incarnation"])
		}
	}
	require.True(t, found)
}
