package bench

import (
	"strings"
	"testing"
)

// TestCanonicalJSON_TrailingData verifies the strict top-level EOF check:
// a JSON stream with ANY bytes after the first value is rejected. dec.More()
// alone is not a valid trailing-data check at the top level (it returns false
// for a next byte of '}' or ']'), so the decoder must be drained to io.EOF.
func TestCanonicalJSON_TrailingData(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // canonical output for valid input; "" means error expected
		ok    bool
	}{
		{name: "valid object", input: `{"b":1,"a":2}`, want: `{"a":2,"b":1}`, ok: true},
		{name: "valid with whitespace", input: " {\n  \"a\": 1\n } \n", want: `{"a":1}`, ok: true},
		{name: "two objects", input: `{} {}`, ok: false},
		{name: "trailing garbage", input: `{}garbage`, ok: false},
		{name: "trailing close brace", input: `{}}`, ok: false},
		{name: "trailing close bracket", input: `{}]`, ok: false},
		{name: "two scalars", input: `1 2`, ok: false},
		{name: "empty input", input: ``, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalJSON([]byte(tc.input))
			if tc.ok {
				if err != nil {
					t.Fatalf("CanonicalJSON(%q) unexpected error: %v", tc.input, err)
				}
				if got != tc.want {
					t.Errorf("CanonicalJSON(%q) = %q, want %q", tc.input, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("CanonicalJSON(%q) = %q, want error (trailing data must be rejected)", tc.input, got)
			}
		})
	}
}

// TestCanonicalJSON_NumberLiteralsPreserved guards the json.Number path: no
// float round-trip may rewrite a number literal.
func TestCanonicalJSON_NumberLiteralsPreserved(t *testing.T) {
	got, err := CanonicalJSON([]byte(`{"n": 12345678901234567890, "f": 0.10}`))
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(got, "12345678901234567890") || !strings.Contains(got, "0.10") {
		t.Errorf("number literals not preserved verbatim: %s", got)
	}
}
