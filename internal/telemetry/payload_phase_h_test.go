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

// PH001: BuildPayload omits Diagnostics entirely when store is not wired.
func TestBuildPayload_DiagnosticsOmittedWhenStoreNil(t *testing.T) {
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{AnonymousID: "test-id"},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})

	payload := svc.BuildPayload()
	if payload.Diagnostics != nil {
		t.Errorf("expected Diagnostics nil when store is not wired, got %+v", payload.Diagnostics)
	}

	// Verify omitempty: JSON must not contain "diagnostics" key.
	raw, _ := json.Marshal(payload)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["diagnostics"]; ok {
		t.Error("diagnostics key present in JSON despite nil pointer (omitempty broken)")
	}
}

// PH002: BuildPayload omits Diagnostics when all counters are zero.
func TestBuildPayload_DiagnosticsOmittedWhenAllZero(t *testing.T) {
	dir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dir, "test.db"), 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{AnonymousID: "test-id"},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	svc.SetDiagnosticsCounterStore(NewDiagnosticsCounterStore(), db)

	payload := svc.BuildPayload()
	if payload.Diagnostics != nil {
		t.Errorf("expected Diagnostics nil when all counters zero, got %+v", payload.Diagnostics)
	}
}

// PH003: BuildPayload populates Diagnostics when at least one counter is non-zero.
func TestBuildPayload_DiagnosticsPopulatedWhenNonZero(t *testing.T) {
	dir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dir, "test.db"), 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	defer db.Close()

	store := NewDiagnosticsCounterStore()
	_ = store.RecordErrorCode(db, "MCPX_HTTP_401")
	_ = store.RecordFixAttempt(db, "success")

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{AnonymousID: "test-id"},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	svc.SetDiagnosticsCounterStore(store, db)

	payload := svc.BuildPayload()
	if payload.Diagnostics == nil {
		t.Fatal("expected Diagnostics non-nil when counters are non-zero")
	}
	if payload.Diagnostics.ErrorCodeCounts24h["MCPX_HTTP_401"] != 1 {
		t.Errorf("expected MCPX_HTTP_401 count 1, got %d", payload.Diagnostics.ErrorCodeCounts24h["MCPX_HTTP_401"])
	}
	if payload.Diagnostics.FixAttempted24h != 1 {
		t.Errorf("expected FixAttempted24h=1, got %d", payload.Diagnostics.FixAttempted24h)
	}
	if payload.Diagnostics.FixSucceeded24h != 1 {
		t.Errorf("expected FixSucceeded24h=1, got %d", payload.Diagnostics.FixSucceeded24h)
	}
	if payload.Diagnostics.UniqueCodesEver != 1 {
		t.Errorf("expected UniqueCodesEver=1, got %d", payload.Diagnostics.UniqueCodesEver)
	}
}

// PH004: JSON round-trip confirms diagnostics is nested under the payload.
func TestBuildPayload_DiagnosticsJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(dir, "test.db"), 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open: %v", err)
	}
	defer db.Close()

	store := NewDiagnosticsCounterStore()
	_ = store.RecordErrorCode(db, "MCPX_DOCKER_DAEMON_DOWN")

	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{AnonymousID: "rt-id"},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	svc.SetDiagnosticsCounterStore(store, db)

	raw, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var wire struct {
		Diagnostics *struct {
			ErrorCodeCounts24h map[string]int `json:"error_code_counts_24h"`
			FixAttempted24h    int            `json:"fix_attempted_24h"`
			FixSucceeded24h    int            `json:"fix_succeeded_24h"`
			UniqueCodesEver    int            `json:"unique_codes_ever"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if wire.Diagnostics == nil {
		t.Fatal("diagnostics absent from JSON")
	}
	if wire.Diagnostics.UniqueCodesEver != 1 {
		t.Errorf("unique_codes_ever want 1, got %d", wire.Diagnostics.UniqueCodesEver)
	}
	if wire.Diagnostics.ErrorCodeCounts24h["MCPX_DOCKER_DAEMON_DOWN"] != 1 {
		t.Errorf("MCPX_DOCKER_DAEMON_DOWN count want 1, got %d", wire.Diagnostics.ErrorCodeCounts24h["MCPX_DOCKER_DAEMON_DOWN"])
	}
}
