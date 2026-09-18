//go:build server

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// wantCredentialBanner is the honesty line of Spec 107 FR-034 / contracts/
// rest-endpoints.md §12: a stored credential is kept for a future broker and
// is NOT injected into upstream calls. It is spelled out here, independently
// of any production constant, so the test pins the contract wording.
const wantCredentialBanner = "Stored credentials are kept for a future broker and are NOT injected into upstream calls in this release."

// newCredentialWordingServer serves GET /api/v1/user/credentials with the
// given list so the real run functions (not just the render helpers) can be
// driven end-to-end without a server-edition instance.
func newCredentialWordingServer(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user/credentials" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	// Point the CLI at the fake server and force the human-readable format.
	oldURL, oldToken := credServerURL, credToken
	oldFormat, oldJSON := globalOutputFormat, globalJSONOutput
	credServerURL, credToken = srv.URL, "test-jwt"
	globalOutputFormat, globalJSONOutput = "", false
	t.Setenv("MCPPROXY_OUTPUT", "")
	t.Cleanup(func() {
		credServerURL, credToken = oldURL, oldToken
		globalOutputFormat, globalJSONOutput = oldFormat, oldJSON
	})
}

// captureCredentialStdout runs fn and returns everything it wrote to os.Stdout.
func captureCredentialStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fnErr := fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	if fnErr != nil {
		t.Fatalf("command returned error: %v\noutput so far:\n%s", fnErr, out)
	}
	return out
}

// firstLine returns the first line of out (without the trailing newline).
func firstLine(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	return line
}

const credentialWordingFixture = `{"credentials":[
  {"server":"github","mode":"oauth_connect","status":"connected","token_type":"Bearer","scopes":["repo"],"obtained_via":"connect_flow"},
  {"server":"jira","mode":"oauth_connect","status":"not_connected","connect_path":"/api/v1/user/credentials/jira/connect"}
]}`

// FR-034 / contracts/rest-endpoints.md §12: `mcpproxy credential list` output
// begins with the "stored … NOT injected" line.
func TestCredentialList_OutputBeginsWithStoredNotInjected(t *testing.T) {
	newCredentialWordingServer(t, credentialWordingFixture)

	out := captureCredentialStdout(t, func() error { return runCredentialList(nil, nil) })

	if got := firstLine(out); got != wantCredentialBanner {
		t.Errorf("credential list: first output line = %q, want %q\nfull output:\n%s", got, wantCredentialBanner, out)
	}
	// The banner must be a prefix, not a replacement: the table still renders.
	if !strings.Contains(out, "github") || !strings.Contains(out, "not_connected") {
		t.Errorf("credential list: table body missing after banner:\n%s", out)
	}
}

// The banner is unconditional (§12 says "output begins with"), so it precedes
// the empty-state message as well.
func TestCredentialList_EmptyOutputBeginsWithStoredNotInjected(t *testing.T) {
	newCredentialWordingServer(t, `{"credentials":[]}`)

	out := captureCredentialStdout(t, func() error { return runCredentialList(nil, nil) })

	if got := firstLine(out); got != wantCredentialBanner {
		t.Errorf("credential list (empty): first output line = %q, want %q\nfull output:\n%s", got, wantCredentialBanner, out)
	}
	if !strings.Contains(out, "No brokered upstreams") {
		t.Errorf("credential list (empty): empty-state line missing after banner:\n%s", out)
	}
}

// FR-034 / contracts/rest-endpoints.md §12: `mcpproxy credential status <server>`
// output begins with the same line; the REST status vocabulary
// (connected|expired|not_connected|unavailable) is unchanged in the body.
func TestCredentialStatus_OutputBeginsWithStoredNotInjected(t *testing.T) {
	newCredentialWordingServer(t, credentialWordingFixture)

	out := captureCredentialStdout(t, func() error { return runCredentialStatus(nil, []string{"github"}) })

	if got := firstLine(out); got != wantCredentialBanner {
		t.Errorf("credential status: first output line = %q, want %q\nfull output:\n%s", got, wantCredentialBanner, out)
	}
	for _, want := range []string{"Server:", "github", "Status:", "connected"} {
		if !strings.Contains(out, want) {
			t.Errorf("credential status: detail body missing %q after banner:\n%s", want, out)
		}
	}
}
