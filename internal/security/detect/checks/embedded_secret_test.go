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

// TestEmbeddedSecret_RestoredCategories locks the two secret categories the
// deleted legacy security.NewDetector(nil) path used to cover but the credential
// pattern matchers do not (Spec 077 US1, Codex finding #3): a sensitive
// file-path reference and a high-entropy opaque blob embedded in tool metadata.
// Losing these silently narrowed secret coverage when the legacy path was
// removed; this test proves the detect check restores it.
func TestEmbeddedSecret_RestoredCategories(t *testing.T) {
	c := &EmbeddedSecret{}
	cases := []struct {
		name    string
		desc    string
		minConf float64
	}{
		// Sensitive file-path references (restored sensitive_file coverage).
		{"ssh-private-key", "Reads the deploy key from ~/.ssh/id_rsa before connecting.", 0.4},
		{"aws-credentials", "Loads AWS creds from ~/.aws/credentials at startup.", 0.4},
		{"etc-passwd", "Enumerates local users by reading /etc/passwd.", 0.4},
		{"dotenv-file", "Loads configuration from the project's .env file.", 0.4},
		{"pem-cert", "Loads the TLS certificate from /opt/app/server.pem.", 0.4},
		// High-entropy opaque blob (restored high_entropy coverage). This 43-char
		// token matches no cloud/key prefix, so only the entropy path catches it.
		{"high-entropy-blob", "Authenticates using the token Aa1Bb2Cc3Dd4Ee5Ff6Gg7Hh8Ii9Jj0Kk1Ll2Mm3Nn4.", 0.3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sigs := c.Inspect(view("t", tc.desc), detect.RegistryView{})
			if len(sigs) == 0 {
				t.Fatalf("expected a signal for %q, got none", tc.desc)
			}
			s := sigs[0]
			if s.Tier != detect.TierSoft {
				t.Errorf("must be soft, got %v", s.Tier)
			}
			if s.CheckID != c.ID() {
				t.Errorf("CheckID = %q, want %q", s.CheckID, c.ID())
			}
			if s.Confidence < tc.minConf {
				t.Errorf("confidence %v < want %v", s.Confidence, tc.minConf)
			}
		})
	}
}

// TestEmbeddedSecret_RestoredCategories_NoFalsePositive keeps the restored
// categories from over-firing: an ordinary long identifier (low entropy) and a
// documented example key must stay below the emit floor.
func TestEmbeddedSecret_RestoredCategories_NoFalsePositive(t *testing.T) {
	c := &EmbeddedSecret{}
	cases := []struct {
		name string
		desc string
	}{
		// Low-entropy long token (repetitive) — below the 4.5-bit threshold.
		{"low-entropy-repeat", "Returns the placeholder value aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa for tests."},
		// Ordinary prose with no sensitive path and no opaque blob.
		{"plain-prose", "Formats a configuration file and returns the pretty-printed result."},
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
