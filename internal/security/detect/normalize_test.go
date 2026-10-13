package detect

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase", "IGNORE Previous", "ignore previous"},
		{"collapse whitespace", "do   not\t\n  tell", "do not tell"},
		{"trim edges", "  hello world  ", "hello world"},
		{"strip zero-width", "ig\u200bno\u200dre", "ignore"},
		{"strip bom/zwnbsp", "ignore\ufeffthis", "ignorethis"},
		{"strip bidi", "\u202eignore", "ignore"},
		{"nfkc fullwidth", "ｉｇｎｏｒｅ", "ignore"},
		{"nfkc ligature", "ﬁle", "file"},
		{"contraction don't", "Don't disclose", "do not disclose"},
		{"contraction doesn't", "It doesn't matter", "it does not matter"},
		{"contraction won't", "won't tell", "will not tell"},
		{"contraction can't", "can't show", "cannot show"},
		{"stem plural", "hidden instructions here", "hidden instruction here"},
		{"stem gerund", "disclosing secrets", "disclos secret"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeDirectiveEquivalence pins the contraction-expansion guarantee
// that lets a single "do not" directive matcher catch the contracted form:
// "don't disclose" and "do not tell" both normalize to a "do not <verb>" form.
func TestNormalizeDirectiveEquivalence(t *testing.T) {
	a := Normalize("Don't disclose this to the user")
	b := Normalize("Do not tell the user")
	for _, s := range []string{a, b} {
		if !strings.Contains(s, "do not") {
			t.Errorf("expected normalized %q to contain \"do not\"", s)
		}
	}
}
