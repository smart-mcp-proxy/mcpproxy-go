package telemetry

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestScanForPII_CurrentErrorCodesShapeViolations extends the spec-095
// wire-form backstop to the schema-v11 standing-state map (MCP-2967).
// current_error_codes is populated by a provider OUTSIDE this package (a
// closure over the supervisor's stateview), so the scanner is the last place a
// leaked server name or URL can be stopped. Keys are where identifying strings
// would leak, so the violation must never echo the offending key.
func TestScanForPII_CurrentErrorCodesShapeViolations(t *testing.T) {
	withoutBlockedValues(t)

	dirty := []struct {
		name    string
		payload string
	}{
		{"server name as key",
			`{"diagnostics":{"current_error_codes":{"my-internal-github-mcp":2}}}`},
		{"URL as key",
			`{"diagnostics":{"current_error_codes":{"https://mcp.corp.example/sse":1}}}`},
		{"uncataloged MCPX-looking code",
			`{"diagnostics":{"current_error_codes":{"MCPX_TOTALLY_MADE_UP":1}}}`},
		{"negative count",
			`{"diagnostics":{"current_error_codes":{"MCPX_HTTP_CONN_REFUSED":-1}}}`},
		{"fractional count",
			`{"diagnostics":{"current_error_codes":{"MCPX_HTTP_CONN_REFUSED":1.5}}}`},
		{"string count",
			`{"diagnostics":{"current_error_codes":{"MCPX_HTTP_CONN_REFUSED":"2"}}}`},
		{"not an object",
			`{"diagnostics":{"current_error_codes":["MCPX_HTTP_CONN_REFUSED"]}}`},
	}

	for _, tc := range dirty {
		t.Run(tc.name, func(t *testing.T) {
			err := ScanForPII([]byte(tc.payload))
			if err == nil {
				t.Fatalf("scanner accepted a malformed current_error_codes map: %s", tc.payload)
			}
			if !errors.Is(err, ErrAnonymityViolation) {
				t.Fatalf("violation does not satisfy errors.Is(ErrAnonymityViolation): %v", err)
			}
			var viol *AnonymityViolation
			if !errors.As(err, &viol) {
				t.Fatalf("not an *AnonymityViolation: %v", err)
			}
			if strings.Contains(viol.Reason, "my-internal-github-mcp") ||
				strings.Contains(viol.Reason, "mcp.corp.example") {
				t.Fatalf("violation echoed the identifying key back: %q", viol.Reason)
			}
		})
	}
}

// TestScanForPII_CurrentErrorCodesAccepted keeps the happy path honest: a
// well-formed standing-state map must pass, alongside the 24h counters.
func TestScanForPII_CurrentErrorCodesAccepted(t *testing.T) {
	withoutBlockedValues(t)

	clean := []string{
		`{"anonymous_id":"abc","schema_version":11}`,
		`{"diagnostics":{"current_error_codes":{"MCPX_OAUTH_LOGIN_REQUIRED":3,"MCPX_HTTP_DNS_FAILED":1}}}`,
		`{"diagnostics":{"error_code_counts_24h":{"MCPX_HTTP_TIMEOUT":2},` +
			`"current_error_codes":{"MCPX_HTTP_TIMEOUT":1},` +
			`"fix_attempted_24h":0,"fix_succeeded_24h":0,"unique_codes_ever":1}}`,
	}
	for _, js := range clean {
		if err := ScanForPII([]byte(js)); err != nil {
			t.Errorf("clean payload rejected: %v\n%s", err, js)
		}
	}
}

// TestDiagnosticsCounters_CurrentErrorCodesKeepsPayloadPresent is the whole
// point of the companion field. Edge-triggering error_code_counts_24h means a
// permanently parked install stops incrementing it, the 24h window decays to
// empty, and isZero() would then drop the entire diagnostics object from the
// heartbeat — absent, which downstream reads as zero. A standing failure must
// keep the object on the wire.
func TestDiagnosticsCounters_CurrentErrorCodesKeepsPayloadPresent(t *testing.T) {
	decayed := DiagnosticsCounters{
		CurrentErrorCodes: map[string]int{"MCPX_OAUTH_LOGIN_REQUIRED": 4},
	}
	if decayed.isZero() {
		t.Fatal("a standing failure with fully decayed 24h counters was treated as zero; " +
			"the diagnostics object would be omitted and the install would read as healthy")
	}

	js, err := json.Marshal(decayed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(js), `"current_error_codes":{"MCPX_OAUTH_LOGIN_REQUIRED":4}`) {
		t.Fatalf("current_error_codes missing from wire form: %s", js)
	}
	if strings.Contains(string(js), "error_code_counts_24h") {
		t.Fatalf("empty 24h map should be dropped by omitempty: %s", js)
	}

	// Nothing standing and nothing counted stays omitted entirely, so an
	// install with no problems is shape-identical to a pre-v11 payload.
	if !(DiagnosticsCounters{}).isZero() {
		t.Fatal("an all-zero DiagnosticsCounters is no longer isZero()")
	}
}

// TestDiagnosticsCounters_CurrentErrorCodesCapped pins the payload-size bound:
// the standing map gets the same top-20 cap as error_code_counts_24h.
func TestDiagnosticsCounters_CurrentErrorCodesCapped(t *testing.T) {
	d := DiagnosticsCounters{CurrentErrorCodes: map[string]int{}}
	for i := 0; i < maxDiagCodeEntries+7; i++ {
		d.CurrentErrorCodes[string(rune('A'+i))] = i + 1
	}

	js, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		CurrentErrorCodes map[string]int `json:"current_error_codes"`
	}
	if err := json.Unmarshal(js, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(wire.CurrentErrorCodes) != maxDiagCodeEntries {
		t.Fatalf("current_error_codes carried %d entries, want the %d cap",
			len(wire.CurrentErrorCodes), maxDiagCodeEntries)
	}
}

// TestSanitizeMCPXCodeMap covers the producer-side filter that sits between
// the supervisor-backed provider and the payload.
func TestSanitizeMCPXCodeMap(t *testing.T) {
	if got := sanitizeMCPXCodeMap(nil); got != nil {
		t.Fatalf("nil in, want nil out, got %v", got)
	}
	if got := sanitizeMCPXCodeMap(map[string]int{}); got != nil {
		t.Fatalf("empty in, want nil out, got %v", got)
	}

	got := sanitizeMCPXCodeMap(map[string]int{
		"MCPX_HTTP_CONN_REFUSED":     2,
		"my-server":                  5, // free text — must be dropped
		"MCPX_HTTP_TIMEOUT":          0, // not standing — must be dropped
		"MCPX_HTTP_DNS_FAILED":       -3,
		"mcpx_lowercase":             1,
		strings.Repeat("MCPX_A", 40): 1, // over the length bound
		// Correctly SHAPED but not in the diagnostics catalog. A shape-only
		// filter keeps this, and ScanForPII then drops the whole heartbeat
		// (not just the key) — so the producer must reject it here.
		"MCPX_TOTALLY_MADE_UP": 1,
	})
	want := map[string]int{"MCPX_HTTP_CONN_REFUSED": 2}
	if len(got) != len(want) {
		t.Fatalf("sanitize kept %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("sanitize kept %v, want %v", got, want)
		}
	}

	// Everything filtered out collapses to nil so omitempty drops the field
	// rather than emitting an empty object.
	if got := sanitizeMCPXCodeMap(map[string]int{"nope": 1}); got != nil {
		t.Fatalf("all-invalid in, want nil out, got %v", got)
	}
	if got := sanitizeMCPXCodeMap(map[string]int{"MCPX_TOTALLY_MADE_UP": 4}); got != nil {
		t.Fatalf("uncataloged MCPX_-shaped key survived sanitize: %v", got)
	}
}

// TestSanitizeMCPXCodeMap_MatchesScannerPredicate pins the invariant that the
// producer-side filter is at least as strict as the wire-side gate. ScanForPII
// drops the ENTIRE heartbeat on one bad key, so any code sanitizeMCPXCodeMap
// admits must also be a code scanDiagCodeMap accepts — otherwise a single
// uncataloged standing code silently kills all telemetry for that install.
func TestSanitizeMCPXCodeMap_MatchesScannerPredicate(t *testing.T) {
	candidates := []string{
		"MCPX_HTTP_CONN_REFUSED",
		"MCPX_OAUTH_LOGIN_REQUIRED",
		"MCPX_TOTALLY_MADE_UP",
		"MCPX_",
		"MCPX_lower_case",
		"my-server",
		"https://internal.example.com/mcp",
		strings.Repeat("MCPX_A", 40),
	}
	for _, code := range candidates {
		kept := len(sanitizeMCPXCodeMap(map[string]int{code: 1})) == 1
		if !kept {
			continue
		}
		payload := []byte(`{"diagnostics":{"current_error_codes":{` +
			strconv.Quote(code) + `:1}}}`)
		if viol := scanDiagnosticsCounters(json.RawMessage(
			mustDiagObject(t, payload))); viol != nil {
			t.Fatalf("sanitize kept %q but the wire scanner rejects it (%s): the "+
				"whole heartbeat would be dropped", code, viol.Rule)
		}
	}
}

// mustDiagObject extracts the "diagnostics" sub-object from a payload literal.
func mustDiagObject(t *testing.T, payload []byte) json.RawMessage {
	t.Helper()
	var env struct {
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return env.Diagnostics
}

// TestPayloadV11_CurrentErrorCodesReachTheWire is the assembly test: with only
// the standing-state provider wired (no diagnostics counter store — the shape
// a parked install settles into once its 24h window has decayed), the
// heartbeat must still carry a diagnostics object, and it must pass the
// anonymity scanner.
func TestPayloadV11_CurrentErrorCodesReachTheWire(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	withoutBlockedValues(t)

	enabledTrue := true
	cfg := &config.Config{
		EnableSocket:      true,
		Features:          &config.FeatureFlags{EnableWebUI: true},
		QuarantineEnabled: &enabledTrue,
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
		},
	}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount: 3, connectedCount: 0, toolCount: 0,
		routingMode: "retrieve_tools", quarantine: true,
	})
	svc.SetCurrentErrorCodesProvider(func() map[string]int {
		return map[string]int{
			"MCPX_OAUTH_LOGIN_REQUIRED": 2,
			"MCPX_HTTP_DNS_FAILED":      1,
			// Producer-side leak: must be filtered before the wire.
			"my-internal-server": 4,
		}
	})

	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)

	for _, want := range []string{
		`"schema_version":11`,
		`"MCPX_OAUTH_LOGIN_REQUIRED":2`,
		`"MCPX_HTTP_DNS_FAILED":1`,
		`"current_error_codes"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("payload missing %s:\n%s", want, js)
		}
	}
	if strings.Contains(js, "my-internal-server") {
		t.Fatalf("a non-MCPX key reached the wire:\n%s", js)
	}
	if err := ScanForPII(data); err != nil {
		t.Fatalf("assembled v11 payload failed the anonymity scanner: %v", err)
	}
}

// TestPayloadV11_NoStandingFailuresOmitsDiagnostics keeps the negative case
// honest: an install with nothing wrong emits no diagnostics object at all,
// so a v11 payload from a healthy install stays shape-identical to a v10 one.
func TestPayloadV11_NoStandingFailuresOmitsDiagnostics(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	enabledTrue := true
	cfg := &config.Config{
		EnableSocket:      true,
		QuarantineEnabled: &enabledTrue,
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
		},
	}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{serverCount: 2, connectedCount: 2})
	svc.SetCurrentErrorCodesProvider(func() map[string]int { return nil })

	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"diagnostics"`) {
		t.Fatalf("healthy install emitted a diagnostics object:\n%s", data)
	}
}
