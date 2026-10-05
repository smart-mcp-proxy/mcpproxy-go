package telemetry

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// effectiveStateCase mirrors one row of testdata/effective_state_cases.json.
// The same file drives the vitest and XCTest suites so the notice copy and the
// source/disabled_by vocabulary cannot drift between surfaces (Spec 109
// FR-044a).
type effectiveStateCase struct {
	Name          string            `json:"name"`
	Env           map[string]string `json:"env"`
	ConfigEnabled *bool             `json:"config_enabled"`
	Want          struct {
		Enabled    bool   `json:"enabled"`
		Source     string `json:"source"`
		DisabledBy string `json:"disabled_by"`
	} `json:"want"`
	Notice      string  `json:"notice"`
	OffLine     *string `json:"off_line"`
	SettingLock *string `json:"setting_lock"`
}

func loadEffectiveStateCases(t *testing.T) []effectiveStateCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/effective_state_cases.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc struct {
		Cases []effectiveStateCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	return doc.Cases
}

// setCaseEnv pins all three opt-out variables (empty strings when unset: CI on
// GitHub Actions sets CI=true, which would otherwise leak into every case).
func setCaseEnv(t *testing.T, env map[string]string) {
	t.Helper()
	clearTelemetryEnv(t)
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func cfgFor(enabled *bool) *config.Config {
	return &config.Config{Telemetry: &config.TelemetryConfig{Enabled: enabled}}
}

func TestResolveEffectiveState_Cases(t *testing.T) {
	for _, tc := range loadEffectiveStateCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			setCaseEnv(t, tc.Env)
			got := ResolveEffectiveState(cfgFor(tc.ConfigEnabled))
			if got.Enabled != tc.Want.Enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tc.Want.Enabled)
			}
			if string(got.Source) != tc.Want.Source {
				t.Errorf("Source = %q, want %q", got.Source, tc.Want.Source)
			}
			if string(got.DisabledBy) != tc.Want.DisabledBy {
				t.Errorf("DisabledBy = %q, want %q", got.DisabledBy, tc.Want.DisabledBy)
			}
		})
	}
}

func TestResolveEffectiveState_AgreesWithEffectiveTelemetryEnabled(t *testing.T) {
	for _, tc := range loadEffectiveStateCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			setCaseEnv(t, tc.Env)
			cfg := cfgFor(tc.ConfigEnabled)
			if got, want := ResolveEffectiveState(cfg).Enabled, EffectiveTelemetryEnabled(cfg); got != want {
				t.Errorf("ResolveEffectiveState.Enabled = %v but EffectiveTelemetryEnabled = %v", got, want)
			}
		})
	}
}

func TestResolveEffectiveState_NilConfig(t *testing.T) {
	clearTelemetryEnv(t)
	got := ResolveEffectiveState(nil)
	if got.Enabled || got.Source != StateSourceDefault || got.DisabledBy != EnvDisabledNone {
		t.Fatalf("nil config = %+v, want {false default}", got)
	}
	if EffectiveTelemetryEnabled(nil) {
		t.Fatal("EffectiveTelemetryEnabled(nil) must stay false")
	}
}

func TestResolveEffectiveState_NoTelemetryBlock(t *testing.T) {
	clearTelemetryEnv(t)
	got := ResolveEffectiveState(&config.Config{})
	if !got.Enabled || got.Source != StateSourceDefault {
		t.Fatalf("absent telemetry block = %+v, want {true default}", got)
	}
}

func TestEffectiveState_JSONShape(t *testing.T) {
	b, err := json.Marshal(EffectiveState{Enabled: false, Source: StateSourceConfig})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"enabled":false,"source":"config"}` {
		t.Fatalf("json = %s (disabled_by must be omitted when empty)", b)
	}
}
