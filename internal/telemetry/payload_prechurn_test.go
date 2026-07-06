package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPayload_PreviousShutdownStableWithinInstance asserts FR-011: the value
// is computed once at startup and stays stable across every heartbeat of the
// instance, even though the on-disk marker is re-armed/mutated meanwhile.
func TestPayload_PreviousShutdownStableWithinInstance(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	// Startup: prior instance crashed; runtime derives "crash" once and re-arms.
	svc.SetPreChurn(PreviousShutdownCrash, store, db)

	first := svc.BuildPayload()
	if first.PreviousShutdown != PreviousShutdownCrash {
		t.Fatalf("expected previous_shutdown %q, got %q", PreviousShutdownCrash, first.PreviousShutdown)
	}

	// Mutate the on-disk marker between heartbeats (resolve + re-arm): the
	// surfaced value must not move.
	if err := store.ResolveCleanShutdown(db); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := store.ArmShutdownMarker(db); err != nil {
		t.Fatalf("re-arm: %v", err)
	}

	second := svc.BuildPayload()
	if second.PreviousShutdown != PreviousShutdownCrash {
		t.Fatalf("previous_shutdown drifted within instance: %q", second.PreviousShutdown)
	}

	data, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"previous_shutdown":"crash"`) {
		t.Errorf("expected previous_shutdown:crash on the wire, got:\n%s", string(data))
	}
}

// TestPayload_PreChurnOmittedOnFirstRun asserts the FR-010/FR-013 unknown
// case and FR-012 absence: a first-ever run (no prior marker, no error ever
// recorded) serializes without previous_shutdown or last_error_code — and is
// never misreported as a crash.
func TestPayload_PreChurnOmittedOnFirstRun(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()

	prev, err := store.ArmShutdownMarker(db)
	if err != nil {
		t.Fatalf("arm: %v", err)
	}
	svc.SetPreChurn(prev, store, db)

	payload := svc.BuildPayload()
	if payload.PreviousShutdown != "" {
		t.Fatalf("first run must not report a shutdown outcome, got %q", payload.PreviousShutdown)
	}
	if payload.LastErrorCode != "" {
		t.Fatalf("expected no last_error_code on first run, got %q", payload.LastErrorCode)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if strings.Contains(js, `"previous_shutdown"`) {
		t.Errorf("expected previous_shutdown omitted on first run, got:\n%s", js)
	}
	if strings.Contains(js, `"last_error_code"`) {
		t.Errorf("expected last_error_code omitted when never recorded, got:\n%s", js)
	}
}

// TestPayload_PreChurnOmittedWhenStoreNotWired: short-lived CLI commands never
// call SetPreChurn — both fields stay absent (same nil-safety as Activation).
func TestPayload_PreChurnOmittedWhenStoreNotWired(t *testing.T) {
	svc := newFunnelPayloadTestService(t)

	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if strings.Contains(js, `"previous_shutdown"`) || strings.Contains(js, `"last_error_code"`) {
		t.Errorf("expected pre-churn fields omitted when store not wired, got:\n%s", js)
	}
}

// TestPayload_LastErrorCodeMostRecentPerHeartbeat asserts FR-012: the field
// tracks the most recently observed code, re-read at each heartbeat build.
func TestPayload_LastErrorCodeMostRecentPerHeartbeat(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()
	svc.SetPreChurn(PreviousShutdownClean, store, db)

	if err := store.RecordLastErrorCode(db, "MCPX_DOCKER_PULL_FAILED"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := svc.BuildPayload().LastErrorCode; got != "MCPX_DOCKER_PULL_FAILED" {
		t.Fatalf("expected first code, got %q", got)
	}

	if err := store.RecordLastErrorCode(db, "MCPX_OAUTH_REFRESH_FAILED"); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if got := svc.BuildPayload().LastErrorCode; got != "MCPX_OAUTH_REFRESH_FAILED" {
		t.Fatalf("expected most recent code on next heartbeat, got %q", got)
	}
}

// TestPayload_PreChurnPassesAnonymityScan: a payload with both pre-churn
// fields populated serializes with enum-only values and zero anonymity
// violations (FR-012/FR-016 posture for this slice).
func TestPayload_PreChurnPassesAnonymityScan(t *testing.T) {
	svc := newFunnelPayloadTestService(t)
	db, _ := openPreChurnTestDB(t)
	store := NewPreChurnStore()
	svc.SetPreChurn(PreviousShutdownCrash, store, db)

	if err := store.RecordLastErrorCode(db, "MCPX_UPSTREAM_CONNECT_REFUSED"); err != nil {
		t.Fatalf("record: %v", err)
	}

	payload := svc.BuildPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if !strings.Contains(js, `"previous_shutdown":"crash"`) {
		t.Errorf("expected previous_shutdown on the wire, got:\n%s", js)
	}
	if !strings.Contains(js, `"last_error_code":"MCPX_UPSTREAM_CONNECT_REFUSED"`) {
		t.Errorf("expected last_error_code on the wire, got:\n%s", js)
	}
	if err := ScanForPII(data); err != nil {
		t.Errorf("anonymity scan failed on pre-churn payload: %v", err)
	}
}
