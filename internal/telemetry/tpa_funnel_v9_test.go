package telemetry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestSchemaVersionIsV9 pins the schema bump that carries the TPA funnel
// counters plus trust_mode_distribution.
func TestSchemaVersionIsV9(t *testing.T) {
	if SchemaVersion != 9 {
		t.Fatalf("SchemaVersion = %d, want 9", SchemaVersion)
	}
}

// TestRecordTPAFunnelScans covers the two new v9 delta counters: the
// synchronous trust_mode:scan tool-change gate and the aggregated-prompt
// poisoning filter, each counted independently of the async job counters.
func TestRecordTPAFunnelScans(t *testing.T) {
	r := NewCounterRegistry()

	r.RecordTPAToolChangeGateScan()
	r.RecordTPAToolChangeGateScan()
	r.RecordTPAToolChangeGateScan()
	r.RecordTPAPromptScan()
	r.RecordTPAPromptScan()

	snap := r.Snapshot()
	if snap.TPAToolChangeGateScans != 3 {
		t.Errorf("tpa_tool_change_gate_scans = %d, want 3", snap.TPAToolChangeGateScans)
	}
	if snap.TPAPromptScans != 2 {
		t.Errorf("tpa_prompt_scans = %d, want 2", snap.TPAPromptScans)
	}
	// The funnel counters are independent of the job-level counters.
	if snap.TPAScansCompleted != 0 || snap.TPAScansFailed != 0 || snap.TPAScansWithFindings != 0 {
		t.Errorf("funnel scans leaked into the job counters: %+v", snap)
	}

	stats := snap.TPAScannerStats()
	if stats == nil {
		t.Fatal("TPAScannerStats() = nil after funnel scans, want the sub-object")
	}
	if stats.ToolChangeGateScans != 3 || stats.PromptScans != 2 {
		t.Errorf("stats = %+v, want gate=3 prompt=2", stats)
	}
}

// TestRecordTPAFunnelScansOnNilRegistry pins the nil-safety contract of the
// *On wrappers — the two call sites may hold a nil registry when telemetry is
// not initialized yet.
func TestRecordTPAFunnelScansOnNilRegistry(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("nil-safe wrappers panicked: %v", rec)
		}
	}()
	RecordTPAToolChangeGateScanOn(nil)
	RecordTPAPromptScanOn(nil)

	r := NewCounterRegistry()
	RecordTPAToolChangeGateScanOn(r)
	RecordTPAPromptScanOn(r)
	snap := r.Snapshot()
	if snap.TPAToolChangeGateScans != 1 || snap.TPAPromptScans != 1 {
		t.Errorf("wrappers did not record: %+v", snap)
	}
}

// TestResetClearsTPAFunnelCounters asserts the v9 counters participate in the
// post-accepted-send reset like every other windowed counter.
func TestResetClearsTPAFunnelCounters(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordTPAToolChangeGateScan()
	r.RecordTPAPromptScan()

	r.Reset()

	snap := r.Snapshot()
	if snap.TPAToolChangeGateScans != 0 || snap.TPAPromptScans != 0 {
		t.Errorf("funnel counters survived Reset: %+v", snap)
	}
	if stats := snap.TPAScannerStats(); stats != nil {
		t.Errorf("TPAScannerStats() = %+v after Reset, want nil (all-zero omission)", stats)
	}
}

// TestTPAScannerStatsOmittedWhenOnlyFunnelZero asserts the omit-when-all-zero
// posture still holds with the two new counters folded into isZero, and that a
// funnel-only install (gate scans but no async jobs) DOES emit the sub-object.
func TestTPAScannerStatsOmittedWhenOnlyFunnelZero(t *testing.T) {
	if stats := NewCounterRegistry().Snapshot().TPAScannerStats(); stats != nil {
		t.Fatalf("TPAScannerStats() = %+v on a fresh registry, want nil", stats)
	}

	gateOnly := NewCounterRegistry()
	gateOnly.RecordTPAToolChangeGateScan()
	if stats := gateOnly.Snapshot().TPAScannerStats(); stats == nil {
		t.Fatal("TPAScannerStats() = nil after a gate scan, want non-nil")
	}

	promptOnly := NewCounterRegistry()
	promptOnly.RecordTPAPromptScan()
	if stats := promptOnly.Snapshot().TPAScannerStats(); stats == nil {
		t.Fatal("TPAScannerStats() = nil after a prompt scan, want non-nil")
	}
}

// newV9PayloadTestService builds a telemetry service with a deterministic
// config and the servers under test. CI/DO_NOT_TRACK are pinned empty so the
// env-based opt-out never changes the payload under test.
func newV9PayloadTestService(t *testing.T, servers []*config.ServerConfig) *Service {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	cfg := &config.Config{
		EnableSocket: true,
		Features:     &config.FeatureFlags{EnableWebUI: true},
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
		},
		Servers: servers,
	}
	return New(cfg, "", "v1.2.3", "personal", zap.NewNop())
}

// TestBuildTrustModeDistribution covers the fixed-key histogram over
// EffectiveTrustMode(): explicit modes, the empty (inherit) default, a typo'd
// mode failing closed to manual, and the legacy-field fallbacks.
func TestBuildTrustModeDistribution(t *testing.T) {
	autoTrue := true
	autoFalse := false

	tests := []struct {
		name string
		cfg  *config.Config
		want map[string]int
	}{
		{
			name: "nil config yields the zeroed fixed keys",
			cfg:  nil,
			want: map[string]int{"auto": 0, "scan": 0, "manual": 0},
		},
		{
			name: "no servers yields the zeroed fixed keys",
			cfg:  &config.Config{},
			want: map[string]int{"auto": 0, "scan": 0, "manual": 0},
		},
		{
			name: "explicit modes",
			cfg: &config.Config{Servers: []*config.ServerConfig{
				{Name: "a", TrustMode: "auto"},
				{Name: "b", TrustMode: "scan"},
				{Name: "c", TrustMode: "scan"},
				{Name: "d", TrustMode: "manual"},
			}},
			want: map[string]int{"auto": 1, "scan": 2, "manual": 1},
		},
		{
			name: "empty mode inherits manual; typo fails closed to manual",
			cfg: &config.Config{Servers: []*config.ServerConfig{
				{Name: "a"},
				{Name: "b", TrustMode: "Scan"},
				{Name: "c", TrustMode: "off"},
			}},
			want: map[string]int{"auto": 0, "scan": 0, "manual": 3},
		},
		{
			name: "legacy auto_approve_tool_changes fallback",
			cfg: &config.Config{Servers: []*config.ServerConfig{
				{Name: "a", AutoApproveToolChanges: &autoTrue},
				{Name: "b", AutoApproveToolChanges: &autoFalse},
			}},
			want: map[string]int{"auto": 1, "scan": 0, "manual": 1},
		},
		{
			name: "nil server entries are skipped",
			cfg: &config.Config{Servers: []*config.ServerConfig{
				nil,
				{Name: "a", TrustMode: "auto"},
			}},
			want: map[string]int{"auto": 1, "scan": 0, "manual": 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildTrustModeDistribution(tc.cfg)
			if len(got) != len(trustModeKeys) {
				t.Fatalf("distribution = %v, want exactly the %d fixed keys", got, len(trustModeKeys))
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("distribution[%q] = %d, want %d (full: %v)", k, got[k], want, got)
				}
			}
		})
	}
}

// TestPayloadV9_TrustModeDistributionAndFunnelCounters is the v9 contract
// test: the payload carries schema_version 9, the trust-mode histogram, and
// the two new tpa_scanner counters — and it stays anonymous.
func TestPayloadV9_TrustModeDistributionAndFunnelCounters(t *testing.T) {
	svc := newV9PayloadTestService(t, []*config.ServerConfig{
		{Name: "a", TrustMode: "auto"},
		{Name: "b", TrustMode: "scan"},
		{Name: "c"},
	})
	reg := svc.Registry()
	reg.RecordTPAToolChangeGateScan()
	reg.RecordTPAToolChangeGateScan()
	reg.RecordTPAPromptScan()

	payload := svc.BuildPayload()

	if payload.SchemaVersion != 9 {
		t.Errorf("schema_version = %d, want 9", payload.SchemaVersion)
	}
	if payload.TrustModeDistribution == nil {
		t.Fatal("payload.trust_mode_distribution = nil, want the v9 histogram")
	}
	if got := payload.TrustModeDistribution["auto"]; got != 1 {
		t.Errorf("trust_mode_distribution[auto] = %d, want 1", got)
	}
	if got := payload.TrustModeDistribution["scan"]; got != 1 {
		t.Errorf("trust_mode_distribution[scan] = %d, want 1", got)
	}
	if got := payload.TrustModeDistribution["manual"]; got != 1 {
		t.Errorf("trust_mode_distribution[manual] = %d, want 1", got)
	}
	if payload.TPAScanner == nil {
		t.Fatal("payload.tpa_scanner = nil, want the sub-object after funnel scans")
	}
	if payload.TPAScanner.ToolChangeGateScans != 2 {
		t.Errorf("tool_change_gate_scans = %d, want 2", payload.TPAScanner.ToolChangeGateScans)
	}
	if payload.TPAScanner.PromptScans != 1 {
		t.Errorf("prompt_scans = %d, want 1", payload.TPAScanner.PromptScans)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	for _, required := range []string{
		`"schema_version":9`,
		`"trust_mode_distribution":`,
		`"tool_change_gate_scans":2`,
		`"prompt_scans":1`,
	} {
		if !strings.Contains(js, required) {
			t.Errorf("expected v9 payload to contain %s, missing from:\n%s", required, js)
		}
	}

	prev := BlockedValues
	BlockedValues = nil
	defer func() { BlockedValues = prev }()
	if scanErr := ScanForPII(data); scanErr != nil {
		t.Fatalf("v9 payload must pass ScanForPII, got: %v\npayload:\n%s", scanErr, js)
	}
}

// TestPayloadV9_TPAScannerStillOmittedWhenNoScans asserts the v8 omission
// posture is preserved: an install that scanned nothing at all — neither an
// async job nor a gate/prompt scan — emits no tpa_scanner key.
func TestPayloadV9_TPAScannerStillOmittedWhenNoScans(t *testing.T) {
	svc := newV9PayloadTestService(t, nil)

	payload := svc.BuildPayload()
	if payload.TPAScanner != nil {
		t.Errorf("payload.tpa_scanner = %+v, want nil when nothing scanned", payload.TPAScanner)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if strings.Contains(js, `"tpa_scanner"`) {
		t.Errorf("tpa_scanner must be omitted when nothing scanned, got:\n%s", js)
	}
	// trust_mode_distribution is a STATE field, not a delta counter: it is
	// always emitted (all-zero on an install with no servers configured).
	if !strings.Contains(js, `"trust_mode_distribution":{"auto":0,"manual":0,"scan":0}`) {
		t.Errorf("expected an all-zero trust_mode_distribution, got:\n%s", js)
	}
}

// TestScanForPII_V9ShapeViolations pins the wire-form backstop for the two new
// tpa_scanner counters and the trust-mode histogram.
func TestScanForPII_V9ShapeViolations(t *testing.T) {
	prev := BlockedValues
	BlockedValues = nil
	defer func() { BlockedValues = prev }()

	clean := []string{
		`{"anonymous_id":"abc","schema_version":9}`,
		`{"anonymous_id":"abc","schema_version":9,"tpa_scanner":{"scans_completed":0,"scans_failed":0,` +
			`"scans_with_findings":0,"tool_change_gate_scans":4,"prompt_scans":9}}`,
		`{"anonymous_id":"abc","schema_version":9,"trust_mode_distribution":{"auto":0,"scan":2,"manual":7}}`,
	}
	for _, js := range clean {
		if err := ScanForPII([]byte(js)); err != nil {
			t.Errorf("clean payload rejected: %v\n%s", err, js)
		}
	}

	dirty := []struct {
		name    string
		payload string
	}{
		{"negative gate counter", `{"tpa_scanner":{"tool_change_gate_scans":-1}}`},
		{"string prompt counter", `{"tpa_scanner":{"prompt_scans":"3"}}`},
		{"trust mode key outside the enum",
			`{"trust_mode_distribution":{"auto":1,"github-private":2}}`},
		{"negative trust mode count", `{"trust_mode_distribution":{"manual":-1}}`},
		{"string trust mode count", `{"trust_mode_distribution":{"manual":"many"}}`},
		{"trust mode not an object", `{"trust_mode_distribution":"auto"}`},
		{"null trust mode distribution", `{"trust_mode_distribution":null}`},
	}
	for _, tc := range dirty {
		err := ScanForPII([]byte(tc.payload))
		if err == nil {
			t.Errorf("%s: expected an anonymity violation, got nil for %s", tc.name, tc.payload)
			continue
		}
		var v *AnonymityViolation
		if !errors.As(err, &v) {
			t.Errorf("%s: expected *AnonymityViolation, got %T", tc.name, err)
		}
	}
}
