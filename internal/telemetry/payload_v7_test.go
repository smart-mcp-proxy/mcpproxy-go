package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSchemaVersionIsV7 pins FR-014: the Spec 080 payload contract is v7.
func TestSchemaVersionIsV7(t *testing.T) {
	if SchemaVersion != 7 {
		t.Fatalf("SchemaVersion = %d, want 7 (Spec 080 FR-014)", SchemaVersion)
	}
}

// TestPayloadV7_FullyPopulatedPassesScanner is the SC-007 contract test: a
// payload exercising every v7 addition (wizard_shown, completed_external,
// web_ui_opened, days_since_install, active_days_30d, previous_shutdown,
// last_error_code) round-trips through the anonymity scanner with zero
// violations, and every new field reaches the wire with its documented
// fixed-enum / non-negative-integer / boolean shape.
func TestPayloadV7_FullyPopulatedPassesScanner(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db := openFunnelTestDB(t)

	// US2 funnel store: install stamp 3 days back, 3 index serves.
	funnel := NewFunnelStore()
	if err := funnel.RecordActivity(db, time.Now().UTC().AddDate(0, 0, -3)); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := funnel.IncrementWebUIOpened(db); err != nil {
			t.Fatalf("IncrementWebUIOpened: %v", err)
		}
	}
	svc.SetFunnelStore(funnel, db)

	// US3 pre-churn snapshot: prior instance crashed, one MCPX_* code recorded.
	prechurn := NewPreChurnStore()
	if err := prechurn.RecordLastErrorCode(db, "MCPX_DOCKER_CLI_NOT_FOUND"); err != nil {
		t.Fatalf("RecordLastErrorCode: %v", err)
	}
	svc.SetPreChurn(PreviousShutdownCrash, prechurn, db)

	// US1 widened enum: connected outside the wizard.
	svc.SetOnboardingProvider(func() *OnboardingSnapshot {
		return &OnboardingSnapshot{
			ConnectedClientCount: 1,
			ConnectedClientIDs:   []string{"claude-code"},
			WizardEngaged:        true,
			WizardShown:          true,
			WizardConnectStep:    "completed_external",
			WizardServerStep:     "skipped",
		}
	})

	payload := svc.BuildPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)

	// Hermetic scan: no stale runtime blocked values.
	prev := BlockedValues
	BlockedValues = nil
	defer func() { BlockedValues = prev }()

	if scanErr := ScanForPII(data); scanErr != nil {
		t.Fatalf("fully-populated v7 payload must pass ScanForPII (SC-007), got: %v\npayload:\n%s", scanErr, js)
	}

	for _, required := range []string{
		`"schema_version":7`,
		`"wizard_shown":true`,
		`"wizard_connect_step":"completed_external"`,
		`"web_ui_opened":3`,
		`"days_since_install":3`,
		`"active_days_30d":2`,
		`"previous_shutdown":"crash"`,
		`"last_error_code":"MCPX_DOCKER_CLI_NOT_FOUND"`,
	} {
		if !strings.Contains(js, required) {
			t.Errorf("expected v7 payload to contain %s, missing from:\n%s", required, js)
		}
	}
}

// TestPayloadV7_ZeroNewFieldsShapeCompatibleWithV6 is the US4 negative case
// (SC-008): with no funnel/pre-churn store wired and a never-shown wizard,
// every v7 addition is omitted (omitempty discipline) — the serialized shape
// is v6-compatible except for schema_version itself.
func TestPayloadV7_ZeroNewFieldsShapeCompatibleWithV6(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	svc.SetOnboardingProvider(func() *OnboardingSnapshot {
		return &OnboardingSnapshot{}
	})

	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)

	if !strings.Contains(js, `"schema_version":7`) {
		t.Errorf("expected schema_version:7 even on a zero-valued payload, got:\n%s", js)
	}
	for _, forbidden := range []string{
		`"wizard_shown"`,
		`"web_ui_opened"`,
		`"days_since_install"`,
		`"active_days_30d"`,
		`"previous_shutdown"`,
		`"last_error_code"`,
		`"completed_external"`,
	} {
		if strings.Contains(js, forbidden) {
			t.Errorf("v7 field %s must be omitted on a zero-valued payload (v6 shape compat), got:\n%s", forbidden, js)
		}
	}
}
