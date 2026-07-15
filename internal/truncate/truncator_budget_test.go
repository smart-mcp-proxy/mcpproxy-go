package truncate

import (
	"strings"
	"testing"
)

// TestSimpleTruncateBudget (spec 084, T031b): the budget must equal the number
// of content bytes simpleTruncate actually retains — limit minus its message
// space — NOT the raw limit (finding 2: a limit-based guard would be wrong by
// up to 200 bytes). 0 means unlimited (no truncation).
func TestSimpleTruncateBudget(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		want  int
	}{
		{"unlimited", 0, 0},
		{"large limit keeps limit-200", 5000, 4800},
		{"boundary limit=400 keeps 200", 400, 200},
		// The messageSpace boundary: below 200 the truncator uses limit/2
		// for the message, keeping the other (rounded-up) half.
		{"small limit=100 keeps half", 100, 50},
		{"odd small limit=101 keeps ceil half", 101, 51},
		{"limit=200 keeps limit-200=0", 200, 0},
		{"limit=201 keeps 1", 201, 1},
		{"tiny limit=1 keeps 1", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTruncator(tc.limit)
			if got := tr.SimpleTruncateBudget(); got != tc.want {
				t.Errorf("SimpleTruncateBudget(limit=%d) = %d, want %d", tc.limit, got, tc.want)
			}
		})
	}
}

// TestSimpleTruncateBudget_MatchesSimpleTruncate: the budget must be exactly
// the retained-prefix length simpleTruncate produces for oversize non-JSON
// content (the path an encoded TOON block always hits — it is not valid JSON).
func TestSimpleTruncateBudget_MatchesSimpleTruncate(t *testing.T) {
	for _, limit := range []int{1, 50, 100, 199, 200, 201, 300, 399, 400, 401, 1000, 5000} {
		tr := NewTruncator(limit)
		content := strings.Repeat("x", limit+1000) // non-JSON, always over limit
		out := tr.simpleTruncate(content)
		retained := strings.TrimSuffix(out, "\n\n... [truncated by mcpproxy, cache not available]")
		if got, want := tr.SimpleTruncateBudget(), len(retained); got != want {
			t.Errorf("limit=%d: SimpleTruncateBudget = %d, but simpleTruncate retained %d bytes", limit, got, want)
		}
	}
}
