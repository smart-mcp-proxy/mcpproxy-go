package security

import (
	"strings"
	"testing"
)

// Masking a whole event payload is only safe if the identifiers the UI joins on
// survive it: a masked parent_id would silently break the code_execution
// parent↔child linking, and a masked session id would break session grouping.
func TestMaskTextLeavesIdentifiersIntact(t *testing.T) {
	d := NewDetector(nil)

	identifiers := []string{
		"01M0WC8EFATS1987C1SXVCHQ94",                       // activity record ULID
		"1787659513199384000-everything-echo-1",            // request id
		"mcp-session-980d2b73-e933-46a0-8d2f-0b9090f6bb79", // MCP session id
		"ws-c90e6550649ad704",                              // work session id
		"scan-everything-1781284446323229000",              // scan id
		"github:create_issue",                              // qualified tool name
	}

	for _, id := range identifiers {
		masked, changed := d.MaskText(id)
		if changed || masked != id {
			t.Errorf("identifier was masked: %q became %q", id, masked)
		}
	}
}

// The same identifiers embedded in a payload that DOES carry a secret: the
// secret goes, the identifiers stay.
func TestMaskTextMasksSecretsWithoutTouchingIdentifiers(t *testing.T) {
	d := NewDetector(nil)

	const secret = "AKIA4XZQPLMN7TYRVBCD"
	text := `{"request_id":"1787659513199384000-everything-echo-1",` +
		`"parent_id":"01M0WC8EFATS1987C1SXVCHQ94",` +
		`"message":"` + secret + `"}`

	masked, _ := d.MaskText(text)

	if strings.Contains(masked, secret) {
		t.Fatalf("secret survived: %q", masked)
	}
	if !strings.Contains(masked, "1787659513199384000-everything-echo-1") {
		t.Fatalf("request id was mangled: %q", masked)
	}
	if !strings.Contains(masked, "01M0WC8EFATS1987C1SXVCHQ94") {
		t.Fatalf("parent id was mangled: %q", masked)
	}
}
