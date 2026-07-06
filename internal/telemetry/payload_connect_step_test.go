package telemetry

import (
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestPayload_WizardConnectStepCompletedExternal asserts the Spec 080 US1
// widened enum surfaces through the heartbeat unchanged (FR-003): a persisted
// "completed_external" connect-step status reaches the wire as a distinct
// wizard_connect_step value.
func TestPayload_WizardConnectStepCompletedExternal(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

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
	svc.SetOnboardingProvider(func() *OnboardingSnapshot {
		return &OnboardingSnapshot{
			ConnectedClientCount: 1,
			ConnectedClientIDs:   []string{"claude-code"},
			WizardEngaged:        true,
			WizardConnectStep:    "completed_external",
			WizardServerStep:     "skipped",
		}
	})

	payload := svc.BuildPayload()
	if payload.WizardConnectStep != "completed_external" {
		t.Fatalf("expected WizardConnectStep=completed_external, got %q", payload.WizardConnectStep)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if !strings.Contains(js, `"wizard_connect_step":"completed_external"`) {
		t.Errorf("expected serialized payload to carry the widened enum value, got:\n%s", js)
	}

	// The widened value is still a fixed-enum token: no user strings, paths,
	// or URLs ride along with it (privacy posture preserved).
	if scanErr := ScanForPII(data); scanErr != nil {
		t.Errorf("anonymity scan failed on completed_external payload: %v", scanErr)
	}
}
