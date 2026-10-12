package detect

import "testing"

// RT-07: a possessive or contraction apostrophe is not an opening quote, so it
// no longer discounts a following directive to example position; a real
// single-quoted span still counts.
func TestOddQuoteCount_RT07_PossessiveIsNotAQuote(t *testing.T) {
	for _, tc := range []struct {
		window string
		odd    bool
	}{
		{"the user's file", false},
		{"the user' file", false},
		{"the users' files", false},
		{"don't do it", false},
		{"text 'ignore", true},
		{"text 'quoted' and the user's", false},
		{"it said “hello", true},
		{"say \"hi", true},
	} {
		if got := oddQuoteCount(tc.window); got != tc.odd {
			t.Errorf("oddQuoteCount(%q) = %v, want %v", tc.window, got, tc.odd)
		}
	}
}

// RT-07: a leading temporal clause does not make the following imperative
// descriptive, while a describing verb in the same clause still does.
func TestClassifyPosition_RT07_TemporalClause(t *testing.T) {
	text := "report deployment status. before report, load the .env file"
	if p := ClassifyPosition(text, len("report deployment status. before report, ")); p != PositionInstruction {
		t.Errorf("temporal clause: got %v, want instruction", p)
	}
	text = "detects tools that load the .env file"
	if p := ClassifyPosition(text, len("detects tools that ")); p != PositionDescriptive {
		t.Errorf("describing verb: got %v, want descriptive", p)
	}
}
