package patterns

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConfidenceFor_ValidatedCardHigh proves a Luhn-validated credit card match
// carries high confidence (Spec 076 T015: "validated card/Luhn → high").
func TestConfidenceFor_ValidatedCardHigh(t *testing.T) {
	p := creditCardPattern()
	// A real, Luhn-valid Visa (not in the known-example set).
	match := "4539578763621486"
	assert.True(t, p.IsValid(match), "test card must pass Luhn so the case is meaningful")
	assert.False(t, p.IsKnownExample(match), "test card must not be a documented example")
	assert.GreaterOrEqual(t, p.ConfidenceFor(match), 0.9,
		"a Luhn-validated card should be high confidence")
}

// TestConfidenceFor_KnownExampleLow proves a documented example value (e.g. an
// AWS doc key) collapses confidence to near-zero so it is not treated as a live
// secret (Spec 076 T016 MUST-NOT-flag: "AKIA…EXAMPLE").
func TestConfidenceFor_KnownExampleLow(t *testing.T) {
	p := awsAccessKeyPattern()
	example := "AKIAIOSFODNN7EXAMPLE"
	assert.True(t, p.IsKnownExample(example), "must be a known example for the case to be meaningful")
	assert.LessOrEqual(t, p.ConfidenceFor(example), 0.2,
		"a documented example key should be low confidence")
}

// TestConfidenceFor_LiveCloudKeyHigh proves a live (non-example) AWS access key
// is high confidence (Spec 076 T016 MUST-flag: "a live API key … high").
func TestConfidenceFor_LiveCloudKeyHigh(t *testing.T) {
	p := awsAccessKeyPattern()
	live := "AKIA1234567890ABCDEF"
	assert.False(t, p.IsKnownExample(live))
	assert.GreaterOrEqual(t, p.ConfidenceFor(live), 0.8,
		"a live cloud credential should be high confidence")
}

// TestConfidenceFor_GenericTokenLow proves a generic/low-distinctiveness matcher
// (the medium-severity bearer-token regex) yields low confidence — the
// "entropy-only → low" end of the scale (Spec 076 T015).
func TestConfidenceFor_GenericTokenLow(t *testing.T) {
	p := bearerTokenPattern()
	match := "bearer abcdefghijklmnopqrstuvwxyz0123"
	assert.LessOrEqual(t, p.ConfidenceFor(match), 0.4,
		"a generic bearer-token match should be low confidence")
}

// TestConfidenceFor_SeverityDefaults proves the default mapping ranks severities
// monotonically and stays within [0,1].
func TestConfidenceFor_SeverityDefaults(t *testing.T) {
	crit := NewPattern("c").WithRegex(`crit`).WithSeverity(SeverityCritical).Build()
	high := NewPattern("h").WithRegex(`high`).WithSeverity(SeverityHigh).Build()
	med := NewPattern("m").WithRegex(`med`).WithSeverity(SeverityMedium).Build()
	low := NewPattern("l").WithRegex(`low`).WithSeverity(SeverityLow).Build()

	cc := crit.ConfidenceFor("crit")
	hc := high.ConfidenceFor("high")
	mc := med.ConfidenceFor("med")
	lc := low.ConfidenceFor("low")

	assert.Greater(t, cc, hc)
	assert.Greater(t, hc, mc)
	assert.Greater(t, mc, lc)
	for _, c := range []float64{cc, hc, mc, lc} {
		assert.GreaterOrEqual(t, c, 0.0)
		assert.LessOrEqual(t, c, 1.0)
	}
}

// TestConfidenceFor_ExplicitOverride proves WithConfidence overrides the
// severity default without disturbing Match/IsValid behavior.
func TestConfidenceFor_ExplicitOverride(t *testing.T) {
	p := NewPattern("x").WithRegex(`x`).WithSeverity(SeverityCritical).WithConfidence(0.33).Build()
	assert.InDelta(t, 0.33, p.ConfidenceFor("x"), 0.0001)
	// Existing behavior is untouched.
	assert.Equal(t, []string{"x"}, p.Match("ab x"))
}
