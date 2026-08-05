package telemetry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestRecordTPAScanCountsAndSeverities covers the happy path: completed and
// failed scans are counted separately, a scan with any finding also bumps
// scans_with_findings, and per-severity totals accumulate across scans.
func TestRecordTPAScanCountsAndSeverities(t *testing.T) {
	r := NewCounterRegistry()

	r.RecordTPAScanCompleted(map[string]int{"high": 2, "low": 1})
	r.RecordTPAScanCompleted(map[string]int{"high": 1})
	r.RecordTPAScanCompleted(nil)                       // clean scan: no findings
	r.RecordTPAScanCompleted(map[string]int{})          // clean scan: empty summary
	r.RecordTPAScanCompleted(map[string]int{"info": 0}) // clean scan: zero count
	r.RecordTPAScanFailed()
	r.RecordTPAScanFailed()

	snap := r.Snapshot()
	if snap.TPAScansCompleted != 5 {
		t.Errorf("tpa_scans_completed = %d, want 5", snap.TPAScansCompleted)
	}
	if snap.TPAScansFailed != 2 {
		t.Errorf("tpa_scans_failed = %d, want 2", snap.TPAScansFailed)
	}
	if snap.TPAScansWithFindings != 2 {
		t.Errorf("tpa_scans_with_findings = %d, want 2", snap.TPAScansWithFindings)
	}
	if got := snap.TPAFindings["high"]; got != 3 {
		t.Errorf("findings[high] = %d, want 3", got)
	}
	if got := snap.TPAFindings["low"]; got != 1 {
		t.Errorf("findings[low] = %d, want 1", got)
	}
	// Every severity key is always present in the snapshot, even at zero.
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		if _, ok := snap.TPAFindings[sev]; !ok {
			t.Errorf("snapshot findings missing fixed severity key %q", sev)
		}
	}
	if len(snap.TPAFindings) != 5 {
		t.Errorf("snapshot findings has %d keys, want exactly the 5 fixed severities", len(snap.TPAFindings))
	}
}

// TestRecordTPAScanDropsUnknownSeverities is the privacy guard: any key that
// is not a member of the fixed severity enum (a rule id, a scanner id, a
// server name, a finding title) is silently dropped, and a scan whose findings
// are ALL unknown keys does not count as "with findings".
func TestRecordTPAScanDropsUnknownSeverities(t *testing.T) {
	r := NewCounterRegistry()

	r.RecordTPAScanCompleted(map[string]int{
		"TPA-2026-0001":            4,
		"github:create_issue":      1,
		"/Users/algis/secret/path": 9,
		"HIGH":                     3, // case-sensitive: not the enum value
	})

	snap := r.Snapshot()
	if snap.TPAScansCompleted != 1 {
		t.Errorf("tpa_scans_completed = %d, want 1", snap.TPAScansCompleted)
	}
	if snap.TPAScansWithFindings != 0 {
		t.Errorf("tpa_scans_with_findings = %d, want 0 (all keys were dropped)", snap.TPAScansWithFindings)
	}
	for k, v := range snap.TPAFindings {
		if !IsTPASeverity(k) {
			t.Errorf("snapshot leaked non-enum findings key %q", k)
		}
		if v != 0 {
			t.Errorf("findings[%q] = %d, want 0", k, v)
		}
	}
}

// TestRecordTPAScanIgnoresNegativeCounts asserts a negative severity count can
// never reach the payload (the anonymity contract is non-negative integers).
func TestRecordTPAScanIgnoresNegativeCounts(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordTPAScanCompleted(map[string]int{"high": -5, "low": 2})

	snap := r.Snapshot()
	if got := snap.TPAFindings["high"]; got != 0 {
		t.Errorf("findings[high] = %d, want 0 (negative dropped)", got)
	}
	if got := snap.TPAFindings["low"]; got != 2 {
		t.Errorf("findings[low] = %d, want 2", got)
	}
	if snap.TPAScansWithFindings != 1 {
		t.Errorf("tpa_scans_with_findings = %d, want 1", snap.TPAScansWithFindings)
	}
}

// TestRecordTPAScanOnNilRegistry pins the nil-safety contract of the *On
// wrappers — integration points may hold a nil registry when telemetry is not
// initialized.
func TestRecordTPAScanOnNilRegistry(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("nil-safe wrappers panicked: %v", rec)
		}
	}()
	RecordTPAScanCompletedOn(nil, map[string]int{"high": 1})
	RecordTPAScanFailedOn(nil)

	// And they still work against a real registry.
	r := NewCounterRegistry()
	RecordTPAScanCompletedOn(r, map[string]int{"critical": 1})
	RecordTPAScanFailedOn(r)
	snap := r.Snapshot()
	if snap.TPAScansCompleted != 1 || snap.TPAScansFailed != 1 || snap.TPAFindings["critical"] != 1 {
		t.Errorf("wrappers did not record: %+v", snap)
	}
}

// TestResetClearsTPACounters asserts the v8 counters participate in the
// post-heartbeat reset like every other windowed counter.
func TestResetClearsTPACounters(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordTPAScanCompleted(map[string]int{"medium": 3})
	r.RecordTPAScanFailed()

	r.Reset()

	snap := r.Snapshot()
	if snap.TPAScansCompleted != 0 || snap.TPAScansFailed != 0 || snap.TPAScansWithFindings != 0 {
		t.Errorf("scan counters survived Reset: %+v", snap)
	}
	for sev, n := range snap.TPAFindings {
		if n != 0 {
			t.Errorf("findings[%q] = %d after Reset, want 0", sev, n)
		}
	}
	if stats := snap.TPAScannerStats(); stats != nil {
		t.Errorf("TPAScannerStats() = %+v after Reset, want nil (all-zero omission)", stats)
	}
}

// TestTPAScanSnapshotIsInternallyConsistent guards the atomics-vs-map tearing
// bug: the scalar counters and the findings map must move together under one
// lock, so a Snapshot concurrent with recording can never observe findings
// without the completed scan that produced them (nor scans_with_findings
// exceeding scans_completed).
func TestTPAScanSnapshotIsInternallyConsistent(t *testing.T) {
	r := NewCounterRegistry()

	const scans = 500
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < scans; i++ {
			r.RecordTPAScanCompleted(map[string]int{"high": 1})
			r.RecordTPAScanFailed()
		}
	}()

	for i := 0; i < scans; i++ {
		snap := r.Snapshot()
		var totalFindings int64
		for _, n := range snap.TPAFindings {
			totalFindings += n
		}
		if totalFindings > snap.TPAScansCompleted {
			t.Fatalf("torn snapshot: %d findings but only %d completed scans", totalFindings, snap.TPAScansCompleted)
		}
		if snap.TPAScansWithFindings > snap.TPAScansCompleted {
			t.Fatalf("torn snapshot: scans_with_findings=%d > scans_completed=%d",
				snap.TPAScansWithFindings, snap.TPAScansCompleted)
		}
	}
	<-done

	final := r.Snapshot()
	if final.TPAScansCompleted != scans || final.TPAScansFailed != scans {
		t.Errorf("final counts = completed %d / failed %d, want %d each",
			final.TPAScansCompleted, final.TPAScansFailed, scans)
	}
	if final.TPAFindings["high"] != scans {
		t.Errorf("findings[high] = %d, want %d", final.TPAFindings["high"], scans)
	}
}

// TestTPAScannerStatsOmittedWhenZero asserts the sub-object projection is nil
// (and therefore omitted from the payload) when nothing was recorded.
func TestTPAScannerStatsOmittedWhenZero(t *testing.T) {
	if stats := NewCounterRegistry().Snapshot().TPAScannerStats(); stats != nil {
		t.Fatalf("TPAScannerStats() = %+v on a fresh registry, want nil", stats)
	}
	// A failure alone is enough to make the sub-object non-nil.
	r := NewCounterRegistry()
	r.RecordTPAScanFailed()
	stats := r.Snapshot().TPAScannerStats()
	if stats == nil {
		t.Fatal("TPAScannerStats() = nil after a failed scan, want non-nil")
	}
	if stats.Findings != nil {
		t.Errorf("findings = %v, want nil (sparse: no severities recorded)", stats.Findings)
	}
}

// newTPAPayloadTestService builds a telemetry service with a deterministic
// config, mirroring newFunnelPayloadTestService but with the deep-scan switch
// controllable.
func newTPAPayloadTestService(t *testing.T, deepScan bool) *Service {
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
	}
	if deepScan {
		cfg.Security = &config.SecurityConfig{
			DeepScan: &config.DeepScanConfig{Enabled: true},
		}
	}
	return New(cfg, "", "v1.2.3", "personal", zap.NewNop())
}

// TestPayloadV8_TPAScannerIncludedAndAnonymous is the v8 contract test: a
// payload carrying scanner activity reaches the wire with counts + fixed
// severity keys only, and passes the anonymity scanner.
func TestPayloadV8_TPAScannerIncludedAndAnonymous(t *testing.T) {
	svc := newTPAPayloadTestService(t, true)
	reg := svc.Registry()
	reg.RecordTPAScanCompleted(map[string]int{"critical": 1, "high": 2})
	reg.RecordTPAScanCompleted(nil)
	reg.RecordTPAScanFailed()

	payload := svc.BuildPayload()
	if payload.TPAScanner == nil {
		t.Fatal("payload.tpa_scanner = nil, want the v8 sub-object")
	}
	if payload.TPAScanner.ScansCompleted != 2 {
		t.Errorf("scans_completed = %d, want 2", payload.TPAScanner.ScansCompleted)
	}
	if payload.TPAScanner.ScansFailed != 1 {
		t.Errorf("scans_failed = %d, want 1", payload.TPAScanner.ScansFailed)
	}
	if payload.TPAScanner.ScansWithFindings != 1 {
		t.Errorf("scans_with_findings = %d, want 1", payload.TPAScanner.ScansWithFindings)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)

	for _, required := range []string{
		`"schema_version":8`,
		`"tpa_scanner":`,
		`"scans_completed":2`,
		`"scans_failed":1`,
		`"scans_with_findings":1`,
		`"critical":1`,
		`"high":2`,
		`"deep_scan_enabled":true`,
	} {
		if !strings.Contains(js, required) {
			t.Errorf("expected v8 payload to contain %s, missing from:\n%s", required, js)
		}
	}
	// Sparse findings: severities that were never seen do not appear.
	if strings.Contains(js, `"medium"`) {
		t.Errorf("zero-valued severity leaked into findings:\n%s", js)
	}

	prev := BlockedValues
	BlockedValues = nil
	defer func() { BlockedValues = prev }()
	if scanErr := ScanForPII(data); scanErr != nil {
		t.Fatalf("v8 payload with tpa_scanner must pass ScanForPII, got: %v\npayload:\n%s", scanErr, js)
	}
}

// TestPayloadV8_TPAScannerOmittedWhenNoScans asserts the additive-only
// contract: an install that never scanned emits a payload shape-identical to
// v7 (no tpa_scanner key at all).
func TestPayloadV8_TPAScannerOmittedWhenNoScans(t *testing.T) {
	svc := newTPAPayloadTestService(t, false)

	payload := svc.BuildPayload()
	if payload.TPAScanner != nil {
		t.Errorf("payload.tpa_scanner = %+v, want nil when no scan ran", payload.TPAScanner)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if strings.Contains(js, `"tpa_scanner"`) {
		t.Errorf("tpa_scanner must be omitted on a zero-valued payload, got:\n%s", js)
	}
	if !strings.Contains(js, `"deep_scan_enabled":false`) {
		t.Errorf("expected deep_scan_enabled:false, got:\n%s", js)
	}
}

// TestBuildFeatureFlagSnapshot_DeepScan covers the v8 feature flag across the
// nil-config, nil-security, disabled, and enabled cases.
func TestBuildFeatureFlagSnapshot_DeepScan(t *testing.T) {
	if snap := BuildFeatureFlagSnapshot(nil); snap.DeepScanEnabled {
		t.Error("deep_scan_enabled = true for a nil config, want false")
	}
	if snap := BuildFeatureFlagSnapshot(&config.Config{}); snap.DeepScanEnabled {
		t.Error("deep_scan_enabled = true with no security block, want false")
	}
	cfgNilDeep := &config.Config{Security: &config.SecurityConfig{}}
	if snap := BuildFeatureFlagSnapshot(cfgNilDeep); snap.DeepScanEnabled {
		t.Error("deep_scan_enabled = true with a nil deep_scan block, want false")
	}
	cfgOff := &config.Config{Security: &config.SecurityConfig{DeepScan: &config.DeepScanConfig{Enabled: false}}}
	if snap := BuildFeatureFlagSnapshot(cfgOff); snap.DeepScanEnabled {
		t.Error("deep_scan_enabled = true while deep_scan.enabled=false")
	}
	cfgOn := &config.Config{Security: &config.SecurityConfig{DeepScan: &config.DeepScanConfig{Enabled: true}}}
	if snap := BuildFeatureFlagSnapshot(cfgOn); !snap.DeepScanEnabled {
		t.Error("deep_scan_enabled = false while deep_scan.enabled=true")
	}
}

// TestScanForPII_TPAScannerShapeViolations pins the wire-form backstop: even
// if producer-side filtering regressed, a tpa_scanner sub-object carrying a
// non-enum key, a negative count, a string count, or an unknown field is
// rejected before transmit.
func TestScanForPII_TPAScannerShapeViolations(t *testing.T) {
	prev := BlockedValues
	BlockedValues = nil
	defer func() { BlockedValues = prev }()

	clean := []string{
		`{"anonymous_id":"abc","schema_version":8}`,
		`{"anonymous_id":"abc","schema_version":8,"tpa_scanner":{"scans_completed":0,"scans_failed":0,"scans_with_findings":0}}`,
		`{"anonymous_id":"abc","schema_version":8,"tpa_scanner":{"scans_completed":3,"scans_failed":1,"scans_with_findings":2,` +
			`"findings":{"critical":1,"high":2,"medium":3,"low":4,"info":5}}}`,
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
		{"non-enum findings key (rule id)",
			`{"tpa_scanner":{"findings":{"TPA-2026-0001":2}}}`},
		{"non-enum findings key (server name)",
			`{"tpa_scanner":{"findings":{"my-private-server":1}}}`},
		{"negative scalar",
			`{"tpa_scanner":{"scans_completed":-1}}`},
		{"negative finding count",
			`{"tpa_scanner":{"findings":{"high":-2}}}`},
		{"string scalar",
			`{"tpa_scanner":{"scans_failed":"3"}}`},
		{"string finding count",
			`{"tpa_scanner":{"findings":{"high":"many"}}}`},
		{"unknown sub-key",
			`{"tpa_scanner":{"scans_completed":1,"server_name":"github"}}`},
		{"not an object",
			`{"tpa_scanner":"github"}`},
		{"findings not an object",
			`{"tpa_scanner":{"findings":["high"]}}`},
		// json.Unmarshal accepts null into a nil map, so null must be
		// rejected explicitly — it is not the required object shape.
		{"null tpa_scanner",
			`{"tpa_scanner":null}`},
		{"null findings",
			`{"tpa_scanner":{"scans_completed":1,"findings":null}}`},
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
			continue
		}
		if v.Rule != "v8_field_invalid" {
			t.Errorf("%s: rule = %q, want v8_field_invalid", tc.name, v.Rule)
		}
		// The violation must never echo the offending (possibly identifying)
		// map KEY or value back into logs — keys are where a server name
		// would leak in, so "server_name" is in this list on purpose.
		for _, secret := range []string{"TPA-2026-0001", "my-private-server", "github", "server_name"} {
			if strings.Contains(v.Pattern, secret) || strings.Contains(v.Reason, secret) {
				t.Errorf("%s: violation echoed %q back: pattern=%q reason=%q",
					tc.name, secret, v.Pattern, v.Reason)
			}
		}
	}
}
