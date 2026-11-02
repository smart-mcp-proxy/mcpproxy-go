package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// feedbackContractFixture is the wire contract between SubmitFeedback and the
// telemetry worker's /v1/feedback handler (repo mcpproxy-telemetry). The worker
// keeps a byte-identical copy at test/fixtures/feedback_contract.json and its
// tests assert every field below is read from this exact nesting. Issue #1724:
// the client nested context under "context" while the worker read top-level
// fields, so every feedback issue ever filed had an empty Context table. Change
// both copies together.
const feedbackContractFixture = "testdata/feedback_contract.json"

func loadFeedbackContract(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(feedbackContractFixture)
	if err != nil {
		t.Fatalf("read %s: %v", feedbackContractFixture, err)
	}
	return data
}

// jsonKeyPaths returns every object key path in a decoded JSON value
// ("context.diagnostics.fix_attempted_24h"). Keys of free-form maps (the MCPX_
// code maps) are reported as "<key>" so the contract pins the shape, not data.
func jsonKeyPaths(prefix string, v any, out map[string]bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	freeForm := strings.HasSuffix(prefix, "error_code_counts_24h") || strings.HasSuffix(prefix, "current_error_codes")
	for k, child := range obj {
		if freeForm {
			k = "<key>"
		}
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		out[p] = true
		jsonKeyPaths(p, child, out)
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestFeedbackContract_FixtureRoundTrips proves every fixture field maps onto a
// FeedbackRequest field at the same nesting: decoding rejects unknown fields
// and re-encoding reproduces the fixture exactly.
func TestFeedbackContract_FixtureRoundTrips(t *testing.T) {
	data := loadFeedbackContract(t)

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req FeedbackRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("fixture does not decode into FeedbackRequest: %v", err)
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("FeedbackRequest does not round-trip the contract fixture\nwant: %s\ngot:  %s", data, encoded)
	}
}

// TestFeedbackContract_SubmitSendsExactlyContractFields sends a fully
// populated feedback through SubmitFeedback (every store wired, every optional
// field non-empty) and asserts the wire body has exactly the fixture's key
// paths: a new field must be added to the fixture (and so to the worker) before
// it ships, and dropping a populated field fails too.
func TestFeedbackContract_SubmitSendsExactlyContractFields(t *testing.T) {
	clearTelemetryEnv(t)
	withFixedMachineID(t, strings.Repeat("b", 64))

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = readAll(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	svc := newFeedbackTestService(t, server.URL, true)
	wireAllFeedbackStores(t, svc)
	if _, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "Something is broken and I want to report it",
		Email:    "test@example.com",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	var fixture, sent any
	if err := json.Unmarshal(loadFeedbackContract(t), &fixture); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode sent body %q: %v", body, err)
	}
	allowed := map[string]bool{}
	jsonKeyPaths("", fixture, allowed)
	sentKeys := map[string]bool{}
	jsonKeyPaths("", sent, sentKeys)
	for _, k := range sortedKeys(sentKeys) {
		if !allowed[k] {
			t.Errorf("SubmitFeedback sent %q, which the contract fixture does not declare", k)
		}
	}
	for _, k := range sortedKeys(allowed) {
		if !sentKeys[k] {
			t.Errorf("contract fixture declares %q, but a fully populated SubmitFeedback did not send it", k)
		}
	}
}

// wireAllFeedbackStores gives svc every source an optional feedback field
// reads, each non-empty, so the contract test sees the complete wire shape.
func wireAllFeedbackStores(t *testing.T, svc *Service) {
	t.Helper()
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "feedback.db"), 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureDiagnosticsCountersBucket(db); err != nil {
		t.Fatalf("ensure diagnostics bucket: %v", err)
	}
	diag := NewDiagnosticsCounterStore()
	if err := diag.RecordErrorCode(db, "MCPX_STDIO_SPAWN_ENOENT"); err != nil {
		t.Fatalf("record error code: %v", err)
	}
	if err := diag.RecordFixAttempt(db, "success"); err != nil {
		t.Fatalf("record fix attempt: %v", err)
	}
	svc.SetDiagnosticsCounterStore(diag, db)
	svc.SetCurrentErrorCodesProvider(func() map[string]int {
		return map[string]int{"MCPX_OAUTH_REFRESH_EXPIRED": 1}
	})
	prechurn := NewPreChurnStore()
	if err := prechurn.RecordLastErrorCode(db, "MCPX_STDIO_SPAWN_ENOENT"); err != nil {
		t.Fatalf("record last error code: %v", err)
	}
	svc.SetPreChurn(PreviousShutdownCrash, prechurn, db)
	svc.startTime = time.Now().Add(-95 * time.Minute)
}

// consentOnlyFeedbackKeys are the wire key paths that must appear only when the
// install has telemetry consent.
var consentOnlyFeedbackKeys = []string{
	"context.anonymous_id",
	"context.machine_id",
	"context.last_startup_outcome",
	"context.previous_shutdown",
	"context.last_error_code",
	"context.diagnostics",
}

// submitFeedbackCaptureKeys submits through svc (built by build against a test
// endpoint) and returns the key paths of the HTTP body actually sent.
func submitFeedbackCaptureKeys(t *testing.T, build func(endpoint string) *Service) map[string]bool {
	t.Helper()
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = readAll(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	svc := build(server.URL)
	if _, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "High CPU usage while idle, please help",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}
	var sent any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode sent body %q: %v", body, err)
	}
	keys := map[string]bool{}
	jsonKeyPaths("", sent, keys)
	return keys
}

// assertNoConsentOnlyKeys fails for every consent-only key present on the wire,
// and checks the basic context still went out.
func assertNoConsentOnlyKeys(t *testing.T, keys map[string]bool) {
	t.Helper()
	for _, k := range consentOnlyFeedbackKeys {
		if keys[k] {
			t.Errorf("consent-only key %q was sent", k)
		}
	}
	if !keys["context.version"] || !keys["context.server_count"] {
		t.Errorf("basic context missing from body: %v", sortedKeys(keys))
	}
}

// TestSubmitFeedback_NoTelemetryRefAfterOptOutLatch: once the opt-out has
// latched, feedback carries no ids or diagnostics even if the config still
// reads enabled. Every store is wired, so an absent key proves the gate, not
// an empty source.
func TestSubmitFeedback_NoTelemetryRefAfterOptOutLatch(t *testing.T) {
	clearTelemetryEnv(t)
	withFixedMachineID(t, strings.Repeat("e", 64))

	keys := submitFeedbackCaptureKeys(t, func(endpoint string) *Service {
		svc := newFeedbackTestService(t, endpoint, true)
		wireAllFeedbackStores(t, svc)
		svc.optedOut.Store(true)
		return svc
	})
	assertNoConsentOnlyKeys(t, keys)
}

// TestSubmitFeedback_NoTelemetryRefOnDevBuild: development (non-semver) builds
// never send telemetry, so their feedback carries none of the consent-only
// fields even with telemetry enabled in config and an anonymous id on file.
func TestSubmitFeedback_NoTelemetryRefOnDevBuild(t *testing.T) {
	for _, version := range []string{"dev", "development", ""} {
		t.Run("version="+version, func(t *testing.T) {
			clearTelemetryEnv(t)
			withFixedMachineID(t, strings.Repeat("f", 64))

			keys := submitFeedbackCaptureKeys(t, func(endpoint string) *Service {
				svc := newFeedbackTestService(t, endpoint, true)
				svc.version = version
				wireAllFeedbackStores(t, svc)
				return svc
			})
			for _, k := range consentOnlyFeedbackKeys {
				if keys[k] {
					t.Errorf("dev build %q sent consent-only key %q", version, k)
				}
			}
			if !keys["context.edition"] || !keys["context.server_count"] {
				t.Errorf("basic context missing: %v", sortedKeys(keys))
			}
		})
	}
}

// TestSubmitFeedback_NoTelemetryRefWhenEnvOptsOut: the env-level opt-outs
// (MCPPROXY_TELEMETRY=false, DO_NOT_TRACK, CI) override a config that says
// enabled, exactly as they do for the heartbeat.
func TestSubmitFeedback_NoTelemetryRefWhenEnvOptsOut(t *testing.T) {
	cases := []struct{ name, key, value string }{
		{"mcpproxy_telemetry_false", "MCPPROXY_TELEMETRY", "false"},
		{"do_not_track", "DO_NOT_TRACK", "1"},
		{"ci", "CI", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearTelemetryEnv(t)
			t.Setenv(tc.key, tc.value) // after clearTelemetryEnv, which blanks all three
			withFixedMachineID(t, strings.Repeat("a", 64))

			keys := submitFeedbackCaptureKeys(t, func(endpoint string) *Service {
				svc := newFeedbackTestService(t, endpoint, true)
				wireAllFeedbackStores(t, svc)
				return svc
			})
			assertNoConsentOnlyKeys(t, keys)
		})
	}
}

// TestSubmitFeedback_RecheckStripsWhenOptOutLandsMidBuild proves the
// last-moment consent recheck in SubmitFeedback bites: the opt-out latches from
// inside a provider that feedbackContext calls after its own gate (the
// current-error-codes provider), so the context was built with consent and only
// the recheck can strip it.
func TestSubmitFeedback_RecheckStripsWhenOptOutLandsMidBuild(t *testing.T) {
	clearTelemetryEnv(t)
	withFixedMachineID(t, strings.Repeat("9", 64))

	var svcRef *Service
	var flipped bool
	keys := submitFeedbackCaptureKeys(t, func(endpoint string) *Service {
		svc := newFeedbackTestService(t, endpoint, true)
		wireAllFeedbackStores(t, svc)
		svc.SetCurrentErrorCodesProvider(func() map[string]int {
			flipped = true
			svcRef.optedOut.Store(true)
			return map[string]int{"MCPX_OAUTH_REFRESH_EXPIRED": 1}
		})
		svcRef = svc
		return svc
	})
	if !flipped {
		t.Fatal("provider never ran; the test no longer exercises the recheck")
	}
	assertNoConsentOnlyKeys(t, keys)
}

// TestFeedbackContext_WithoutTelemetryClearsEveryConsentField guards the
// last-moment recheck: every field documented as consent-only is cleared.
func TestFeedbackContext_WithoutTelemetryClearsEveryConsentField(t *testing.T) {
	var fc FeedbackContext
	if err := json.Unmarshal(mustContractContext(t), &fc); err != nil {
		t.Fatal(err)
	}
	fc.withoutTelemetry()
	if fc.AnonymousID != "" || fc.MachineID != "" || fc.LastStartupOutcome != "" ||
		fc.PreviousShutdown != "" || fc.LastErrorCode != "" || fc.Diagnostics != nil {
		t.Errorf("withoutTelemetry left consent-only fields: %+v", fc)
	}
	if fc.Version == "" || fc.ServerCount == 0 {
		t.Errorf("withoutTelemetry cleared basic context: %+v", fc)
	}
}

func mustContractContext(t *testing.T) []byte {
	t.Helper()
	var doc struct {
		Context json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(loadFeedbackContract(t), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Context
}

// TestSubmitFeedback_IncludesTelemetryRefWhenEnabled: with telemetry on, the
// feedback carries the same anonymous_id and machine_id the heartbeat reports,
// so a maintainer can join the report to that install's telemetry.
func TestSubmitFeedback_IncludesTelemetryRefWhenEnabled(t *testing.T) {
	clearTelemetryEnv(t)
	machineID := strings.Repeat("c", 64)
	withFixedMachineID(t, machineID)

	var got FeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	svc := newFeedbackTestService(t, server.URL, true)
	if _, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "High CPU usage while idle, please help",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	if got.Context.AnonymousID != testFeedbackAnonID {
		t.Errorf("anonymous_id = %q, want %q", got.Context.AnonymousID, testFeedbackAnonID)
	}
	if got.Context.MachineID != machineID {
		t.Errorf("machine_id = %q, want %q", got.Context.MachineID, machineID)
	}
	if got.Context.LastStartupOutcome != "success" {
		t.Errorf("last_startup_outcome = %q, want success", got.Context.LastStartupOutcome)
	}
	if got.Context.PreviousShutdown != PreviousShutdownCrash {
		t.Errorf("previous_shutdown = %q, want %q", got.Context.PreviousShutdown, PreviousShutdownCrash)
	}
	if got.Context.GoVersion == "" {
		t.Error("go_version missing")
	}
	if got.Context.ToolCount != 7 {
		t.Errorf("tool_count = %d, want 7", got.Context.ToolCount)
	}
}

// TestSubmitFeedback_NoTelemetryRefWhenDisabled: a user who turned telemetry
// off has not agreed to have this report linked to an install, so the
// correlation ids and diagnostics are withheld; the basic context still goes.
func TestSubmitFeedback_NoTelemetryRefWhenDisabled(t *testing.T) {
	clearTelemetryEnv(t)
	withFixedMachineID(t, strings.Repeat("d", 64))

	keys := submitFeedbackCaptureKeys(t, func(endpoint string) *Service {
		svc := newFeedbackTestService(t, endpoint, false)
		wireAllFeedbackStores(t, svc)
		return svc
	})
	assertNoConsentOnlyKeys(t, keys)
}

const testFeedbackAnonID = "550e8400-e29b-41d4-a716-446655440000"

func newFeedbackTestService(t *testing.T, endpoint string, telemetryEnabled bool) *Service {
	t.Helper()
	enabled := telemetryEnabled
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Enabled:            &enabled,
			Endpoint:           endpoint,
			AnonymousID:        testFeedbackAnonID,
			LastStartupOutcome: "success",
		},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount:    3,
		connectedCount: 2,
		toolCount:      7,
		routingMode:    "retrieve_tools",
	})
	svc.SetPreChurn(PreviousShutdownCrash, nil, nil)
	return svc
}

func withFixedMachineID(t *testing.T, id string) {
	t.Helper()
	prev := machineIDProvider
	machineIDProvider = func() string { return id }
	resetMachineIDForTest()
	t.Cleanup(func() {
		machineIDProvider = prev
		resetMachineIDForTest()
	})
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
