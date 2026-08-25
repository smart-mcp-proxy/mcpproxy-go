package security

import (
	"strings"
	"testing"
)

func TestMaskValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"aws key keeps its prefix", "AKIAIOSFODNN7EXAMPLE", "AKIA…****"},
		{"github token keeps its prefix", "ghp_1234567890abcdefghij", "ghp_…****"},
		{"short value keeps nothing", "abcd", "****"},
		{"empty stays empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskValue(tc.value); got != tc.want {
				t.Fatalf("MaskValue(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestMaskTextMasksDetectedSecret(t *testing.T) {
	d := NewDetector(nil)

	const secret = "AKIAIOSFODNN7EXAMPLE"
	text := "Echo: " + secret + " aws key here"

	masked, changed := d.MaskText(text)

	if !changed {
		t.Fatal("expected MaskText to report a change")
	}
	if strings.Contains(masked, secret) {
		t.Fatalf("masked text still contains the secret: %q", masked)
	}
	if !strings.Contains(masked, "AKIA…****") {
		t.Fatalf("masked text lost the recognisable prefix: %q", masked)
	}
	if !strings.Contains(masked, "aws key here") {
		t.Fatalf("masked text lost its surrounding context: %q", masked)
	}
}

func TestMaskTextLeavesCleanTextAlone(t *testing.T) {
	d := NewDetector(nil)

	text := `{"message":"hello world","count":3}`
	masked, changed := d.MaskText(text)

	if changed {
		t.Fatalf("clean text reported as masked: %q", masked)
	}
	if masked != text {
		t.Fatalf("clean text was modified: %q", masked)
	}
}

func TestMaskArgumentsWalksNestedValues(t *testing.T) {
	d := NewDetector(nil)

	const secret = "AKIAIOSFODNN7EXAMPLE"
	args := map[string]interface{}{
		"message": secret + " aws key here",
		"nested": map[string]interface{}{
			"deep": []interface{}{"prefix " + secret},
		},
		"count": float64(3),
	}

	masked := d.MaskArguments(args)

	if got := masked["message"].(string); strings.Contains(got, secret) {
		t.Fatalf("top-level value not masked: %q", got)
	}
	nested := masked["nested"].(map[string]interface{})
	deep := nested["deep"].([]interface{})
	if got := deep[0].(string); strings.Contains(got, secret) {
		t.Fatalf("nested value not masked: %q", got)
	}
	if masked["count"] != float64(3) {
		t.Fatalf("non-string value altered: %v", masked["count"])
	}

	// The caller's map belongs to storage and must survive untouched.
	if got := args["message"].(string); !strings.Contains(got, secret) {
		t.Fatalf("input map was mutated: %q", got)
	}
}

func TestStripInternalArgs(t *testing.T) {
	args := map[string]interface{}{
		"message":         "hello",
		"_auth_user_id":   "u-1",
		"_auth_auth_type": "admin",
	}

	stripped := StripInternalArgs(args)

	if _, ok := stripped["_auth_auth_type"]; ok {
		t.Fatalf("internal key survived: %v", stripped)
	}
	if _, ok := stripped["_auth_user_id"]; ok {
		t.Fatalf("internal key survived: %v", stripped)
	}
	if stripped["message"] != "hello" {
		t.Fatalf("caller argument dropped: %v", stripped)
	}
	if len(args) != 3 {
		t.Fatalf("input map was mutated: %v", args)
	}
}

func TestStripInternalArgsReturnsInputWhenClean(t *testing.T) {
	args := map[string]interface{}{"message": "hello"}
	if got := StripInternalArgs(args); len(got) != 1 || got["message"] != "hello" {
		t.Fatalf("clean arguments altered: %v", got)
	}
}
