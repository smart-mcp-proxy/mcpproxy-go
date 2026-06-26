package checks

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

func TestEmbeddedSecret_MustFlag(t *testing.T) {
	c := &EmbeddedSecret{}
	cases := []struct {
		name    string
		desc    string
		minConf float64
	}{
		{
			"live-aws-key",
			"Returns the weather. Internal note: use key AKIA1234567890ABCDEF for the backend.",
			0.8,
		},
		{
			"luhn-card",
			"Processes a payment using the card 4539578763621486 on file.",
			0.9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sigs := c.Inspect(view("t", tc.desc), detect.RegistryView{})
			if len(sigs) == 0 {
				t.Fatalf("expected a signal, got none")
			}
			s := sigs[0]
			if s.Tier != detect.TierSoft {
				t.Errorf("must be soft, got %v", s.Tier)
			}
			if s.CheckID != c.ID() {
				t.Errorf("CheckID = %q, want %q", s.CheckID, c.ID())
			}
			if s.Confidence < tc.minConf {
				t.Errorf("confidence %v < want %v (live secret should be high)", s.Confidence, tc.minConf)
			}
		})
	}
}

func TestEmbeddedSecret_MustNotFlag(t *testing.T) {
	c := &EmbeddedSecret{}
	cases := []struct {
		name string
		desc string
	}{
		{"documented-example-key", "Example usage: set AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE in your env."},
		{"no-secret", "Adds two numbers and returns the sum."},
		{"prose-only", "Generates Markdown documentation describing how to rotate an API key safely."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sigs := c.Inspect(view("t", tc.desc), detect.RegistryView{})
			if len(sigs) != 0 {
				t.Fatalf("expected no signal for %q, got %+v", tc.desc, sigs)
			}
		})
	}
}

// Evidence must never echo the full secret back into a report.
func TestEmbeddedSecret_EvidenceMasked(t *testing.T) {
	c := &EmbeddedSecret{}
	secret := "AKIA1234567890ABCDEF"
	sigs := c.Inspect(view("t", "key "+secret), detect.RegistryView{})
	if len(sigs) == 0 {
		t.Fatal("expected a signal")
	}
	if got := sigs[0].Evidence; got == secret || len(got) == 0 {
		t.Errorf("evidence must be masked, got %q", got)
	}
}
