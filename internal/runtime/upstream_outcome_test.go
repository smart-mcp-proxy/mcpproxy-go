package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Spec 126 FR-035 to FR-038 (T082): the runtime side of the completion
// counters — the starter classifier, the outcome entry point, and the
// telemetry wiring (install anchor provider, first-window coverage).

type stubStarter struct {
	name string
	inc  int64
}

func (s stubStarter) IsStarter(name string, inc int64) bool { return name == s.name && inc == s.inc }

func newOutcomeRuntime(t *testing.T, dataDir string, telemetryOn bool) *Runtime {
	t.Helper()
	cfg := newPreChurnTestConfig(dataDir)
	cfg.Telemetry = &config.TelemetryConfig{Enabled: &telemetryOn}
	rt, err := New(cfg, filepath.Join(dataDir, "config.json"), zap.NewNop())
	require.NoError(t, err)
	return rt
}

// FR-037: in PR-A the default classifier answers false for every server.
func TestStarterClassifier_DefaultAnswersFalse(t *testing.T) {
	rt := newOutcomeRuntime(t, t.TempDir(), true)
	defer rt.Close()
	c := rt.StarterClassifier()
	require.NotNil(t, c)
	require.False(t, c.IsStarter("time", 1))
	require.False(t, c.IsStarter("", 0))

	rt.SetStarterClassifier(stubStarter{name: "time", inc: 3})
	require.True(t, rt.StarterClassifier().IsStarter("time", 3))
	require.False(t, rt.StarterClassifier().IsStarter("time", 4), "a different incarnation is not the starter")
	rt.SetStarterClassifier(nil)
	require.False(t, rt.StarterClassifier().IsStarter("time", 3), "nil restores the default")
}

// One outcome per call reaches the counter window and the lifetime scope; a
// starter success is excluded from succeeded_non_starter.
func TestRecordUpstreamOutcome_CountsAndScope(t *testing.T) {
	rt := newOutcomeRuntime(t, t.TempDir(), true)
	defer rt.Close()
	rt.SetTelemetry("v0.0.0-test", "personal")
	rt.SetStarterClassifier(stubStarter{name: "time", inc: 1})
	db := rt.TelemetryService().ActivationDB()

	rt.RecordUpstreamOutcome("time", 1, true)
	got := rt.TelemetryRegistry().Snapshot().UpstreamOutcomes
	require.Equal(t, "1-10", got.Succeeded)
	require.Equal(t, "0", got.SucceededNonStarter, "the starter's success is not non-starter traffic")
	scope, err := telemetry.LoadSuccessScope(db)
	require.NoError(t, err)
	require.Equal(t, telemetry.SuccessScopeStarterOnly, scope)

	rt.RecordUpstreamOutcome("time", 2, true) // a user's own re-add: another incarnation
	got = rt.TelemetryRegistry().Snapshot().UpstreamOutcomes
	require.Equal(t, "1-10", got.SucceededNonStarter)
	scope, err = telemetry.LoadSuccessScope(db)
	require.NoError(t, err)
	require.Equal(t, telemetry.SuccessScopeNonStarter, scope)

	rt.RecordUpstreamOutcome("github", 1, false)
	require.Equal(t, "1-10", rt.TelemetryRegistry().Snapshot().UpstreamOutcomes.Unusable)
	scope, err = telemetry.LoadSuccessScope(db)
	require.NoError(t, err)
	require.Equal(t, telemetry.SuccessScopeNonStarter, scope, "an unusable result never moves the scope")
}

// FR-038 / T080: the scope is recorded with telemetry disabled.
func TestRecordUpstreamOutcome_ScopeRecordedWithTelemetryOff(t *testing.T) {
	rt := newOutcomeRuntime(t, t.TempDir(), false)
	defer rt.Close()
	rt.SetTelemetry("v0.0.0-test", "personal")
	rt.RecordUpstreamOutcome("github", 1, true)
	scope, err := telemetry.LoadSuccessScope(rt.TelemetryService().ActivationDB())
	require.NoError(t, err)
	require.Equal(t, telemetry.SuccessScopeNonStarter, scope)
}

// Before telemetry is wired the call is a no-op (no panic).
func TestRecordUpstreamOutcome_NoTelemetryService(t *testing.T) {
	rt := newOutcomeRuntime(t, t.TempDir(), true)
	defer rt.Close()
	rt.RecordUpstreamOutcome("github", 1, true)
	var nilRT *Runtime
	nilRT.RecordUpstreamOutcome("github", 1, true)
}

// SetTelemetry wires the install anchor into day_two and the first window's
// coverage from the store's state at open (FR-034, FR-036).
func TestSetTelemetry_WiresDayTwoAndCoverage(t *testing.T) {
	t.Run("fresh install", func(t *testing.T) {
		// The real startup order on an empty data directory: New records the
		// anchor right after the store opens, SetTelemetry reads it once.
		rt := newOutcomeRuntime(t, t.TempDir(), true)
		defer rt.Close()
		require.True(t, rt.StorageManager().WasEmptyAtOpen())
		_, ok, err := rt.StorageManager().InstallAnchor()
		require.NoError(t, err)
		require.True(t, ok, "runtime.New records the anchor on an empty store")
		rt.SetTelemetry("v0.0.0-test", "personal")

		p := rt.TelemetryService().BuildPayload()
		require.NotNil(t, p.DayTwo)
		require.Equal(t, "fresh", p.DayTwo.InstallOrigin)
		require.Equal(t, "d0_6", p.DayTwo.InstallAge)
		require.Equal(t, "none", p.DayTwo.SuccessScopeEver)
		out := rt.TelemetryRegistry().Snapshot().UpstreamOutcomes
		require.Equal(t, telemetry.OutcomeCoverageFull, out.Coverage)
		require.Equal(t, "d0_6", out.WindowAge)
	})
	t.Run("existing install, first start of this build", func(t *testing.T) {
		dir := t.TempDir()
		// An older build's store: data, no anchor, no marker.
		older, err := storage.NewManager(dir, zap.NewNop().Sugar())
		require.NoError(t, err)
		require.NoError(t, older.GetDB().Update(func(tx *bbolt.Tx) error {
			return tx.Bucket([]byte(storage.UpstreamsBucket)).Put([]byte("github"), []byte(`{"name":"github"}`))
		}))
		require.NoError(t, older.Close())

		rt := newOutcomeRuntime(t, dir, true)
		defer rt.Close()
		require.False(t, rt.StorageManager().WasEmptyAtOpen())
		_, ok, err := rt.StorageManager().InstallAnchor()
		require.NoError(t, err)
		require.False(t, ok, "an upgraded store never gets an anchor")
		rt.SetTelemetry("v0.0.0-test", "personal")

		p := rt.TelemetryService().BuildPayload()
		require.NotNil(t, p.DayTwo)
		require.Equal(t, "existing", p.DayTwo.InstallOrigin)
		require.Empty(t, p.DayTwo.InstallAge)
		out := rt.TelemetryRegistry().Snapshot().UpstreamOutcomes
		require.Equal(t, telemetry.OutcomeCoveragePartial, out.Coverage)
		require.Empty(t, out.WindowAge)
	})
	t.Run("server edition", func(t *testing.T) {
		rt := newOutcomeRuntime(t, t.TempDir(), true)
		defer rt.Close()
		rt.SetTelemetry("v0.0.0-test", "server")
		require.Nil(t, rt.TelemetryService().BuildPayload().DayTwo)
		require.Empty(t, rt.TelemetryRegistry().Snapshot().UpstreamOutcomes.WindowAge)
	})
}

// FR-035: replay stays outside both the dispatch and the outcome counts, as
// it is today. Replay is an operator re-run of a recorded call, not an agent's
// logical call, so ReplayToolCallWithOptions must reach no telemetry counter.
func TestReplayToolCall_RecordsNoUpstreamOutcome(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runtime.go", nil, 0)
	require.NoError(t, err)
	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ReplayToolCallWithOptions" || fn.Body == nil {
			return true
		}
		body = fn.Body
		return false
	})
	require.NotNil(t, body, "ReplayToolCallWithOptions not found in runtime.go")
	forbidden := map[string]bool{
		"RecordUpstreamOutcome":   true,
		"RecordUpstreamOutcomeOn": true,
		"RecordUpstreamTool":      true,
		"RecordUpstreamToolOn":    true,
		"RecordSuccessScope":      true,
		"TelemetryRegistry":       true,
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && forbidden[sel.Sel.Name] {
			t.Errorf("ReplayToolCallWithOptions calls %s: replay must record no dispatch or outcome", sel.Sel.Name)
		}
		return true
	})
}
