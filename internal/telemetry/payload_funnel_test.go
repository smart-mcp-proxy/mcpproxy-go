package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func newFunnelPayloadTestService(t *testing.T) *Service {
	t.Helper()
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
	return New(cfg, "", "v1.2.3", "personal", zap.NewNop())
}

// TestPayload_WizardShownIndependentOfEngagement asserts FR-005: the
// previously unobservable "shown but ignored" state is expressible —
// wizard_shown true while wizard_engaged stays absent (omitempty false).
func TestPayload_WizardShownIndependentOfEngagement(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	svc.SetOnboardingProvider(func() *OnboardingSnapshot {
		return &OnboardingSnapshot{
			WizardShown:   true,
			WizardEngaged: false,
		}
	})

	payload := svc.BuildPayload()
	if !payload.WizardShown {
		t.Fatalf("expected WizardShown=true")
	}
	if payload.WizardEngaged {
		t.Fatalf("expected WizardEngaged=false")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if !strings.Contains(js, `"wizard_shown":true`) {
		t.Errorf("expected wizard_shown:true on the wire, got:\n%s", js)
	}
	if strings.Contains(js, `"wizard_engaged"`) {
		t.Errorf("expected wizard_engaged absent for shown-but-ignored install, got:\n%s", js)
	}
}

// TestPayload_WizardShownAbsentWhenNeverRendered asserts the negative case:
// an install where the wizard never rendered serializes without wizard_shown.
func TestPayload_WizardShownAbsentWhenNeverRendered(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	svc.SetOnboardingProvider(func() *OnboardingSnapshot {
		return &OnboardingSnapshot{}
	})

	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"wizard_shown"`) {
		t.Errorf("expected wizard_shown omitted when never rendered, got:\n%s", string(data))
	}
}

// TestPayload_FunnelFieldsOmittedWithoutStore asserts FR-009 nil-safety:
// when no funnel store is wired (short-lived CLI commands), the new fields
// are absent — keeping the serialized shape v6-compatible.
func TestPayload_FunnelFieldsOmittedWithoutStore(t *testing.T) {
	svc := newFunnelPayloadTestService(t)

	payload := svc.BuildPayload()
	if payload.DaysSinceInstall != nil {
		t.Fatalf("expected DaysSinceInstall nil without store, got %v", *payload.DaysSinceInstall)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	for _, field := range []string{`"web_ui_opened"`, `"days_since_install"`, `"active_days_30d"`} {
		if strings.Contains(js, field) {
			t.Errorf("expected %s omitted when store not wired, got:\n%s", field, js)
		}
	}
}

// TestPayload_FunnelFieldsPopulated asserts FR-006/FR-007/FR-008 surfacing:
// a wired store populates web_ui_opened, days_since_install (present even at
// 0), and active_days_30d — and the serialized payload passes the anonymity
// scanner (no timestamps, no per-day structure on the wire).
func TestPayload_FunnelFieldsPopulated(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db := openFunnelTestDB(t)
	store := NewFunnelStore()
	if err := store.IncrementWebUIOpened(db); err != nil {
		t.Fatalf("IncrementWebUIOpened: %v", err)
	}
	if err := store.IncrementWebUIOpened(db); err != nil {
		t.Fatalf("IncrementWebUIOpened: %v", err)
	}
	svc.SetFunnelStore(store, db)

	payload := svc.BuildPayload()
	if payload.WebUIOpened != 2 {
		t.Fatalf("expected web_ui_opened=2, got %d", payload.WebUIOpened)
	}
	if payload.DaysSinceInstall == nil || *payload.DaysSinceInstall != 0 {
		t.Fatalf("expected days_since_install=0 on install day, got %v", payload.DaysSinceInstall)
	}
	// BuildPayload records the current day as active before snapshotting.
	if payload.ActiveDays30d != 1 {
		t.Fatalf("expected active_days_30d=1 on first run, got %d", payload.ActiveDays30d)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if !strings.Contains(js, `"web_ui_opened":2`) {
		t.Errorf("expected web_ui_opened:2 on the wire, got:\n%s", js)
	}
	// FR-007: day-0 must be transmitted, not omitted — otherwise install day
	// is indistinguishable from "store not wired".
	if !strings.Contains(js, `"days_since_install":0`) {
		t.Errorf("expected days_since_install:0 on the wire, got:\n%s", js)
	}
	if !strings.Contains(js, `"active_days_30d":1`) {
		t.Errorf("expected active_days_30d:1 on the wire, got:\n%s", js)
	}
	// FR-008: the per-day structure never leaves the machine.
	if strings.Contains(js, `"active_days"`) || strings.Contains(js, "first_install") {
		t.Errorf("per-day structure or install stamp leaked to the wire:\n%s", js)
	}
	if scanErr := ScanForPII(data); scanErr != nil {
		t.Errorf("anonymity scan failed on funnel payload: %v", scanErr)
	}
}

// TestPayload_FunnelDaysSinceInstallAges asserts the whole-day count grows
// with a pre-seeded install stamp (boundary math via the store, surfaced by
// the payload builder).
func TestPayload_FunnelDaysSinceInstallAges(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db := openFunnelTestDB(t)
	store := NewFunnelStore()
	// Seed the install stamp 5 days in the past.
	if err := store.RecordActivity(db, time.Now().UTC().AddDate(0, 0, -5)); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	svc.SetFunnelStore(store, db)

	payload := svc.BuildPayload()
	if payload.DaysSinceInstall == nil || *payload.DaysSinceInstall != 5 {
		t.Fatalf("expected days_since_install=5, got %v", payload.DaysSinceInstall)
	}
	if payload.ActiveDays30d != 2 {
		t.Fatalf("expected active_days_30d=2 (seeded day + today), got %d", payload.ActiveDays30d)
	}
}

// TestService_RecordWebUIOpenNilSafe asserts the index-serve hook is a no-op
// when the store is not wired (FR-009 nil-safety) and increments when it is.
func TestService_RecordWebUIOpenNilSafe(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	// Must not panic without a store.
	svc.RecordWebUIOpen()

	db := openFunnelTestDB(t)
	store := NewFunnelStore()
	svc.SetFunnelStore(store, db)
	svc.RecordWebUIOpen()
	svc.RecordWebUIOpen()

	st, err := store.Snapshot(db, time.Now().UTC())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if st.WebUIOpened != 2 {
		t.Fatalf("expected web_ui_opened=2 after two hooks, got %d", st.WebUIOpened)
	}
}
