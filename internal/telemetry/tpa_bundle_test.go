package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// bundleObj wraps the given tpa_scanner members into a minimal payload.
func bundleObj(members string) []byte {
	return []byte(`{"anonymous_id":"abc","schema_version":16,"tpa_scanner":{` + members + `}}`)
}

func u64(v uint64) *uint64 { return &v }

// TestScanForPII_TPABundleTupleAccepted covers every valid tuple of the
// Spec 101 FR-021 bundle keys.
func TestScanForPII_TPABundleTupleAccepted(t *testing.T) {
	withoutBlockedValues(t)
	clean := []string{
		`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":3`,
		`"bundle_source":"embedded","bundle_signature_verified":true,"bundle_version":"12.34","bundle_sequence":0`,
		`"bundle_source":"file","bundle_signature_verified":true,"bundle_version":"0.1","bundle_sequence":9007199254740991`,
		`"bundle_source":"fetch","bundle_signature_verified":true,"bundle_version":"1.0","bundle_sequence":7`,
		`"bundle_source":"file","bundle_signature_verified":false,"bundle_version":"other"`,
		`"bundle_source":"fetch","bundle_signature_verified":true,"bundle_version":"other"`,
		`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"other"`,
		// Together with the existing counters and findings.
		`"scans_completed":1,"scans_failed":0,"scans_with_findings":1,"findings":{"high":1},` +
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":1`,
	}
	for _, m := range clean {
		if err := ScanForPII(bundleObj(m)); err != nil {
			t.Errorf("valid tuple rejected: %v\n%s", err, m)
		}
	}
}

// TestScanForPII_TPABundleTupleViolations pins the whole-tuple rule and the
// typed-key shapes. Every violation is a v8_field_invalid and never echoes the
// offending key or value.
func TestScanForPII_TPABundleTupleViolations(t *testing.T) {
	withoutBlockedValues(t)
	const goodTail = `"bundle_signature_verified":true,"bundle_version":"0.1","bundle_sequence":5`
	dirty := []struct{ name, members string }{
		{"free-string bundle_version",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1.7-acme","bundle_sequence":1`},
		{"three-part version",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1.7","bundle_sequence":1`},
		{"four-digit schema line",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"1000.1","bundle_sequence":1`},
		{"empty bundle_version",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"","bundle_sequence":1`},
		{"non-string bundle_version",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":0.1,"bundle_sequence":1`},
		{"sequence with other",
			`"bundle_source":"file","bundle_signature_verified":true,"bundle_version":"other","bundle_sequence":4`},
		{"schema line without sequence",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1"`},
		{"file unverified schema line",
			`"bundle_source":"file","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":2`},
		{"fetch unverified schema line",
			`"bundle_source":"fetch","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":2`},
		{"unknown source",
			`"bundle_source":"usb",` + goodTail},
		{"empty source",
			`"bundle_source":"",` + goodTail},
		{"non-string source",
			`"bundle_source":1,` + goodTail},
		{"non-bool signature_verified",
			`"bundle_source":"file","bundle_signature_verified":"true","bundle_version":"0.1","bundle_sequence":5`},
		{"numeric signature_verified",
			`"bundle_source":"file","bundle_signature_verified":1,"bundle_version":"0.1","bundle_sequence":5`},
		{"sequence 2^53",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":9007199254740992`},
		{"negative sequence",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":-1`},
		{"float sequence",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":1.5`},
		{"string sequence",
			`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"0.1","bundle_sequence":"5"`},
		{"version without source",
			`"bundle_signature_verified":false,"bundle_version":"other"`},
		{"source without version",
			`"bundle_source":"embedded","bundle_signature_verified":false`},
		{"source without signature_verified",
			`"bundle_source":"embedded","bundle_version":"other"`},
		{"sequence alone",
			`"bundle_sequence":5`},
		{"null source",
			`"bundle_source":null,` + goodTail},
	}
	for _, tc := range dirty {
		err := ScanForPII(bundleObj(tc.members))
		if err == nil {
			t.Errorf("%s: expected a violation for %s", tc.name, tc.members)
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
		for _, secret := range []string{"0.1.7", "acme", "usb", "9007199254740992", "1000.1"} {
			if strings.Contains(v.Pattern, secret) || strings.Contains(v.Reason, secret) {
				t.Errorf("%s: violation echoed %q: pattern=%q reason=%q", tc.name, secret, v.Pattern, v.Reason)
			}
		}
	}
}

// TestScanForPII_TPABundleUnknownKeyStillRejected: the bundle group widens the
// whitelist by exactly four keys.
func TestScanForPII_TPABundleUnknownKeyStillRejected(t *testing.T) {
	withoutBlockedValues(t)
	err := ScanForPII(bundleObj(`"bundle_source":"embedded","bundle_signature_verified":false,"bundle_version":"other","bundle_path":"/x"`))
	if err == nil {
		t.Fatal("unknown bundle_* key must be rejected")
	}
}

func newBundleTestService(t *testing.T) *Service {
	t.Helper()
	return newTPAPayloadTestService(t, false)
}

// TestTPABundleProvider_ZeroCountersStillCarriesObject: bundle state is
// per-heartbeat state, so tpa_scanner appears even when no scan ran.
func TestTPABundleProvider_ZeroCountersStillCarriesObject(t *testing.T) {
	svc := newBundleTestService(t)
	svc.SetTPABundleProvider(func() *TPABundleSnapshot {
		return &TPABundleSnapshot{Source: "embedded", SignatureVerified: false, BundleVersion: "0.1", Sequence: u64(42)}
	})

	payload := svc.BuildPayload()
	if payload.TPAScanner == nil {
		t.Fatal("tpa_scanner = nil, want an object carrying the bundle keys")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	for _, want := range []string{
		`"schema_version":16`,
		`"tpa_scanner":{`,
		`"scans_completed":0`,
		`"bundle_source":"embedded"`,
		`"bundle_signature_verified":false`,
		`"bundle_version":"0.1"`,
		`"bundle_sequence":42`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("payload missing %s:\n%s", want, js)
		}
	}
	withoutBlockedValues(t)
	if err := ScanForPII(data); err != nil {
		t.Fatalf("payload must pass the backstop: %v\n%s", err, js)
	}
}

// TestTPABundleProvider_OtherOmitsSequence: the "other" form carries no
// sequence key and a verified=false boolean is still transmitted (not
// omitted).
func TestTPABundleProvider_OtherOmitsSequence(t *testing.T) {
	svc := newBundleTestService(t)
	svc.SetTPABundleProvider(func() *TPABundleSnapshot {
		return &TPABundleSnapshot{Source: "file", SignatureVerified: false, BundleVersion: "other"}
	})
	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	if strings.Contains(js, "bundle_sequence") {
		t.Errorf("bundle_sequence must be absent for other:\n%s", js)
	}
	if !strings.Contains(js, `"bundle_signature_verified":false`) {
		t.Errorf("bundle_signature_verified:false must be transmitted:\n%s", js)
	}
	withoutBlockedValues(t)
	if err := ScanForPII(data); err != nil {
		t.Fatalf("backstop: %v\n%s", err, js)
	}
}

// TestTPABundleProvider_MergesWithCountersAndSurvivesPendingSnapshot: the
// merge must survive applyRegistrySnapshot (which replaces TPAScanner) on the
// pending-snapshot path, and must not be cleared by the counter reset.
func TestTPABundleProvider_MergesWithCountersAndSurvivesPendingSnapshot(t *testing.T) {
	svc := newBundleTestService(t)
	svc.SetTPABundleProvider(func() *TPABundleSnapshot {
		return &TPABundleSnapshot{Source: "fetch", SignatureVerified: true, BundleVersion: "0.1", Sequence: u64(7)}
	})
	svc.Registry().RecordTPAScanCompleted(map[string]int{"high": 1})

	// Pending snapshot path (BuildPayload applies it after the build).
	snap := svc.Registry().Drain()
	svc.pendingCounterSnapshot = &snap
	payload := svc.BuildPayload()
	if payload.TPAScanner == nil || payload.TPAScanner.ScansCompleted != 1 {
		t.Fatalf("counters lost: %+v", payload.TPAScanner)
	}
	if payload.TPAScanner.BundleSource != "fetch" || payload.TPAScanner.BundleSequence == nil || *payload.TPAScanner.BundleSequence != 7 {
		t.Fatalf("bundle keys lost after applyRegistrySnapshot: %+v", payload.TPAScanner)
	}

	// After the accepted-send reset the next heartbeat still reports the bundle.
	svc.pendingCounterSnapshot = nil
	payload = svc.BuildPayload()
	if payload.TPAScanner == nil || payload.TPAScanner.BundleSource != "fetch" {
		t.Fatalf("bundle state cleared by counter reset: %+v", payload.TPAScanner)
	}
	if payload.TPAScanner.ScansCompleted != 0 {
		t.Errorf("scans_completed = %d, want 0 after drain", payload.TPAScanner.ScansCompleted)
	}
}

// TestTPABundleProvider_NilAddsNothing: a provider returning nil leaves the
// payload exactly as before.
func TestTPABundleProvider_NilAddsNothing(t *testing.T) {
	svc := newBundleTestService(t)
	svc.SetTPABundleProvider(func() *TPABundleSnapshot { return nil })
	payload := svc.BuildPayload()
	if payload.TPAScanner != nil {
		t.Errorf("tpa_scanner = %+v, want nil", payload.TPAScanner)
	}
	data, _ := json.Marshal(payload)
	if strings.Contains(string(data), "bundle_") || strings.Contains(string(data), "tpa_scanner") {
		t.Errorf("bundle keys or tpa_scanner appeared with a nil snapshot:\n%s", data)
	}

	// With scan counters and a nil snapshot: exactly the old object.
	svc.Registry().RecordTPAScanFailed()
	payload = svc.BuildPayload()
	if payload.TPAScanner == nil || payload.TPAScanner.BundleSource != "" || payload.TPAScanner.BundleSequence != nil {
		t.Errorf("unexpected bundle fields: %+v", payload.TPAScanner)
	}
	data, _ = json.Marshal(payload)
	if strings.Contains(string(data), "bundle_") {
		t.Errorf("bundle keys leaked with a nil snapshot:\n%s", data)
	}
}

// TestTPABundleProvider_NotConsultedAfterOptOut: the existing opt-out latch
// returns from sendHeartbeat before any payload is built, so the provider is
// never called and nothing is transmitted.
func TestTPABundleProvider_NotConsultedAfterOptOut(t *testing.T) {
	clearTelemetryEnv(t)

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(true), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	var calls atomic.Int32
	svc.SetTPABundleProvider(func() *TPABundleSnapshot {
		calls.Add(1)
		return &TPABundleSnapshot{Source: "embedded", BundleVersion: "0.1", Sequence: u64(1)}
	})
	svc.optedOut.Store(true)

	svc.sendHeartbeat(context.Background())

	if calls.Load() != 0 {
		t.Errorf("bundle provider consulted %d times after opt-out", calls.Load())
	}
	if hits.Load() != 0 {
		t.Errorf("heartbeat transmitted after opt-out: %d", hits.Load())
	}
}
