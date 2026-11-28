package diagnostics

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Spec 135 FR-003 / contracts/diagnostics-codes.md: the issuer-mismatch code
// is a registered, documented catalog entry whose page exists on disk and is
// listed in the errors index and the site sidebar.
func TestOAuthIssuerMismatch_CatalogAndDocs(t *testing.T) {
	const code Code = "MCPX_OAUTH_ISSUER_MISMATCH"
	entry, ok := Get(code)
	if !ok {
		t.Fatalf("%s has no catalog entry", code)
	}
	if entry.DocsURL != "https://docs.mcpproxy.app/errors/"+string(code) {
		t.Errorf("docs url = %q", entry.DocsURL)
	}
	if IsPermanent(code) {
		t.Errorf("a refused sign-in response is fixed by signing in again; it must not park the server")
	}
	page, err := os.ReadFile("../../docs/errors/" + string(code) + ".md")
	if err != nil {
		t.Fatalf("docs/errors page missing: %v", err)
	}
	if !strings.Contains(string(page), "id: "+string(code)) {
		t.Errorf("docs page has no front-matter id")
	}
	for _, f := range []struct{ path, want string }{
		{"../../docs/errors/README.md", string(code) + ".md"},
		{"../../website/sidebars.js", "'errors/" + string(code) + "'"},
	} {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), f.want) {
			t.Errorf("%s does not list %s", f.path, code)
		}
	}
	// The refusal's text (it advises a login) classifies as the mismatch.
	msg := "OAuth token exchange failed for gh: MCPX_OAUTH_ISSUER_MISMATCH: the sign-in response could not be confirmed as coming from the expected authorization server (https://as.example): run mcpproxy auth login"
	if got := Classify(errors.New(msg), ClassifierHints{}); got != code {
		t.Errorf("Classify = %q, want %q", got, code)
	}
}
