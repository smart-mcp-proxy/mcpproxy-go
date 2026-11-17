package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Spec 123 T043 (FR-031): the undecryptable-token code is a registered,
// documented catalog entry whose fixes never offer a login, and the
// classifier recognises it ahead of the login-required text matches.

const spec123UndecryptableCode Code = "MCPX_OAUTH_TOKEN_UNDECRYPTABLE"

func TestOAuthTokenUndecryptable_CatalogEntry(t *testing.T) {
	entry, ok := Get(spec123UndecryptableCode)
	if !ok {
		t.Fatalf("%s has no catalog entry", spec123UndecryptableCode)
	}
	if entry.Severity != SeverityError {
		t.Errorf("severity = %q, want error", entry.Severity)
	}
	if entry.DocsURL != "https://docs.mcpproxy.app/errors/"+string(spec123UndecryptableCode) {
		t.Errorf("docs url = %q", entry.DocsURL)
	}
	if IsPermanent(spec123UndecryptableCode) {
		t.Errorf("an undecryptable row is recoverable by restoring the key and restarting; it must not park the server")
	}
	var sawLogout bool
	for _, s := range entry.FixSteps {
		label := strings.ToLower(s.Label)
		if s.FixerKey == "oauth_reauth" || strings.Contains(label, "sign in") || strings.HasPrefix(label, "log in") {
			t.Errorf("fix step %q offers a login; the row must be restored or discarded first", s.Label)
		}
		if strings.Contains(s.Command, "mcpproxy auth logout") {
			sawLogout = true
		}
	}
	if !sawLogout {
		t.Errorf("no fix step names `mcpproxy auth logout` (the discard path)")
	}
}

func TestOAuthTokenUndecryptable_DocsPage(t *testing.T) {
	page, err := os.ReadFile("../../docs/errors/" + string(spec123UndecryptableCode) + ".md")
	if err != nil {
		t.Fatalf("docs/errors page missing: %v", err)
	}
	if !strings.Contains(string(page), "id: "+string(spec123UndecryptableCode)) {
		t.Errorf("docs page has no front-matter id")
	}
	readme, err := os.ReadFile("../../docs/errors/README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), string(spec123UndecryptableCode)+".md") {
		t.Errorf("docs/errors/README.md does not list the code")
	}
	sidebars, err := os.ReadFile("../../website/sidebars.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sidebars), "'errors/"+string(spec123UndecryptableCode)+"'") {
		t.Errorf("website/sidebars.js does not list the page")
	}
}

func TestOAuthTokenUndecryptable_Classifier(t *testing.T) {
	for _, msg := range []string{
		// The producer's canonical sentence, which also names the ways out
		// (one of which mentions a login, so it must win over that match).
		"stored OAuth token cannot be decrypted: no encryption key configured; restore the key, or run `mcpproxy auth logout --server=gh` and then `mcpproxy auth login --server=gh`",
		// The storage error text itself, after string flattening.
		"all authentication strategies failed, last error: stored OAuth token gh_0123456789abcdef cannot be decrypted: key_mismatch (row key id aaaa, configured key id bbbb)",
	} {
		if got := Classify(errors.New(msg), ClassifierHints{}); got != spec123UndecryptableCode {
			t.Errorf("Classify(%q) = %q, want %q", msg, got, spec123UndecryptableCode)
		}
	}
	// The typed fast path.
	wrapped := WrapError(spec123UndecryptableCode, fmt.Errorf("x"))
	if got := Classify(wrapped, ClassifierHints{}); got != spec123UndecryptableCode {
		t.Errorf("typed: got %q", got)
	}
}
