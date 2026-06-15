package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// clearTelemetryEnv neutralizes env vars that would otherwise force telemetry
// off (GitHub Actions sets CI=true) so the resolved-state logic exercises the
// config value, not the env override.
func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
}

func TestTelemetryDisableTransition(t *testing.T) {
	clearTelemetryEnv(t)

	cfg := func(enabled *bool) *config.Config {
		return &config.Config{Telemetry: &config.TelemetryConfig{Enabled: enabled}}
	}
	nilTelemetry := &config.Config{} // Telemetry nil => resolved enabled (opt-out default)

	cases := []struct {
		name  string
		prior *config.Config
		next  *config.Config
		want  bool
	}{
		{"enabled_to_disabled", cfg(boolPtr(true)), cfg(boolPtr(false)), true},
		{"nil_to_disabled", nilTelemetry, cfg(boolPtr(false)), true},
		{"enabledNilPtr_to_disabled", cfg(nil), cfg(boolPtr(false)), true},
		{"disabled_to_disabled", cfg(boolPtr(false)), cfg(boolPtr(false)), false},
		{"enabled_to_enabled", cfg(boolPtr(true)), cfg(boolPtr(true)), false},
		{"disabled_to_enabled", cfg(boolPtr(false)), cfg(boolPtr(true)), false},
		{"nil_to_nil", nilTelemetry, &config.Config{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TelemetryDisableTransition(tc.prior, tc.next); got != tc.want {
				t.Fatalf("TelemetryDisableTransition=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestSendOptOutBeacon_PayloadShape asserts the beacon hits the existing
// /heartbeat ingest path and carries ONLY the event marker + anonymous ID — no
// usage payload whatsoever.
func TestSendOptOutBeacon_PayloadShape(t *testing.T) {
	type capture struct {
		path   string
		method string
		body   map[string]any
	}
	clearTelemetryEnv(t)
	done := make(chan capture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		done <- capture{path: r.URL.Path, method: r.Method, body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{Telemetry: &config.TelemetryConfig{
		AnonymousID: "anon-123", Endpoint: server.URL,
	}}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	if err := svc.SendOptOutBeacon(context.Background()); err != nil {
		t.Fatalf("SendOptOutBeacon returned error: %v", err)
	}

	select {
	case c := <-done:
		if c.path != "/heartbeat" {
			t.Errorf("beacon path = %q, want /heartbeat (reuse existing ingest)", c.path)
		}
		if c.method != http.MethodPost {
			t.Errorf("beacon method = %q, want POST", c.method)
		}
		if c.body["event"] != OptOutEvent {
			t.Errorf("event = %v, want %q", c.body["event"], OptOutEvent)
		}
		if c.body["anonymous_id"] != "anon-123" {
			t.Errorf("anonymous_id = %v, want anon-123", c.body["anonymous_id"])
		}
		// Strict: NO usage fields. The whole point of the opt-out beacon is that
		// it carries nothing but the dedup ID.
		usageKeys := []string{
			"server_count", "connected_server_count", "tool_count", "version",
			"uptime_hours", "surface_requests", "builtin_tool_calls",
			"rest_endpoint_calls", "feature_flags", "os", "arch", "routing_mode",
		}
		for _, k := range usageKeys {
			if _, ok := c.body[k]; ok {
				t.Errorf("opt-out beacon must not carry usage field %q (got %v)", k, c.body[k])
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for opt-out beacon")
	}
}

func TestValidateTelemetryURL(t *testing.T) {
	ok := []string{"https://telemetry.mcpproxy.app/v1/heartbeat", "http://127.0.0.1:8080/heartbeat"}
	for _, u := range ok {
		if _, err := validateTelemetryURL(u); err != nil {
			t.Errorf("validateTelemetryURL(%q) unexpected error: %v", u, err)
		}
	}
	bad := []string{"file:///etc/passwd", "gopher://x/heartbeat", "/heartbeat", "telemetry.mcpproxy.app/heartbeat"}
	for _, u := range bad {
		if _, err := validateTelemetryURL(u); err == nil {
			t.Errorf("validateTelemetryURL(%q) expected error, got nil", u)
		}
	}
}

// TestNotifyConfigChanged_FiresExactlyOnceOnDisable verifies the server-side
// transition detection: an enabled->disabled config swap emits exactly one
// opt-out beacon carrying the anonymous ID.
func TestNotifyConfigChanged_FiresExactlyOnceOnDisable(t *testing.T) {
	clearTelemetryEnv(t)

	received := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	enabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(true), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}
	disabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(false), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}

	svc := New(enabled, "", "v1.2.3", "personal", zap.NewNop())
	svc.NotifyConfigChanged(disabled)

	select {
	case body := <-received:
		if body["event"] != OptOutEvent {
			t.Errorf("event = %v, want %q", body["event"], OptOutEvent)
		}
		if body["anonymous_id"] != "anon-xyz" {
			t.Errorf("anonymous_id = %v, want anon-xyz", body["anonymous_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected one opt-out beacon, got none")
	}

	// Exactly one: nothing else within a short window.
	select {
	case extra := <-received:
		t.Fatalf("expected exactly one beacon, got a second: %v", extra)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestNotifyConfigChanged_NoBeaconWhenAlreadyDisabled(t *testing.T) {
	clearTelemetryEnv(t)

	received := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	disabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(false), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}
	// Service constructed already-disabled; a reload that keeps it disabled must
	// emit nothing.
	svc := New(disabled, "", "v1.2.3", "personal", zap.NewNop())
	svc.NotifyConfigChanged(disabled)

	select {
	case <-received:
		t.Fatal("no beacon expected for disabled->disabled reload")
	case <-time.After(300 * time.Millisecond):
	}
}

// TestNotifyConfigChanged_SendFailureStillDisables proves the opt-out is
// best-effort: a failing endpoint must NOT prevent telemetry from stopping.
func TestNotifyConfigChanged_SendFailureStillDisables(t *testing.T) {
	clearTelemetryEnv(t)

	// Point at a closed server so the beacon send fails fast.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	enabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(true), AnonymousID: "anon-xyz", Endpoint: deadURL,
	}}
	disabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(false), AnonymousID: "anon-xyz", Endpoint: deadURL,
	}}

	svc := New(enabled, "", "v1.2.3", "personal", zap.NewNop())
	svc.NotifyConfigChanged(disabled)

	// Telemetry must be marked opted-out regardless of the send outcome.
	deadline := time.After(2 * time.Second)
	for !svc.optedOut.Load() {
		select {
		case <-deadline:
			t.Fatal("telemetry was not disabled after a failed opt-out beacon")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestOptOut_StopsFurtherHeartbeats verifies that once opted out, sendHeartbeat
// emits nothing — respecting the user's decision.
func TestOptOut_StopsFurtherHeartbeats(t *testing.T) {
	clearTelemetryEnv(t)

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	enabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(true), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}
	disabled := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(false), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}

	svc := New(enabled, "", "v1.2.3", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{serverCount: 1})
	svc.NotifyConfigChanged(disabled)

	// Wait for the single opt-out beacon to land.
	time.Sleep(200 * time.Millisecond)
	hitsAfterBeacon := hits.Load()

	// Any further heartbeat attempts must be suppressed.
	svc.sendHeartbeat(context.Background())
	if got := hits.Load(); got != hitsAfterBeacon {
		t.Fatalf("heartbeat emitted after opt-out: hits went %d -> %d", hitsAfterBeacon, got)
	}
}
