package telemetry

import (
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "00000000-0000-0000-0000-000000000001",
		},
	}
	return New(cfg, "", "v1.2.3", "personal", zap.NewNop())
}

func TestIDRotatesAfter365Days(t *testing.T) {
	svc := newTestService(t)
	svc.config.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Add(-400 * 24 * time.Hour).Format(time.RFC3339)
	originalID := svc.config.Telemetry.AnonymousID

	svc.maybeRotateAnonymousID(time.Now().UTC())

	if svc.config.Telemetry.AnonymousID == originalID {
		t.Error("expected anonymous ID to rotate after 400 days")
	}
	parsed, err := time.Parse(time.RFC3339, svc.config.Telemetry.AnonymousIDCreatedAt)
	if err != nil {
		t.Fatalf("created_at not RFC3339: %v", err)
	}
	if time.Since(parsed) > time.Minute {
		t.Errorf("created_at not refreshed: %v", parsed)
	}
}

func TestIDDoesNotRotateBefore365Days(t *testing.T) {
	svc := newTestService(t)
	svc.config.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	originalID := svc.config.Telemetry.AnonymousID

	svc.maybeRotateAnonymousID(time.Now().UTC())

	if svc.config.Telemetry.AnonymousID != originalID {
		t.Error("expected anonymous ID to remain unchanged before 365 days")
	}
}

func TestLegacyInstallInitializesCreatedAtWithoutRotating(t *testing.T) {
	svc := newTestService(t)
	svc.config.Telemetry.AnonymousIDCreatedAt = "" // Legacy install: no created_at
	originalID := svc.config.Telemetry.AnonymousID

	svc.maybeRotateAnonymousID(time.Now().UTC())

	if svc.config.Telemetry.AnonymousID != originalID {
		t.Error("legacy install must NOT rotate the ID, only initialize created_at")
	}
	if svc.config.Telemetry.AnonymousIDCreatedAt == "" {
		t.Error("created_at must be initialized for legacy install")
	}
}

func TestClockSkewFutureCreatedAtDoesNotRotate(t *testing.T) {
	svc := newTestService(t)
	// Pretend the system clock rolled backward: created_at is in the future.
	svc.config.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	originalID := svc.config.Telemetry.AnonymousID
	originalCreated := svc.config.Telemetry.AnonymousIDCreatedAt

	svc.maybeRotateAnonymousID(time.Now().UTC())

	if svc.config.Telemetry.AnonymousID != originalID {
		t.Error("future created_at must not trigger rotation")
	}
	if svc.config.Telemetry.AnonymousIDCreatedAt != originalCreated {
		t.Error("future created_at must not be modified")
	}
}

func TestCorruptCreatedAtIsResetWithoutRotating(t *testing.T) {
	svc := newTestService(t)
	svc.config.Telemetry.AnonymousIDCreatedAt = "not-a-real-timestamp"
	originalID := svc.config.Telemetry.AnonymousID

	svc.maybeRotateAnonymousID(time.Now().UTC())

	if svc.config.Telemetry.AnonymousID != originalID {
		t.Error("corrupt created_at must not trigger rotation")
	}
	if _, err := time.Parse(time.RFC3339, svc.config.Telemetry.AnonymousIDCreatedAt); err != nil {
		t.Errorf("created_at should be reset to a valid timestamp, got %q", svc.config.Telemetry.AnonymousIDCreatedAt)
	}
}
