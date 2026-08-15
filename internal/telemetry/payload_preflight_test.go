package telemetry

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newPreflightService stands up a telemetry Service with the preflight counter
// store wired onto a throwaway BBolt DB (issue #969).
func newPreflightService(t *testing.T, cfg *config.Config) (*Service, *bbolt.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dir, "preflight_payload.db"), 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	svc.SetPreflightCounterStore(NewPreflightCounterStore(), db)
	return svc, db
}

// pinTelemetryEnvEnabled neutralises the three env opt-outs so a test that is
// asserting the ENABLED path is not silently turned into a no-op by CI=true on
// a build machine.
func pinTelemetryEnvEnabled(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
}

func enabledTelemetryConfig() *config.Config {
	enabled := true
	return &config.Config{
		Telemetry: &config.TelemetryConfig{AnonymousID: "test-id", Enabled: &enabled},
	}
}

// PF001: the preflight sub-object is omitted entirely when the store is not
// wired — a short-lived CLI process produces a payload shaped exactly like one
// from before this field existed.
func TestBuildPayload_PreflightOmittedWhenStoreNil(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	svc := New(enabledTelemetryConfig(), "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})

	payload := svc.BuildPayload()
	if payload.Preflight != nil {
		t.Errorf("expected Preflight nil when store is not wired, got %+v", payload.Preflight)
	}

	raw, _ := json.Marshal(payload)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["preflight"]; ok {
		t.Error("preflight key present in JSON despite nil pointer (omitempty broken)")
	}
}

// PF002: wired but never incremented → still omitted.
func TestBuildPayload_PreflightOmittedWhenAllZero(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	svc, _ := newPreflightService(t, enabledTelemetryConfig())

	if payload := svc.BuildPayload(); payload.Preflight != nil {
		t.Errorf("expected Preflight nil when all counters zero, got %+v", payload.Preflight)
	}
}

// PF003: every counter family reaches the payload, through the Service-level
// Record* entry points the server actually calls.
func TestBuildPayload_PreflightPopulated(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	svc, _ := newPreflightService(t, enabledTelemetryConfig())

	svc.RecordFilterDiagnosticsEmitted(3, 2)
	svc.RecordFilterDiagnosticsEmitted(1, 1)
	svc.RecordFilterDiagnosticsFollowed()
	svc.RecordAvailabilityBlock(BlockReasonServerQuarantined)
	svc.RecordAvailabilityBlock(BlockReasonServerQuarantined)
	svc.RecordAvailabilityBlock(BlockReasonToolNotCallable)
	svc.RecordDiscoveryOmission()
	svc.RecordDiscoveryOmission()
	svc.RecordDiscoveryOmission()

	payload := svc.BuildPayload()
	if payload.Preflight == nil {
		t.Fatal("expected Preflight non-nil after recording counters")
	}
	pf := payload.Preflight
	if pf.FilterDiagEmitted24h != 2 {
		t.Errorf("filter_diag_emitted_24h = %d, want 2", pf.FilterDiagEmitted24h)
	}
	if pf.FilterDiagMissingAnnotation24h != 4 {
		t.Errorf("filter_diag_missing_annotation_24h = %d, want 4", pf.FilterDiagMissingAnnotation24h)
	}
	if pf.FilterDiagExplicit24h != 3 {
		t.Errorf("filter_diag_explicit_24h = %d, want 3", pf.FilterDiagExplicit24h)
	}
	if pf.FilterDiagFollowed24h != 1 {
		t.Errorf("filter_diag_followed_24h = %d, want 1", pf.FilterDiagFollowed24h)
	}
	if pf.AvailabilityBlock24h != 3 {
		t.Errorf("availability_block_24h = %d, want 3", pf.AvailabilityBlock24h)
	}
	if pf.AvailabilityBlockReasons24h[BlockReasonServerQuarantined] != 2 {
		t.Errorf("server_quarantined = %d, want 2", pf.AvailabilityBlockReasons24h[BlockReasonServerQuarantined])
	}
	if pf.DiscoveryOmission24h != 3 {
		t.Errorf("discovery_omission_24h = %d, want 3", pf.DiscoveryOmission24h)
	}
}

// PF004: JSON round-trip — the sub-object is nested under "preflight" with the
// documented key names, and the payload still passes the anonymity scanner.
func TestBuildPayload_PreflightJSONRoundTrip(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	svc, _ := newPreflightService(t, enabledTelemetryConfig())

	svc.RecordFilterDiagnosticsEmitted(2, 0)
	svc.RecordAvailabilityBlock(BlockReasonTokenPermission)
	svc.RecordDiscoveryOmission()

	raw, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var wire struct {
		Preflight *struct {
			FilterDiagEmitted24h           int            `json:"filter_diag_emitted_24h"`
			FilterDiagMissingAnnotation24h int            `json:"filter_diag_missing_annotation_24h"`
			FilterDiagExplicit24h          int            `json:"filter_diag_explicit_24h"`
			FilterDiagFollowed24h          int            `json:"filter_diag_followed_24h"`
			AvailabilityBlock24h           int            `json:"availability_block_24h"`
			AvailabilityBlockReasons24h    map[string]int `json:"availability_block_reasons_24h"`
			DiscoveryOmission24h           int            `json:"discovery_omission_24h"`
		} `json:"preflight"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if wire.Preflight == nil {
		t.Fatal("preflight absent from JSON")
	}
	if wire.Preflight.FilterDiagEmitted24h != 1 {
		t.Errorf("filter_diag_emitted_24h want 1, got %d", wire.Preflight.FilterDiagEmitted24h)
	}
	if wire.Preflight.FilterDiagMissingAnnotation24h != 2 {
		t.Errorf("filter_diag_missing_annotation_24h want 2, got %d", wire.Preflight.FilterDiagMissingAnnotation24h)
	}
	if wire.Preflight.AvailabilityBlockReasons24h[BlockReasonTokenPermission] != 1 {
		t.Errorf("token_permission want 1, got %d", wire.Preflight.AvailabilityBlockReasons24h[BlockReasonTokenPermission])
	}
	if wire.Preflight.DiscoveryOmission24h != 1 {
		t.Errorf("discovery_omission_24h want 1, got %d", wire.Preflight.DiscoveryOmission24h)
	}
	if err := ScanForPII(raw); err != nil {
		t.Errorf("payload with preflight counters must pass the anonymity scanner: %v", err)
	}
}

// PF005: telemetry opt-out is honoured at EVENT time — an install with
// telemetry disabled persists nothing, so an occurrence observed while off can
// never become transmissible if telemetry is turned back on later.
func TestPreflightCounters_RespectConfigOptOut(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	disabled := false
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{AnonymousID: "test-id", Enabled: &disabled},
	}
	svc, db := newPreflightService(t, cfg)

	svc.RecordFilterDiagnosticsEmitted(5, 5)
	svc.RecordFilterDiagnosticsFollowed()
	svc.RecordAvailabilityBlock(BlockReasonServerQuarantined)
	svc.RecordDiscoveryOmission()

	snap, err := NewPreflightCounterStore().Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.isZero() {
		t.Fatalf("opt-out must persist nothing, got %+v", snap)
	}
	if payload := svc.BuildPayload(); payload.Preflight != nil {
		t.Errorf("expected Preflight nil under opt-out, got %+v", payload.Preflight)
	}
}

// PF006: the env opt-out (DO_NOT_TRACK and friends) is honoured on the same
// event-time gate as the config flag.
func TestPreflightCounters_RespectEnvOptOut(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	t.Setenv("DO_NOT_TRACK", "1")

	svc, db := newPreflightService(t, enabledTelemetryConfig())
	svc.RecordAvailabilityBlock(BlockReasonToolNotCallable)
	svc.RecordDiscoveryOmission()

	snap, err := NewPreflightCounterStore().Snapshot(db)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.isZero() {
		t.Fatalf("env opt-out must persist nothing, got %+v", snap)
	}
}

// PF007: every Record* is a safe no-op when the store was never wired.
func TestPreflightCounters_NoStoreIsNoOp(t *testing.T) {
	pinTelemetryEnvEnabled(t)
	svc := New(enabledTelemetryConfig(), "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})

	svc.RecordFilterDiagnosticsEmitted(1, 1)
	svc.RecordFilterDiagnosticsFollowed()
	svc.RecordAvailabilityBlock(BlockReasonOther)
	svc.RecordDiscoveryOmission()

	if payload := svc.BuildPayload(); payload.Preflight != nil {
		t.Errorf("expected Preflight nil with no store wired, got %+v", payload.Preflight)
	}
}
