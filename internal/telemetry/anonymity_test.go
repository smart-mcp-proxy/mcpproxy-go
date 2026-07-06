package telemetry

import (
	"errors"
	"strings"
	"testing"

	"github.com/denisbrodbeck/machineid"
)

// TestScanForPII_BlockedPrefix_UsersPath asserts that a payload containing a
// /Users/... path fails scanning and the violation identifies the matching
// prefix.
func TestScanForPII_BlockedPrefix_UsersPath(t *testing.T) {
	payload := []byte(`{"anonymous_id":"abc","config_path":"/Users/alice/.mcpproxy/config.json"}`)

	err := ScanForPII(payload)
	if err == nil {
		t.Fatalf("expected violation, got nil")
	}
	if !errors.Is(err, ErrAnonymityViolation) {
		t.Fatalf("expected errors.Is(err, ErrAnonymityViolation)=true, got err=%v", err)
	}
	var v *AnonymityViolation
	if !errors.As(err, &v) {
		t.Fatalf("expected *AnonymityViolation, got %T (%v)", err, err)
	}
	if v.Rule != "blocked_prefix" {
		t.Errorf("expected rule=blocked_prefix, got %q", v.Rule)
	}
	if v.Pattern != "/Users/" {
		t.Errorf("expected pattern=/Users/, got %q", v.Pattern)
	}
}

// TestScanForPII_BlockedValue_EnvVar asserts that when BlockedValues is
// populated with an env-var value (e.g., GITHUB_TOKEN), a payload containing
// that value is rejected.
func TestScanForPII_BlockedValue_EnvVar(t *testing.T) {
	// Populate runtime blocked values (simulating T025 startup).
	prev := BlockedValues
	BlockedValues = []string{"ghp_1234567890abcdef"}
	defer func() { BlockedValues = prev }()

	payload := []byte(`{"anonymous_id":"abc","some_field":"ghp_1234567890abcdef"}`)

	err := ScanForPII(payload)
	if err == nil {
		t.Fatalf("expected violation, got nil")
	}
	if !errors.Is(err, ErrAnonymityViolation) {
		t.Fatalf("expected errors.Is(err, ErrAnonymityViolation)=true, got err=%v", err)
	}
	var v *AnonymityViolation
	if !errors.As(err, &v) {
		t.Fatalf("expected *AnonymityViolation, got %T (%v)", err, err)
	}
	if v.Rule != "blocked_value" {
		t.Errorf("expected rule=blocked_value, got %q", v.Rule)
	}
}

// TestScanForPII_CleanPayload asserts that a well-formed v3 payload with no
// blocked prefixes passes cleanly.
func TestScanForPII_CleanPayload(t *testing.T) {
	payload := []byte(`{"anonymous_id":"550e8400-e29b-41d4-a716-446655440000","version":"v0.25.0","os":"darwin","arch":"arm64","schema_version":3,"env_kind":"interactive","env_markers":{"has_ci_env":false,"has_cloud_ide_env":false,"is_container":false,"has_tty":true,"has_display":true}}`)

	if err := ScanForPII(payload); err != nil {
		t.Fatalf("expected clean payload to pass, got err=%v", err)
	}
}

// TestScanForPII_EnvMarkersNonBool asserts that a payload where an
// env_markers.* field is a string (not a bool) is rejected. This defends
// against a future refactor accidentally widening an EnvMarkers field to a
// non-boolean type.
func TestScanForPII_EnvMarkersNonBool(t *testing.T) {
	payload := []byte(`{"anonymous_id":"abc","env_markers":{"has_ci_env":"yes","has_cloud_ide_env":false,"is_container":false,"has_tty":true,"has_display":true}}`)

	err := ScanForPII(payload)
	if err == nil {
		t.Fatalf("expected violation for non-bool env_markers field, got nil")
	}
	if !errors.Is(err, ErrAnonymityViolation) {
		t.Fatalf("expected errors.Is(err, ErrAnonymityViolation)=true, got err=%v", err)
	}
	var v *AnonymityViolation
	if !errors.As(err, &v) {
		t.Fatalf("expected *AnonymityViolation, got %T (%v)", err, err)
	}
	if v.Rule != "env_markers_non_bool" {
		t.Errorf("expected rule=env_markers_non_bool, got %q", v.Rule)
	}
}

// TestPopulateBlockedValuesFrom (Spec 044 T025) verifies the runtime-value
// appender collects hostname, home-dir basename, and sensitive env-var values
// into BlockedValues, deduplicating and dropping entries shorter than 3 bytes.
func TestPopulateBlockedValuesFrom(t *testing.T) {
	prev := BlockedValues
	BlockedValues = nil
	defer func() { BlockedValues = prev }()

	fakeHost := func() (string, error) { return "my-host.local", nil }
	fakeHome := func() (string, error) { return "/Users/alice", nil }
	fakeEnv := func(name string) string {
		switch name {
		case "GITHUB_TOKEN":
			return "ghp_secret12345"
		case "OPENAI_API_KEY":
			return "sk-openaitestvalue"
		case "ANTHROPIC_API_KEY":
			return "" // absent
		case "GOOGLE_API_KEY":
			return "x" // too short, must be dropped
		default:
			return ""
		}
	}

	populateBlockedValuesFrom(fakeHost, fakeHome, fakeEnv)

	want := map[string]bool{
		"my-host.local":      true,
		"alice":              true, // basename of home dir
		"ghp_secret12345":    true,
		"sk-openaitestvalue": true,
	}
	got := map[string]bool{}
	for _, v := range BlockedValues {
		got[v] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("BlockedValues missing %q (got %v)", w, BlockedValues)
		}
	}
	for _, v := range BlockedValues {
		if len(v) < 3 {
			t.Errorf("BlockedValues contains short value %q (<3 bytes)", v)
		}
	}

	// Running a second time must be idempotent: no duplicates.
	populateBlockedValuesFrom(fakeHost, fakeHome, fakeEnv)
	counts := map[string]int{}
	for _, v := range BlockedValues {
		counts[v]++
	}
	for v, c := range counts {
		if c > 1 {
			t.Errorf("BlockedValues has duplicate %q (count=%d)", v, c)
		}
	}
}

// TestScanForPII_RawMachineIDBlocked asserts that when the raw OS machine id is
// added to BlockedValues (defense-in-depth, as the telemetry service does for
// hostname/tokens), a payload accidentally carrying the raw id is rejected. The
// hashed machine_id we DO emit is unrelated to the raw value and passes. Skips
// when the OS machine id is unreadable on this host.
func TestScanForPII_RawMachineIDBlocked(t *testing.T) {
	raw, err := machineid.ID()
	if err != nil || len(raw) < 3 {
		t.Skipf("OS machine id unavailable on this host (%v); skipping", err)
	}

	prev := BlockedValues
	BlockedValues = []string{raw}
	defer func() { BlockedValues = prev }()

	// A payload leaking the raw id must be caught.
	leak := []byte(`{"anonymous_id":"abc","machine_id":"` + raw + `"}`)
	if scanErr := ScanForPII(leak); scanErr == nil {
		t.Fatalf("expected violation when raw machine id present, got nil")
	} else if !errors.Is(scanErr, ErrAnonymityViolation) {
		t.Fatalf("expected ErrAnonymityViolation, got %v", scanErr)
	}

	// The hashed value we actually emit must NOT contain the raw id and must
	// pass the scan.
	hashed := protectedMachineID()
	if hashed != "" && strings.Contains(hashed, raw) {
		t.Fatalf("hashed machine_id leaked the raw id")
	}
	clean := []byte(`{"anonymous_id":"abc","machine_id":"` + hashed + `"}`)
	if scanErr := ScanForPII(clean); scanErr != nil {
		t.Errorf("hashed machine_id payload should pass scan, got %v", scanErr)
	}
}

// TestScanForPII_V7FieldViolations (Spec 080 FR-016) asserts the scanner
// structurally validates every v7 field on the serialized form: booleans,
// non-negative integers, and documented fixed enums only. Each case injects
// one malformed field into an otherwise-clean payload and expects the
// v7_field_invalid rule to fire with the field name as the pattern.
func TestScanForPII_V7FieldViolations(t *testing.T) {
	cases := map[string]struct {
		payload string
		field   string
	}{
		"wizard_shown non-bool": {
			payload: `{"anonymous_id":"abc","wizard_shown":"yes"}`,
			field:   "wizard_shown",
		},
		"web_ui_opened negative": {
			payload: `{"anonymous_id":"abc","web_ui_opened":-1}`,
			field:   "web_ui_opened",
		},
		"web_ui_opened fractional": {
			payload: `{"anonymous_id":"abc","web_ui_opened":1.5}`,
			field:   "web_ui_opened",
		},
		"web_ui_opened string": {
			payload: `{"anonymous_id":"abc","web_ui_opened":"3"}`,
			field:   "web_ui_opened",
		},
		"days_since_install negative": {
			payload: `{"anonymous_id":"abc","days_since_install":-3}`,
			field:   "days_since_install",
		},
		"active_days_30d negative": {
			payload: `{"anonymous_id":"abc","active_days_30d":-1}`,
			field:   "active_days_30d",
		},
		"previous_shutdown outside enum": {
			payload: `{"anonymous_id":"abc","previous_shutdown":"terminated by user"}`,
			field:   "previous_shutdown",
		},
		"previous_shutdown non-string": {
			payload: `{"anonymous_id":"abc","previous_shutdown":1}`,
			field:   "previous_shutdown",
		},
		"last_error_code free text": {
			payload: `{"anonymous_id":"abc","last_error_code":"connection refused on port 8080"}`,
			field:   "last_error_code",
		},
		"last_error_code lowercase": {
			payload: `{"anonymous_id":"abc","last_error_code":"mcpx_docker_cli_not_found"}`,
			field:   "last_error_code",
		},
		"last_error_code shape-valid but not in diagnostics catalog": {
			payload: `{"anonymous_id":"abc","last_error_code":"MCPX_UPSTREAM_CONNECT_REFUSED"}`,
			field:   "last_error_code",
		},
		"wizard_connect_step outside enum": {
			payload: `{"anonymous_id":"abc","wizard_connect_step":"my custom step"}`,
			field:   "wizard_connect_step",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ScanForPII([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected violation for payload %s, got nil", tc.payload)
			}
			if !errors.Is(err, ErrAnonymityViolation) {
				t.Fatalf("expected errors.Is(err, ErrAnonymityViolation)=true, got %v", err)
			}
			var v *AnonymityViolation
			if !errors.As(err, &v) {
				t.Fatalf("expected *AnonymityViolation, got %T (%v)", err, err)
			}
			if v.Rule != "v7_field_invalid" {
				t.Errorf("expected rule=v7_field_invalid, got %q", v.Rule)
			}
			if v.Pattern != tc.field {
				t.Errorf("expected pattern=%q, got %q", tc.field, v.Pattern)
			}
		})
	}
}

// TestScanForPII_V7FieldValidValues asserts every documented v7 value shape
// passes: booleans, non-negative integers (including 0), and each member of
// the fixed enums — including the widened wizard_connect_step and the
// spec-allowed "unknown" previous_shutdown.
func TestScanForPII_V7FieldValidValues(t *testing.T) {
	payloads := []string{
		`{"anonymous_id":"abc","schema_version":7,"wizard_shown":true,"web_ui_opened":3,"days_since_install":0,"active_days_30d":30}`,
		`{"anonymous_id":"abc","schema_version":7,"previous_shutdown":"clean"}`,
		`{"anonymous_id":"abc","schema_version":7,"previous_shutdown":"crash"}`,
		`{"anonymous_id":"abc","schema_version":7,"previous_shutdown":"unknown"}`,
		`{"anonymous_id":"abc","schema_version":7,"last_error_code":"MCPX_DOCKER_CLI_NOT_FOUND"}`,
		`{"anonymous_id":"abc","schema_version":7,"wizard_connect_step":"completed"}`,
		`{"anonymous_id":"abc","schema_version":7,"wizard_connect_step":"completed_external"}`,
		`{"anonymous_id":"abc","schema_version":7,"wizard_connect_step":"skipped"}`,
		`{"anonymous_id":"abc","schema_version":7,"wizard_connect_step":""}`,
	}
	for _, p := range payloads {
		if err := ScanForPII([]byte(p)); err != nil {
			t.Errorf("expected valid v7 payload to pass, got %v for:\n%s", err, p)
		}
	}
}

// TestScanForPII_AllBlockedPrefixes covers each of the default blocked
// prefixes to guard against a regression that silently drops one.
func TestScanForPII_AllBlockedPrefixes(t *testing.T) {
	cases := map[string]string{
		"/Users/":       `{"path":"/Users/bob/file"}`,
		"/home/":        `{"path":"/home/bob/file"}`,
		`C:\\Users\\`:   `{"path":"C:\\Users\\bob\\file"}`,
		"/var/folders/": `{"path":"/var/folders/xx/yyy"}`,
	}
	for prefix, payload := range cases {
		t.Run(strings.TrimSpace(prefix), func(t *testing.T) {
			err := ScanForPII([]byte(payload))
			if err == nil {
				t.Fatalf("expected violation for prefix %q", prefix)
			}
			var v *AnonymityViolation
			if !errors.As(err, &v) {
				t.Fatalf("expected *AnonymityViolation, got %T", err)
			}
			if v.Pattern != prefix {
				t.Errorf("expected pattern=%q, got %q", prefix, v.Pattern)
			}
		})
	}
}
