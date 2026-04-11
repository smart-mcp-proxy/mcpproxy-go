package telemetry

import (
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestPayloadHasNoForbiddenSubstrings is the canonical privacy regression
// test. It builds a fully populated heartbeat payload (all counters set, all
// flags set, every error category present, doctor results recorded) and
// asserts that the rendered JSON does not contain any string from a list of
// forbidden substrings.
//
// If this test ever fails, the privacy contract of Spec 042 has been broken
// and the change MUST be reverted before merging.
func TestPayloadHasNoForbiddenSubstrings(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	enabledTrue := true
	cfg := &config.Config{
		EnableSocket:           true,
		EnablePrompts:          true,
		Features:               &config.FeatureFlags{EnableWebUI: true},
		RequireMCPAuth:         true,
		EnableCodeExecution:    true,
		QuarantineEnabled:      &enabledTrue,
		SensitiveDataDetection: &config.SensitiveDataDetectionConfig{Enabled: true},
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
			LastReportedVersion:  "v1.0.0",
			LastStartupOutcome:   "success",
			NoticeShown:          true,
		},
		Servers: []*config.ServerConfig{
			// Canary server with deliberately distinctive name and URL.
			// If anything in the payload contains "MY-CANARY-SERVER" or the
			// host string, the privacy contract is broken.
			{
				Name:  "MY-CANARY-SERVER",
				URL:   "https://internal-corp-secrets.example.com/oauth/authorize",
				OAuth: &config.OAuthConfig{ClientID: "SUPER-SECRET-CLIENT-ID-9876"},
			},
			{
				Name:  "another-server",
				URL:   "/Users/alice/private-token-store",
				OAuth: &config.OAuthConfig{ClientID: "another-secret"},
			},
		},
	}

	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount:    99,
		connectedCount: 50,
		toolCount:      1000,
		routingMode:    "dynamic",
		quarantine:     true,
	})

	// Exercise every counter so the payload is fully populated.
	for _, s := range []Surface{SurfaceMCP, SurfaceCLI, SurfaceWebUI, SurfaceTray, SurfaceUnknown} {
		svc.Registry().RecordSurface(s)
	}
	for _, name := range []string{
		"retrieve_tools", "call_tool_read", "call_tool_write", "call_tool_destructive",
		"upstream_servers", "quarantine_security", "code_execution",
	} {
		svc.Registry().RecordBuiltinTool(name)
	}
	// Try to leak the canary upstream tool name — must be silently dropped.
	svc.Registry().RecordBuiltinTool("MY-CANARY-SERVER:exfiltrate_secrets")
	for i := 0; i < 42; i++ {
		svc.Registry().RecordUpstreamTool()
	}
	svc.Registry().RecordRESTRequest("GET", "/api/v1/servers", "2xx")
	svc.Registry().RecordRESTRequest("POST", "/api/v1/servers/{name}/enable", "2xx")
	svc.Registry().RecordRESTRequest("GET", "/api/v1/status", "5xx")
	svc.Registry().RecordRESTRequest("GET", "UNMATCHED", "4xx")
	for cat := range validErrorCategories {
		svc.Registry().RecordError(cat)
	}
	svc.Registry().RecordDoctorRun([]DoctorCheckResult{
		fakeDoctorResult{name: "db_writable", pass: true},
		fakeDoctorResult{name: "config_valid", pass: false},
		fakeDoctorResult{name: "port_available", pass: true},
	})

	payload := svc.BuildPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	js := string(data)

	// Forbidden substrings — if any of these appear, telemetry has leaked
	// information that the privacy contract forbids.
	forbidden := []string{
		// Canary names from the test fixture.
		"MY-CANARY-SERVER",
		"my-canary-server",
		"another-server",
		"exfiltrate_secrets",
		"SUPER-SECRET-CLIENT-ID-9876",
		"another-secret",
		"internal-corp-secrets.example.com",

		// File paths.
		"/Users/",
		"/home/",
		`C:\\`,

		// Network identifiers.
		"localhost",
		"127.0.0.1",
		"192.168.",
		"10.0.0.",

		// Auth secrets.
		"Bearer ",
		"apikey=",
		"password=",
		"client_secret",

		// Free-text error messages.
		"error: ",
		"failed: ",
	}

	for _, forbidden := range forbidden {
		if strings.Contains(js, forbidden) {
			t.Errorf("PRIVACY VIOLATION: payload contains forbidden substring %q\nfull payload:\n%s",
				forbidden, js)
		}
	}

	// Sanity check: the payload should still contain the legitimate fields,
	// otherwise we've over-redacted.
	for _, required := range []string{
		`"schema_version":2`,
		`"surface_requests"`,
		`"builtin_tool_calls"`,
		`"upstream_tool_call_count_bucket"`,
		`"rest_endpoint_calls"`,
		`"feature_flags"`,
		`"error_category_counts"`,
		`"doctor_checks"`,
	} {
		if !strings.Contains(js, required) {
			t.Errorf("expected payload to contain %q, missing from:\n%s", required, js)
		}
	}

	// Payload size sanity (Spec 042 SC-006: under 8 KB).
	if len(data) > 8*1024 {
		t.Errorf("payload size %d bytes exceeds 8 KB privacy budget", len(data))
	}
}
