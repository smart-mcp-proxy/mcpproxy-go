package security

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// A private-key pattern matches only the BEGIN line, so masking the match
// alone would replace the label and serve the key.
func TestMaskTextMasksWholeKeyBlock(t *testing.T) {
	d := NewDetector(nil)

	const body = "MIIEowIBAAKCAQEAx7Ke9SecretKeyBodyThatMustNeverBeRendered1234567890"
	text := "before\n-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----\nafter"

	masked, changed := d.MaskText(text)

	if !changed {
		t.Fatal("expected the key block to be masked")
	}
	if strings.Contains(masked, body) {
		t.Fatalf("key body survived masking: %q", masked)
	}
	if strings.Contains(masked, "BEGIN RSA PRIVATE KEY") || strings.Contains(masked, "END RSA PRIVATE KEY") {
		t.Fatalf("key envelope survived masking: %q", masked)
	}
	if !strings.Contains(masked, "before") || !strings.Contains(masked, "after") {
		t.Fatalf("masking ate the surrounding text: %q", masked)
	}
}

func TestMaskTextMasksUnterminatedKeyBlock(t *testing.T) {
	d := NewDetector(nil)

	const body = "MIIEowIBAAKCAQEAtruncatedSecretBody0987654321"
	text := "note\n-----BEGIN OPENSSH PRIVATE KEY-----\n" + body

	masked, _ := d.MaskText(text)

	if strings.Contains(masked, body) {
		t.Fatalf("truncated key body survived masking: %q", masked)
	}
	if !strings.Contains(masked, "note") {
		t.Fatalf("masking ate the leading text: %q", masked)
	}
}

// A block must end at ITS OWN footer. Stopping at the first "-----END" left
// the remainder of an outer key readable whenever another block was quoted
// inside it.
func TestMaskTextMasksNestedKeyBlocks(t *testing.T) {
	d := NewDetector(nil)

	const outerTail = "OUTERKEYREMAINDERxyz0123456789ABCDEFGHIJKLMNOP"
	const innerBody = "INNERKEYBODYabcdefghijklmnop0123456789"
	text := "-----BEGIN RSA PRIVATE KEY-----\n" +
		"-----BEGIN EC PRIVATE KEY-----\n" + innerBody + "\n-----END EC PRIVATE KEY-----\n" +
		outerTail + "\n-----END RSA PRIVATE KEY-----\ntrailing text"

	masked, _ := d.MaskText(text)

	if strings.Contains(masked, outerTail) {
		t.Fatalf("outer key remainder survived: %q", masked)
	}
	if strings.Contains(masked, innerBody) {
		t.Fatalf("inner key body survived: %q", masked)
	}
	if !strings.Contains(masked, "trailing text") {
		t.Fatalf("masking ate the text after the block: %q", masked)
	}
}

// A custom pattern filed under private_key can match anything; block masking
// must not eat the text that follows an ordinary match.
func TestMaskTextDoesNotBlockMaskANonHeaderPrivateKeyMatch(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Categories["high_entropy"] = false
	cfg.CustomPatterns = []config.CustomPattern{{
		Name:     "internal_key_label",
		Regex:    `INTERNAL-KEY-[0-9]{4}`,
		Category: "private_key",
		Severity: "high",
	}}
	d := NewDetector(cfg)

	masked, changed := d.MaskText("label INTERNAL-KEY-1234 and everything after it")

	if !changed {
		t.Fatalf("custom pattern did not mask: %q", masked)
	}
	if !strings.Contains(masked, "and everything after it") {
		t.Fatalf("block masking swallowed the trailing text: %q", masked)
	}
	if strings.Contains(masked, "INTERNAL-KEY-1234") {
		t.Fatalf("custom match not masked: %q", masked)
	}
}

// Masking must not inherit Scan's find-limit: everything the sweep skips is a
// value served in the clear.
func TestMaskTextMasksEveryHighEntropyValue(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	d := NewDetector(cfg)

	var b strings.Builder
	const count = 800
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "v%d=%s\n", i, highEntropyValue(i))
	}

	masked, _ := d.MaskText(b.String())

	for _, i := range []int{0, count / 2, count - 1} {
		if strings.Contains(masked, highEntropyValue(i)) {
			t.Fatalf("high-entropy value %d survived masking", i)
		}
	}
}

// A 48-char base64-alphabet token over the entropy threshold, distinct per
// index. Driven by an LCG so the characters do not repeat in a short cycle —
// a low-entropy fixture would pass the test for the wrong reason.
func highEntropyValue(i int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	state := uint32(i)*2654435761 + 12345
	var b strings.Builder
	for j := 0; j < 48; j++ {
		state = state*1664525 + 1013904223
		b.WriteByte(alphabet[(state>>16)%uint32(len(alphabet))])
	}
	return b.String()
}

// The replacement pass must not stop at the detection cap: the cap bounds what
// is worth REPORTING, and reusing it here would leave later secrets in the clear.
func TestMaskTextMasksBeyondTheDetectionCap(t *testing.T) {
	d := NewDetector(nil)

	var b strings.Builder
	const count = MaxDetectionsPerScan * 2
	for i := 0; i < count; i++ {
		// 36+ chars after the prefix — the shape the GitHub PAT pattern matches.
		fmt.Fprintf(&b, "key%d=ghp_%036d\n", i, i)
	}
	text := b.String()

	masked, _ := d.MaskText(text)

	if strings.Contains(masked, fmt.Sprintf("ghp_%036d", 0)) || strings.Contains(masked, fmt.Sprintf("ghp_%036d", count-1)) {
		t.Fatalf("a token survived past the cap:\n%s", masked)
	}
	if got := strings.Count(masked, "ghp_…****"); got != count {
		t.Fatalf("masked %d of %d tokens", got, count)
	}
}

// Removing the replacement cap makes "a payload stuffed with secrets" the
// worst case, so it must not be quadratic: a full-size activity response
// (64KB, the activity_max_response_size cap) of nothing but distinct tokens
// still has to mask in well under a second.
func TestMaskTextStaysCheapOnAPayloadFullOfSecrets(t *testing.T) {
	d := NewDetector(nil)

	var b strings.Builder
	for i := 0; b.Len() < 64*1024; i++ {
		fmt.Fprintf(&b, "key%d=ghp_%036d\n", i, i)
	}
	text := b.String()

	start := time.Now()
	masked, _ := d.MaskText(text)
	elapsed := time.Since(start)

	if strings.Contains(masked, "ghp_0000") {
		t.Fatal("tokens survived masking")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("masking a %d-byte payload took %s", len(text), elapsed)
	}
	t.Logf("masked %d bytes in %s", len(text), elapsed)
}

// Turning detection off later must not retroactively serve the credentials in
// records it already flagged.
func TestMaskTextMasksEvenWhenDetectionIsDisabled(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Enabled = false
	d := NewDetector(cfg)

	masked, changed := d.MaskText("AKIAIOSFODNN7EXAMPLE")

	if !changed || strings.Contains(masked, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("secret served with detection disabled: %q", masked)
	}
}

// A category the operator excluded is neither flagged nor masked.
func TestMaskTextHonoursDisabledCategory(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Categories["cloud_credentials"] = false
	cfg.Categories["high_entropy"] = false
	d := NewDetector(cfg)

	masked, changed := d.MaskText("AKIAIOSFODNN7EXAMPLE")

	if changed || masked != "AKIAIOSFODNN7EXAMPLE" {
		t.Fatalf("excluded category was masked anyway: %q", masked)
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
