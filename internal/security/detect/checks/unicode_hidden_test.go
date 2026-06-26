package checks

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// inspectOne builds a single-tool RegistryView and returns the signals the
// check emits for that tool. Cross-tool checks get the same one-tool view.
func inspectOne(c detect.Check, tv detect.ToolView) []detect.Signal {
	reg := detect.NewRegistryView([]detect.ToolView{tv})
	return c.Inspect(reg.Tools[0], reg)
}

func TestUnicodeHidden_FlagsZeroWidth(t *testing.T) {
	// A single zero-width space (U+200B) smuggled into the raw description.
	tv := detect.ToolView{Server: "a", Name: "transfer", Description: "transfer\u200bfunds between accounts"}
	sigs := inspectOne(&UnicodeHidden{}, tv)
	if len(sigs) == 0 {
		t.Fatalf("expected a signal for zero-width char, got none")
	}
	if sigs[0].Tier != detect.TierHard {
		t.Errorf("unicode.hidden must be a hard signal, got tier %v", sigs[0].Tier)
	}
	if sigs[0].CheckID != "unicode.hidden" {
		t.Errorf("CheckID = %q, want unicode.hidden", sigs[0].CheckID)
	}
}

func TestUnicodeHidden_EscalatesOnThreeClasses(t *testing.T) {
	// zero-width (U+200B) + bidi override (U+202E) + PUA (U+E000) = 3 classes.
	tv := detect.ToolView{Server: "a", Name: "x", Description: "a\u200b\u202eb\ue000c"}
	sigs := inspectOne(&UnicodeHidden{}, tv)
	if len(sigs) == 0 {
		t.Fatalf("expected a signal for 3 hidden classes, got none")
	}
	if sigs[0].Confidence < 0.9 {
		t.Errorf("at least 3 classes must escalate to near-1.0 confidence, got %v", sigs[0].Confidence)
	}
}

func TestUnicodeHidden_EscalatesOnDecodedTagMessage(t *testing.T) {
	// Unicode TAG block chars U+E0072 U+E006D decode to ASCII "rm".
	tv := detect.ToolView{Server: "a", Name: "x", Description: "weather \U000E0072\U000E006D tool"}
	sigs := inspectOne(&UnicodeHidden{}, tv)
	if len(sigs) == 0 {
		t.Fatalf("expected a signal for tag-block message, got none")
	}
	if sigs[0].Confidence < 0.9 {
		t.Errorf("decoded tag message must escalate, got confidence %v", sigs[0].Confidence)
	}
	if !strings.Contains(sigs[0].Evidence, "rm") {
		t.Errorf("evidence should reveal decoded tag message %q", sigs[0].Evidence)
	}
}

func TestUnicodeHidden_IgnoresPlainASCII(t *testing.T) {
	tv := detect.ToolView{Server: "a", Name: "weather", Description: "Get the current weather for a city. Returns temperature."}
	if sigs := inspectOne(&UnicodeHidden{}, tv); len(sigs) != 0 {
		t.Errorf("plain ASCII must not flag, got %+v", sigs)
	}
}

func TestUnicodeHidden_IgnoresAccentedUnicode(t *testing.T) {
	tv := detect.ToolView{Server: "a", Name: "cafe", Description: "Naïve café résumé über jalapeño."}
	if sigs := inspectOne(&UnicodeHidden{}, tv); len(sigs) != 0 {
		t.Errorf("ordinary accented Unicode must not flag, got %+v", sigs)
	}
}
