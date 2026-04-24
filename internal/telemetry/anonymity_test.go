package telemetry

import (
	"errors"
	"strings"
	"testing"
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
