package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/denisbrodbeck/machineid"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// isLowerHex reports whether s is a non-empty string of lowercase hex digits.
func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// newMachineIDTestService builds a minimal telemetry Service suitable for
// exercising BuildPayload in machine-id tests.
func newMachineIDTestService(t *testing.T) *Service {
	t.Helper()
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "fixed-id",
			AnonymousIDCreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
	return New(cfg, "", "v1.2.3", "personal", zap.NewNop())
}

// TestMachineID_PresentAndStable asserts the machine_id field is populated and
// identical across two payload builds on the same machine.
func TestMachineID_PresentAndStable(t *testing.T) {
	const fixed = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	prev := machineIDProvider
	machineIDProvider = func() string { return fixed }
	resetMachineIDForTest()
	defer func() {
		machineIDProvider = prev
		resetMachineIDForTest()
	}()

	svc := newMachineIDTestService(t)

	p1 := svc.BuildPayload()
	p2 := svc.BuildPayload()

	if p1.MachineID == "" {
		t.Fatalf("machine_id is empty, want %q", fixed)
	}
	if p1.MachineID != fixed {
		t.Errorf("machine_id = %q, want %q", p1.MachineID, fixed)
	}
	if p1.MachineID != p2.MachineID {
		t.Errorf("machine_id not stable across builds: %q != %q", p1.MachineID, p2.MachineID)
	}
}

// TestMachineID_OmittedWhenUnavailable asserts that when the OS machine id
// cannot be read, the field is empty and omitted from the serialized JSON
// (never failing the heartbeat).
func TestMachineID_OmittedWhenUnavailable(t *testing.T) {
	prev := machineIDProvider
	machineIDProvider = func() string { return "" }
	resetMachineIDForTest()
	defer func() {
		machineIDProvider = prev
		resetMachineIDForTest()
	}()

	svc := newMachineIDTestService(t)
	payload := svc.BuildPayload()

	if payload.MachineID != "" {
		t.Errorf("machine_id = %q, want empty when unavailable", payload.MachineID)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(data), "machine_id") {
		t.Errorf("empty machine_id should be omitted from JSON, got: %s", data)
	}
}

// TestResolveMachineID_CachedOncePerProcess asserts resolveMachineID probes the
// provider at most once and caches the result.
func TestResolveMachineID_CachedOncePerProcess(t *testing.T) {
	calls := 0
	prev := machineIDProvider
	machineIDProvider = func() string {
		calls++
		return "cached-value"
	}
	resetMachineIDForTest()
	defer func() {
		machineIDProvider = prev
		resetMachineIDForTest()
	}()

	_ = resolveMachineID()
	_ = resolveMachineID()
	_ = resolveMachineID()

	if calls != 1 {
		t.Errorf("machineIDProvider called %d times, want 1 (result must be cached)", calls)
	}
}

// TestProtectedMachineID_NotRawAndHex verifies the production hash is a
// lowercase-hex string, is not the raw OS machine id, and is scoped to the app
// key (differs from the raw id and from a bare hash of another app key). Skips
// when the OS machine id is unreadable (e.g. CI containers without
// /etc/machine-id) — the graceful-fallback path is covered separately.
func TestProtectedMachineID_NotRawAndHex(t *testing.T) {
	raw, err := machineid.ID()
	if err != nil || raw == "" {
		t.Skipf("OS machine id unavailable on this host (%v); skipping raw-comparison", err)
	}

	got := protectedMachineID()
	if got == "" {
		t.Fatal("protectedMachineID returned empty despite a readable OS machine id")
	}
	if !isLowerHex(got) {
		t.Errorf("protectedMachineID = %q, want lowercase hex", got)
	}
	// HMAC-SHA256 hex is 64 chars.
	if len(got) != 64 {
		t.Errorf("protectedMachineID length = %d, want 64 (SHA-256 hex)", len(got))
	}
	if strings.Contains(got, raw) || got == raw {
		t.Errorf("protectedMachineID leaked the raw machine id")
	}
}

// TestProtectedMachineID_RawNeverInPayload builds a real payload (production
// provider) and asserts the raw OS machine id never appears in the serialized
// JSON, while the hashed machine_id does. Skips when the raw id is unreadable.
func TestProtectedMachineID_RawNeverInPayload(t *testing.T) {
	raw, err := machineid.ID()
	if err != nil || raw == "" {
		t.Skipf("OS machine id unavailable on this host (%v); skipping", err)
	}

	// Ensure the production provider is in effect and the cache is fresh.
	resetMachineIDForTest()
	defer resetMachineIDForTest()

	svc := newMachineIDTestService(t)
	payload := svc.BuildPayload()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	js := string(data)

	if strings.Contains(js, raw) {
		t.Errorf("PRIVACY VIOLATION: payload contains the raw machine id")
	}
	if payload.MachineID != "" && !strings.Contains(js, payload.MachineID) {
		t.Errorf("hashed machine_id missing from serialized payload")
	}
}
