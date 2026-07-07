package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// writeEnvelope writes the {"success":true,"data":...} REST envelope.
func writeEnvelope(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
}

func writeEnvelopeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
}

// ============================================================================
// FR-011 negatives (SC-005): activity invariant fails when a call is absent
// or its request id does not resolve.
// ============================================================================

// activityFake serves /api/v1/activity with a fixed record set, honoring the
// request_id filter.
func activityFake(t *testing.T, records []activityRecord, headerCorrelated bool) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/activity", func(w http.ResponseWriter, r *http.Request) {
		rid := r.URL.Query().Get("request_id")
		var out []activityRecord
		for _, rec := range records {
			if rid == "" || rec.RequestID == rid {
				out = append(out, rec)
			}
		}
		if !headerCorrelated && rid != "" && strings.HasPrefix(rid, "hdr-") {
			out = nil // header ids never recorded
		}
		writeEnvelope(w, activityListResponse{Activities: out, Total: len(out)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newClient(srv.URL, "test-key")
}

func TestCheckActivityRequestIDs_MissingCall_Fails(t *testing.T) {
	c := activityFake(t, []activityRecord{
		{ID: "1", Type: "tool_call", RequestID: "core-id-1", Arguments: map[string]any{"text": "nonce-A"}},
	}, false)
	calls := []issuedCall{
		{Cell: "stdio", Nonce: "nonce-A", HeaderRequestID: "hdr-1"},
		{Cell: "http", Nonce: "nonce-B-dropped", HeaderRequestID: "hdr-2"},
	}
	res, err := checkActivityRequestIDs(context.Background(), c, calls, 0)
	if err == nil {
		t.Fatal("expected failure when an issued call has no activity record")
	}
	if len(res.Missing) != 1 || res.Missing[0] != "nonce-B-dropped" {
		t.Errorf("missing=%v want [nonce-B-dropped]", res.Missing)
	}
}

func TestCheckActivityRequestIDs_UnresolvableRequestID_Fails(t *testing.T) {
	// Record exists (nonce matches) but carries an EMPTY request id, which can
	// never resolve via ?request_id=.
	c := activityFake(t, []activityRecord{
		{ID: "1", Type: "tool_call", RequestID: "", Arguments: map[string]any{"text": "nonce-A"}},
	}, false)
	calls := []issuedCall{{Cell: "stdio", Nonce: "nonce-A", HeaderRequestID: "hdr-1"}}
	res, err := checkActivityRequestIDs(context.Background(), c, calls, 0)
	if err == nil {
		t.Fatal("expected failure when the recorded request id cannot resolve")
	}
	if len(res.Unresolvable) != 1 {
		t.Errorf("unresolvable=%v want exactly 1", res.Unresolvable)
	}
}

func TestCheckActivityRequestIDs_RecordedIDFallback_PassesAndRecordsLimitation(t *testing.T) {
	c := activityFake(t, []activityRecord{
		{ID: "1", Type: "tool_call", RequestID: "core-id-1", Arguments: map[string]any{"text": "nonce-A"}},
		{ID: "2", Type: "tool_call", RequestID: "core-id-2", Arguments: map[string]any{"args": map[string]any{"text": "nonce-B"}}},
	}, false)
	calls := []issuedCall{
		{Cell: "stdio", Nonce: "nonce-A", HeaderRequestID: "hdr-1"},
		{Cell: "http", Nonce: "nonce-B", HeaderRequestID: "hdr-2"},
	}
	res, err := checkActivityRequestIDs(context.Background(), c, calls, time.Second)
	if err != nil {
		t.Fatalf("expected pass via recorded-id fallback: %v", err)
	}
	if res.CorrelationMode != "recorded-id" {
		t.Errorf("mode=%s want recorded-id", res.CorrelationMode)
	}
	if res.Limitation == "" {
		t.Error("limitation must be recorded when header correlation is unsupported")
	}
	if res.Resolved != 2 {
		t.Errorf("resolved=%d want 2", res.Resolved)
	}
}

func TestCheckActivityRequestIDs_HeaderCorrelation_Detected(t *testing.T) {
	c := activityFake(t, []activityRecord{
		{ID: "1", Type: "tool_call", RequestID: "hdr-1", Arguments: map[string]any{"text": "nonce-A"}},
	}, true)
	calls := []issuedCall{{Cell: "stdio", Nonce: "nonce-A", HeaderRequestID: "hdr-1"}}
	res, err := checkActivityRequestIDs(context.Background(), c, calls, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.CorrelationMode != "header" || res.Limitation != "" {
		t.Errorf("mode=%s limitation=%q want header mode with no limitation", res.CorrelationMode, res.Limitation)
	}
}

func TestCheckActivityRequestIDs_NoIssuedCalls_Fails(t *testing.T) {
	c := activityFake(t, nil, false)
	if _, err := checkActivityRequestIDs(context.Background(), c, nil, 0); err == nil {
		t.Fatal("zero issued calls must fail, not vacuously pass")
	}
}

// ============================================================================
// FR-012 negatives: flat counters under traffic fail the gate.
// ============================================================================

// countersFake serves the three counter endpoints from mutable state.
type countersFake struct {
	mu        sync.Mutex
	toolList  int
	saved     int
	calls     int64
	builtin   int64
	telemetry int // http status for /telemetry/payload; 0 => 200
}

func (f *countersFake) client(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats/tokens", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeEnvelope(w, tokenStats{TotalServerToolListSize: f.toolList, SavedTokens: f.saved})
	})
	mux.HandleFunc("/api/v1/activity/usage", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeEnvelope(w, usageResponse{Tools: []usageToolStat{{Server: "gate-stdio", Tool: "echo", Calls: f.calls}}})
	})
	mux.HandleFunc("/api/v1/telemetry/payload", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.telemetry != 0 {
			writeEnvelopeError(w, f.telemetry, "telemetry service unavailable")
			return
		}
		writeEnvelope(w, telemetryPayload{BuiltinToolCalls: map[string]int64{"call_tool_read": f.builtin}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newClient(srv.URL, "test-key")
}

func TestCheckCountersMoved_FlatCounters_Fail(t *testing.T) {
	fake := &countersFake{toolList: 100, saved: 50, calls: 7, builtin: 3}
	c := fake.client(t)
	before, err := takeCounterSnapshot(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	// No movement at all.
	steps, _, err := checkCountersMoved(context.Background(), c, before, 0)
	if err == nil || !strings.Contains(err.Error(), "flat counters") {
		t.Fatalf("expected flat-counter failure, got %v", err)
	}
	failed := 0
	for _, s := range steps {
		if s.Status == gatereport.StatusFail {
			failed++
		}
	}
	if failed < 2 {
		t.Errorf("expected tokens+usage steps to fail, steps=%+v", steps)
	}
}

func TestCheckCountersMoved_AllMoved_Pass(t *testing.T) {
	fake := &countersFake{toolList: 0, saved: 0, calls: 0, builtin: 0}
	c := fake.client(t)
	before, err := takeCounterSnapshot(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.toolList, fake.saved, fake.calls, fake.builtin = 500, 400, 12, 30
	fake.mu.Unlock()
	steps, after, err := checkCountersMoved(context.Background(), c, before, time.Second)
	if err != nil {
		t.Fatalf("expected pass: %v (steps=%+v)", err, steps)
	}
	if after.UsageCalls != 12 {
		t.Errorf("after.UsageCalls=%d want 12", after.UsageCalls)
	}
}

func TestCheckCountersMoved_TelemetryUnavailable_SkippedNotFailed(t *testing.T) {
	fake := &countersFake{toolList: 0, calls: 0, telemetry: http.StatusServiceUnavailable}
	c := fake.client(t)
	before, err := takeCounterSnapshot(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if before.TelemetryAvailable {
		t.Fatal("fake telemetry should be unavailable")
	}
	fake.mu.Lock()
	fake.toolList, fake.calls = 100, 5
	fake.mu.Unlock()
	steps, _, err := checkCountersMoved(context.Background(), c, before, time.Second)
	if err != nil {
		t.Fatalf("telemetry unavailability must not fail the check when other counters move: %v", err)
	}
	var sawSkip bool
	for _, s := range steps {
		if s.Name == "telemetry-builtin-tool-calls-increase" && s.Status == gatereport.StatusSkipped && s.Reason != "" {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Errorf("telemetry sub-check must be recorded as skipped with a reason, steps=%+v", steps)
	}
}

func TestCheckCountersMoved_TelemetryRegressesAfterHealthyBaseline_Fails(t *testing.T) {
	// Telemetry is healthy at the boot baseline but goes 503 under matrix
	// traffic. That is a regression, not an environment skip — the sub-check
	// must FAIL rather than be masked as skipped.
	fake := &countersFake{toolList: 0, calls: 0, builtin: 3, telemetry: 0}
	c := fake.client(t)
	before, err := takeCounterSnapshot(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !before.TelemetryAvailable {
		t.Fatal("fake telemetry should be available at baseline")
	}
	fake.mu.Lock()
	fake.toolList, fake.calls = 100, 5
	fake.telemetry = http.StatusServiceUnavailable
	fake.mu.Unlock()

	steps, _, err := checkCountersMoved(context.Background(), c, before, 0)
	if err == nil || !strings.Contains(err.Error(), "flat counters") {
		t.Fatalf("expected failure when telemetry disappears after a healthy baseline, got %v", err)
	}
	var telemetryFailed bool
	for _, s := range steps {
		if s.Name == "telemetry-builtin-tool-calls-increase" {
			if s.Status == gatereport.StatusSkipped {
				t.Errorf("telemetry regression must not be skipped: %+v", s)
			}
			if s.Status == gatereport.StatusFail {
				telemetryFailed = true
			}
		}
	}
	if !telemetryFailed {
		t.Errorf("telemetry sub-check must be recorded as failed, steps=%+v", steps)
	}
}

// ============================================================================
// FR-013 negative: a pre-approved (never-quarantined) server fails the
// quarantine invariant.
// ============================================================================

// quarantineFake is a stateful fake of the server-management surface.
type quarantineFake struct {
	mu          sync.Mutex
	added       bool
	name        string
	quarantined bool
	connected   bool
	approved    bool
	// preApproved simulates the negative case: servers come up unquarantined.
	preApproved bool
}

func (f *quarantineFake) client(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/servers", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost {
			var req addServerRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.added = true
			f.name = req.Name
			f.quarantined = !f.preApproved
			f.connected = f.preApproved // quarantined servers do not connect
			writeEnvelope(w, map[string]any{"added": true})
			return
		}
		var servers []serverInfo
		if f.added {
			servers = append(servers, serverInfo{
				Name: f.name, Enabled: true, Quarantined: f.quarantined,
				Connected: f.connected, ToolCount: 2,
			})
		}
		writeEnvelope(w, serversResponse{Servers: servers})
	})
	mux.HandleFunc("/api/v1/servers/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/unquarantine"):
			f.quarantined = false
			f.connected = true
			writeEnvelope(w, map[string]any{"quarantined": false})
		case strings.HasSuffix(r.URL.Path, "/tools/approve"):
			f.approved = true
			writeEnvelope(w, map[string]any{"approved": 2})
		case r.Method == http.MethodDelete:
			f.added = false
			writeEnvelope(w, map[string]any{"removed": true})
		default:
			writeEnvelopeError(w, http.StatusNotFound, "not found")
		}
	})
	mux.HandleFunc("/api/v1/tools/call", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var req struct {
			Arguments struct {
				Name string         `json:"name"`
				Args map[string]any `json:"args"`
			} `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if f.quarantined {
			writeEnvelope(w, []contentBlock{{Type: "text", Text: `{"status":"QUARANTINED_SERVER_BLOCKED"}`}})
			return
		}
		echo, _ := json.Marshal(map[string]any{"echo": req.Arguments.Args})
		writeEnvelope(w, []contentBlock{{Type: "text", Text: string(echo)}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newClient(srv.URL, "test-key")
}

func fakeFixtureBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcpfixture")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func quarantineDeps(t *testing.T) quarantineFlowDeps {
	t.Helper()
	return quarantineFlowDeps{
		FixtureBinary:  fakeFixtureBinary(t),
		WorkDir:        t.TempDir(),
		ServerName:     "gate-fresh-test",
		AppearTimeout:  2 * time.Second,
		ConnectTimeout: 2 * time.Second,
	}
}

func TestCheckQuarantineFlow_PreApprovedServer_Fails(t *testing.T) {
	fake := &quarantineFake{preApproved: true}
	c := fake.client(t)
	steps, cleanup, err := checkQuarantineFlow(context.Background(), c, quarantineDeps(t))
	defer cleanup()
	if err == nil {
		t.Fatal("a server that never entered quarantine must fail the invariant")
	}
	if !strings.Contains(err.Error(), "NOT quarantined") {
		t.Errorf("error should name the missing quarantine transition: %v", err)
	}
	var sawFail bool
	for _, s := range steps {
		if s.Name == "enters-quarantine" && s.Status == gatereport.StatusFail {
			sawFail = true
		}
	}
	if !sawFail {
		t.Errorf("enters-quarantine step must fail, steps=%+v", steps)
	}
}

func TestCheckQuarantineFlow_FullLifecycle_Passes(t *testing.T) {
	fake := &quarantineFake{}
	c := fake.client(t)
	steps, cleanup, err := checkQuarantineFlow(context.Background(), c, quarantineDeps(t))
	defer cleanup()
	if err != nil {
		t.Fatalf("expected the full Spec 032 flow to pass against the fake: %v (steps=%+v)", err, steps)
	}
	wantSteps := []string{"add-server", "enters-quarantine", "call-blocked-while-quarantined", "approve", "post-approval-call"}
	if len(steps) != len(wantSteps) {
		t.Fatalf("got %d steps want %d: %+v", len(steps), len(wantSteps), steps)
	}
	for i, want := range wantSteps {
		if steps[i].Name != want || steps[i].Status != gatereport.StatusPass {
			t.Errorf("step %d = %s/%s, want %s/pass", i, steps[i].Name, steps[i].Status, want)
		}
	}
	if !fake.approved {
		t.Error("tools/approve was never called")
	}
}

// ============================================================================
// FR-014 negative: the upgrade check fails on a corrupted/absent index
// (search returns nothing).
// ============================================================================

func searchFake(t *testing.T, results []searchResult) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/index/search", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, searchResponse{Results: results, Total: len(results)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newClient(srv.URL, "test-key")
}

func TestAssertSearchHasResults_EmptyIndex_Fails(t *testing.T) {
	c := searchFake(t, nil)
	err := assertSearchHasResults(context.Background(), c, "echo", "gate-up-a", 0)
	if err == nil || !strings.Contains(err.Error(), "no usable results") {
		t.Fatalf("empty index must fail the preservation check, got %v", err)
	}
}

func TestAssertSearchHasResults_WrongServer_Fails(t *testing.T) {
	c := searchFake(t, []searchResult{{Tool: searchResultTool{Name: "other:echo", ServerName: "other"}}})
	if err := assertSearchHasResults(context.Background(), c, "echo", "gate-up-a", 0); err == nil {
		t.Fatal("results not matching the expected server must fail")
	}
}

func TestAssertSearchHasResults_Match_Passes(t *testing.T) {
	c := searchFake(t, []searchResult{{Tool: searchResultTool{Name: "gate-up-a:echo", ServerName: "gate-up-a"}}})
	if err := assertSearchHasResults(context.Background(), c, "echo", "gate-up-a", time.Second); err != nil {
		t.Fatal(err)
	}
}

// ============================================================================
// client plumbing
// ============================================================================

func TestClient_SendsAPIKeyAndRequestID(t *testing.T) {
	var gotKey, gotRID string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tools/call", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		gotRID = r.Header.Get("X-Request-Id")
		writeEnvelope(w, []contentBlock{{Type: "text", Text: `{"echo":{"text":"n"}}`}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newClient(srv.URL, "secret-key")
	text, err := c.callToolREST(context.Background(), "gate-stdio:echo", map[string]any{"text": "n"}, "rid-42")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "secret-key" || gotRID != "rid-42" {
		t.Errorf("key=%q rid=%q", gotKey, gotRID)
	}
	if !strings.Contains(text, `"echo"`) {
		t.Errorf("text=%q", text)
	}
}

func TestClient_ErrorEnvelope(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelopeError(w, http.StatusUnauthorized, "invalid API key")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newClient(srv.URL, "wrong")
	err := c.statusOK(context.Background())
	var ae *apiError
	if err == nil || !asAPIError(err, &ae) || ae.Status != http.StatusUnauthorized {
		t.Fatalf("want 401 apiError, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("error should carry the server message: %v", err)
	}
}

// Guard: fragment names used by the driver must exist in the manifest so the
// merger recognizes them (drift protection).
func TestDriverFragmentNames_MatchManifest(t *testing.T) {
	manifest := map[string]bool{}
	for _, m := range gatereport.Manifest() {
		manifest[m.Name] = true
	}
	for _, cell := range allCells {
		if !manifest["matrix/"+cell] {
			t.Errorf("matrix cell %q has no manifest entry", cell)
		}
	}
	for _, name := range []string{
		gatereport.EntryInvariantActivity,
		gatereport.EntryInvariantCounters,
		gatereport.EntryInvariantQuarantine,
		gatereport.EntryInvariantUpgrade,
	} {
		if !manifest[name] {
			t.Errorf("invariant %q has no manifest entry", name)
		}
	}
}

// sanity: gateServerConfig JSON encodes isolation override the way
// internal/config expects (enabled bool + image string).
func TestBuildGateConfig_DockerIsolationShape(t *testing.T) {
	cfg := buildGateConfig("127.0.0.1:1", t.TempDir(), "k", []gateServerConfig{
		{"name": "gate-docker", "command": "/mcpfixture", "args": []string{"--transport", "stdio"},
			"protocol": "stdio", "enabled": true, "quarantined": false,
			"isolation": map[string]any{"image": dockerFixtureImage}},
	}, map[string]any{"enabled": true})
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The docker cell rides GLOBAL docker_isolation.enabled=true with a
	// per-server image override; per-server opt-ins alone (enabled bool or
	// mode enum) verifiably do not engage isolation when the global flag is
	// off.
	for _, want := range []string{`"isolation":{"image":"mcpfixture:gate"}`, `"docker_isolation":{"enabled":true}`, `"mcpServers"`, `"enable_socket":false`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("config JSON missing %s:\n%s", want, raw)
		}
	}
}

func TestFreePortAndCopyFile(t *testing.T) {
	// The exec-bit assertion below (0o100) is Unix-only; Windows has no Unix
	// permission bits, so copyFile cannot set an owner-execute bit there. The
	// gate driver runs exclusively on the ubuntu-latest runner, so this staging
	// helper is only ever exercised on Linux.
	if runtime.GOOS == "windows" {
		t.Skip("copyFile exec-bit staging is Linux-only gate infra (gate runs on ubuntu-latest)")
	}
	p, err := freePort()
	if err != nil || p == 0 {
		t.Fatalf("freePort: %d %v", p, err)
	}
	src := fakeFixtureBinary(t)
	dst := filepath.Join(t.TempDir(), "sub", "copy")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("copied file not executable: %v", info.Mode())
	}
}

func TestCompleteOAuthFlow_SubmitsCredentialsAndFollowsRedirect(t *testing.T) {
	var callbackHit bool
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackHit = true
		fmt.Fprint(w, "ok")
	}))
	defer cb.Close()

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/authorize" || r.Method != http.MethodPost {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("username") != "testuser" || r.FormValue("consent") != "on" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, cb.URL+"/callback?code=abc&state="+r.FormValue("state"), http.StatusFound)
	}))
	defer idp.Close()

	authURL := idp.URL + "/authorize?client_id=c1&redirect_uri=" + cb.URL + "/callback&state=s1&code_challenge=x&code_challenge_method=S256"
	if err := completeOAuthFlow(context.Background(), authURL); err != nil {
		t.Fatal(err)
	}
	if !callbackHit {
		t.Error("redirect chain never reached the callback")
	}
}

func TestCompleteOAuthFlow_ErrorRedirect_Fails(t *testing.T) {
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "error page")
	}))
	defer cb.Close()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cb.URL+"/cb?error=access_denied&error_description=denied", http.StatusFound)
	}))
	defer idp.Close()
	err := completeOAuthFlow(context.Background(), idp.URL+"/authorize?client_id=c1")
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("error redirect must fail the flow with the IdP error, got %v", err)
	}
}
