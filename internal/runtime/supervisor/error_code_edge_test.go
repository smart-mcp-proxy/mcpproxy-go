package supervisor

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// newEdgeTestSupervisor builds a bare supervisor plus a notification recorder.
// It deliberately mirrors TestSupervisor_ErrorCodeNotifierSynchronous rather
// than driving reconcile(): MockUpstreamAdapter.AddServer creates a
// ServerState with a nil ConnectionInfo, so a reconcile-driven test never
// reaches classifyAndAttach and would pass vacuously.
func newEdgeTestSupervisor(t *testing.T) (*Supervisor, *[]string) {
	t.Helper()

	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	t.Cleanup(func() { configSvc.Close() })

	mockUpstream := NewMockUpstreamAdapter()
	t.Cleanup(func() { mockUpstream.Close() })

	sup := New(configSvc, mockUpstream, zap.NewNop())

	codes := make([]string, 0, 16)
	sup.SetErrorCodeNotifier(func(code string) { codes = append(codes, code) })

	return sup, &codes
}

func edgeTestServerState(name string, err error, retryCount int) *ServerState {
	return &ServerState{
		Name:    name,
		Config:  &config.ServerConfig{Name: name, URL: "http://127.0.0.1:1/mcp", Enabled: true},
		Enabled: true,
		ConnectionInfo: &types.ConnectionInfo{
			State:      types.StateError,
			LastError:  err,
			RetryCount: retryCount,
		},
	}
}

func edgeTestServerStateAt(name string, err error, retryCount int, attempt time.Time) *ServerState {
	st := edgeTestServerState(name, err, retryCount)
	st.ConnectionInfo.LastRetryTime = attempt
	return st
}

// TestUpdateStateView_OAuthRetriesReNotifyDespiteFrozenRetryCount pins the
// hole a cross-model review found in the first cut of the edge gate.
//
// types.StateManager.SetOAuthError increments oauthRetryCount and NOT
// retryCount (upstream/types/types.go:726). A server failing OAuth over and
// over therefore holds (same classified code, same ConnectionInfo.RetryCount)
// across every attempt, so a gate keyed on those two alone fired exactly once
// and then went silent forever — unbounded coalescing, not the
// observation-cadence-bounded coalescing the comment claimed. What DOES move
// on each attempt is LastRetryTime, which every failure setter stamps and
// which a re-observation of a sticky error never touches.
//
// Without the LastRetryTime term this fails with got=1.
func TestUpdateStateView_OAuthRetriesReNotifyDespiteFrozenRetryCount(t *testing.T) {
	sup, codes := newEdgeTestSupervisor(t)

	oauthErr := fmt.Errorf("all authentication strategies failed: oauth token exchange rejected")
	base := time.Now()

	// Three distinct failed attempts. RetryCount is frozen at 0 throughout,
	// exactly as SetOAuthError leaves it.
	sup.updateStateView("srv", edgeTestServerStateAt("srv", oauthErr, 0, base))
	sup.updateStateView("srv", edgeTestServerStateAt("srv", oauthErr, 0, base.Add(30*time.Second)))
	sup.updateStateView("srv", edgeTestServerStateAt("srv", oauthErr, 0, base.Add(90*time.Second)))

	if len(*codes) != 3 {
		t.Fatalf("three OAuth attempts with a frozen RetryCount produced %d notifications (%v), want 3", len(*codes), *codes)
	}
	for i, c := range *codes {
		if c != (*codes)[0] {
			t.Fatalf("attempt %d reclassified: %v", i, *codes)
		}
	}
}

// TestUpdateStateView_StickyAttemptTimeDoesNotReNotify is the other half of
// the guard above: adding LastRetryTime to the basis must not re-open the
// level-triggering it replaced. reconcile() re-observes the SAME sticky
// ConnectionInfo every 30s, attempt time included, so nothing about the
// timestamp changes and the pass must stay silent.
func TestUpdateStateView_StickyAttemptTimeDoesNotReNotify(t *testing.T) {
	sup, codes := newEdgeTestSupervisor(t)

	stuck := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
	attempt := time.Now()
	for i := 0; i < 10; i++ {
		sup.updateStateView("srv", edgeTestServerStateAt("srv", stuck, 3, attempt))
	}

	if len(*codes) != 1 {
		t.Fatalf("10 reconcile passes over one unchanged failure produced %d notifications (%v), want exactly 1", len(*codes), *codes)
	}
}

// TestUpdateStateView_ErrorCodeIsEdgeTriggered pins the defect this change
// fixes: reconcile() runs updateStateView for EVERY configured server on a
// 30s ticker, and ConnectionInfo.LastError is sticky (cleared only on a
// transition to Ready). Level-triggered notification therefore emitted one
// telemetry "event" per server per tick — 2880/day for a server parked
// awaiting OAuth login that makes zero connection attempts.
//
// On HEAD this fails with got=10.
func TestUpdateStateView_ErrorCodeIsEdgeTriggered(t *testing.T) {
	sup, codes := newEdgeTestSupervisor(t)

	stuck := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
	for i := 0; i < 10; i++ {
		sup.updateStateView("srv", edgeTestServerState("srv", stuck, 3))
	}

	if len(*codes) != 1 {
		t.Fatalf("unchanging error over 10 reconcile passes produced %d notifications (%v), want exactly 1", len(*codes), *codes)
	}
	if (*codes)[0] == "" {
		t.Fatal("notification carried an empty code")
	}
}

// TestUpdateStateView_RetryCountAdvanceReNotifies asserts the edge gate does
// not swallow a genuinely new failed attempt: the same classified code with a
// higher ConnectionInfo.RetryCount is a real event and must be counted.
func TestUpdateStateView_RetryCountAdvanceReNotifies(t *testing.T) {
	sup, codes := newEdgeTestSupervisor(t)

	stuck := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
	sup.updateStateView("srv", edgeTestServerState("srv", stuck, 1))
	sup.updateStateView("srv", edgeTestServerState("srv", stuck, 1)) // same attempt, no event
	sup.updateStateView("srv", edgeTestServerState("srv", stuck, 2)) // new attempt failed

	if len(*codes) != 2 {
		t.Fatalf("got %d notifications (%v), want 2 (initial + one retry advance)", len(*codes), *codes)
	}
	if (*codes)[0] != (*codes)[1] {
		t.Fatalf("retry advance reclassified: %v", *codes)
	}
}

// TestUpdateStateView_CodeChangeReNotifies asserts a different underlying
// failure still produces an event even when the retry count is unchanged.
func TestUpdateStateView_CodeChangeReNotifies(t *testing.T) {
	sup, codes := newEdgeTestSupervisor(t)

	refused := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
	var dns error = &net.DNSError{Name: "nonexistent.invalid", Err: "no such host"}

	sup.updateStateView("srv", edgeTestServerState("srv", refused, 1))
	sup.updateStateView("srv", edgeTestServerState("srv", refused, 1))
	sup.updateStateView("srv", edgeTestServerState("srv", dns, 1))

	if len(*codes) != 2 {
		t.Fatalf("got %d notifications (%v), want 2 (initial + code change)", len(*codes), *codes)
	}
	if (*codes)[0] == (*codes)[1] {
		t.Fatalf("distinct errors classified identically (%v); pick errors that classify differently", *codes)
	}
}

// TestUpdateStateView_StandingDiagnosticStillRendered guards the companion
// requirement: only the TELEMETRY notification is edge-triggered. The
// standing diagnostic on ServerStatus must keep being attached on every pass
// so the UI, REST API and CLI still render it.
func TestUpdateStateView_StandingDiagnosticStillRendered(t *testing.T) {
	sup, _ := newEdgeTestSupervisor(t)

	stuck := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
	for i := 0; i < 5; i++ {
		sup.updateStateView("srv", edgeTestServerState("srv", stuck, 3))
	}

	status, ok := sup.StateView().GetServer("srv")
	if !ok {
		t.Fatal("server missing from stateview")
	}
	if status.Diagnostic == nil {
		t.Fatal("standing diagnostic was dropped by the edge gate; the UI would render nothing")
	}
	if status.LastError == "" {
		t.Fatal("standing LastError was dropped")
	}
}

// TestCurrentErrorCodes_StandingState covers the companion signal. Edge-
// triggering alone deletes the "installs currently affected" measurement: the
// 24h counter decays, error_code_counts_24h is omitempty, and the whole
// Diagnostics object is omitempty via isZero(). A permanently parked OAuth
// install would emit once and then vanish from the payload — trading an
// inflated number for a missing one. CurrentErrorCodes reports the standing
// state instead: classified code -> number of configured servers in it.
func TestCurrentErrorCodes_StandingState(t *testing.T) {
	sup, _ := newEdgeTestSupervisor(t)

	if got := sup.CurrentErrorCodes(); len(got) != 0 {
		t.Fatalf("clean supervisor reported standing codes: %v", got)
	}

	refused := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
	for _, name := range []string{"a", "b"} {
		sup.updateStateView(name, edgeTestServerState(name, refused, 1))
	}

	got := sup.CurrentErrorCodes()
	if len(got) != 1 {
		t.Fatalf("want exactly one standing code, got %v", got)
	}
	for code, n := range got {
		if n != 2 {
			t.Fatalf("standing code %s counted %d servers, want 2", code, n)
		}
		if !strings.HasPrefix(code, "MCPX_") {
			t.Fatalf("non-MCPX code leaked into the standing map: %q", code)
		}
	}

	// A server that recovers must drop out of the standing set — the property
	// the decaying 24h counter cannot express.
	sup.updateStateView("a", &ServerState{
		Name:      "a",
		Config:    &config.ServerConfig{Name: "a", URL: "http://127.0.0.1:1/mcp", Enabled: true},
		Enabled:   true,
		Connected: true,
		ConnectionInfo: &types.ConnectionInfo{
			State: types.StateReady,
		},
	})

	total := 0
	for _, n := range sup.CurrentErrorCodes() {
		total += n
	}
	if total != 1 {
		t.Fatalf("after one server recovered, standing total = %d, want 1 (%v)", total, sup.CurrentErrorCodes())
	}
}

// TestCurrentErrorCodes_ExcludesDisabledAndQuarantined pins the "currently
// affected" semantics: a server the user disabled or quarantined is not a
// problem the install is suffering from.
func TestCurrentErrorCodes_ExcludesDisabledAndQuarantined(t *testing.T) {
	sup, _ := newEdgeTestSupervisor(t)

	refused := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)

	disabled := edgeTestServerState("disabled", refused, 1)
	disabled.Enabled = false
	sup.updateStateView("disabled", disabled)

	quarantined := edgeTestServerState("quarantined", refused, 1)
	quarantined.Quarantined = true
	sup.updateStateView("quarantined", quarantined)

	if got := sup.CurrentErrorCodes(); len(got) != 0 {
		t.Fatalf("disabled/quarantined servers counted as currently affected: %v", got)
	}
}
