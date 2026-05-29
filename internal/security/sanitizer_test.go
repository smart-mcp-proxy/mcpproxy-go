package security

import (
	"reflect"
	"strings"
	"testing"
)

func TestSpotlightUntrusted_BasicWrap(t *testing.T) {
	out := SpotlightUntrusted("hello world", "github", "create_issue")
	want := "«untrusted:github/create_issue»\nhello world\n«/untrusted:github/create_issue»"
	if out != want {
		t.Fatalf("unexpected wrap:\n got: %q\nwant: %q", out, want)
	}
}

func TestSpotlightUntrusted_EscapesGuillemets(t *testing.T) {
	out := SpotlightUntrusted("a « b » c", "srv", "tool")
	if !strings.Contains(out, "a &laquo; b &raquo; c") {
		t.Fatalf("expected escaped body, got: %q", out)
	}
	// The only real guillemets must be in the fences.
	body := out
	body = strings.Replace(body, "«untrusted:srv/tool»", "", 1)
	body = strings.Replace(body, "«/untrusted:srv/tool»", "", 1)
	if strings.ContainsAny(body, "«»") {
		t.Fatalf("found unescaped guillemet in body: %q", body)
	}
}

func TestSpotlightUntrusted_EmptyNoOp(t *testing.T) {
	if got := SpotlightUntrusted("", "srv", "tool"); got != "" {
		t.Fatalf("expected empty no-op, got: %q", got)
	}
}

func TestSpotlightUntrusted_FencesContainServerTool(t *testing.T) {
	out := SpotlightUntrusted("x", "myserver", "mytool")
	if !strings.Contains(out, "«untrusted:myserver/mytool»") {
		t.Fatalf("open fence missing server/tool: %q", out)
	}
	if !strings.Contains(out, "«/untrusted:myserver/mytool»") {
		t.Fatalf("close fence missing server/tool: %q", out)
	}
}

func TestStripControlSequences_ANSI(t *testing.T) {
	in := "red\x1b[31mtext\x1b[0m end\x1b]0;title\x07done"
	out, stripped := StripControlSequences(in, map[string]bool{"ansi": true})
	if strings.Contains(out, "\x1b") {
		t.Fatalf("ANSI not stripped: %q", out)
	}
	if out != "redtext enddone" {
		t.Fatalf("unexpected ansi result: %q", out)
	}
	if !reflect.DeepEqual(stripped, []string{"ansi"}) {
		t.Fatalf("expected [ansi], got %v", stripped)
	}
}

func TestStripControlSequences_C0C1KeepsWhitespace(t *testing.T) {
	in := "a\tb\nc\rd\x00e\x07f\u0085g"
	out, stripped := StripControlSequences(in, map[string]bool{"c0c1": true})
	if out != "a\tb\nc\rdefg" {
		t.Fatalf("c0c1 result wrong: %q", out)
	}
	if !reflect.DeepEqual(stripped, []string{"c0c1"}) {
		t.Fatalf("expected [c0c1], got %v", stripped)
	}
}

func TestStripControlSequences_ZeroWidth(t *testing.T) {
	in := "a\u200bb\u200cc\u200dd\u2060e\ufefff"
	out, stripped := StripControlSequences(in, map[string]bool{"zero_width": true})
	if out != "abcdef" {
		t.Fatalf("zero_width result wrong: %q", out)
	}
	if !reflect.DeepEqual(stripped, []string{"zero_width"}) {
		t.Fatalf("expected [zero_width], got %v", stripped)
	}
}

func TestStripControlSequences_Bidi(t *testing.T) {
	in := "a\u202ab\u202ec\u2066d\u2069e"
	out, stripped := StripControlSequences(in, map[string]bool{"bidi": true})
	if out != "abcde" {
		t.Fatalf("bidi result wrong: %q", out)
	}
	if !reflect.DeepEqual(stripped, []string{"bidi"}) {
		t.Fatalf("expected [bidi], got %v", stripped)
	}
}

func TestStripControlSequences_MultipleClassesSorted(t *testing.T) {
	in := "a\u200bb\u202ec\x1b[0md"
	out, stripped := StripControlSequences(in, map[string]bool{
		"ansi": true, "zero_width": true, "bidi": true,
	})
	if out != "abcd" {
		t.Fatalf("multi result wrong: %q", out)
	}
	want := []string{"ansi", "bidi", "zero_width"}
	if !reflect.DeepEqual(stripped, want) {
		t.Fatalf("expected sorted %v, got %v", want, stripped)
	}
}

func TestStripControlSequences_ClassToggledOff(t *testing.T) {
	in := "a\u200bb\u202ec"
	out, stripped := StripControlSequences(in, map[string]bool{
		"bidi": true, "zero_width": false,
	})
	if out != "a\u200bbc" {
		t.Fatalf("expected zero_width preserved, got: %q", out)
	}
	if !reflect.DeepEqual(stripped, []string{"bidi"}) {
		t.Fatalf("expected [bidi], got %v", stripped)
	}
}

func TestStripControlSequences_NoOp(t *testing.T) {
	in := "clean text"
	out, stripped := StripControlSequences(in, map[string]bool{
		"ansi": true, "c0c1": true, "zero_width": true, "bidi": true,
	})
	if out != in {
		t.Fatalf("expected unchanged, got: %q", out)
	}
	if len(stripped) != 0 {
		t.Fatalf("expected no stripped classes, got %v", stripped)
	}
}
