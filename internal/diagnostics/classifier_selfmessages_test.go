package diagnostics

import (
	"errors"
	"testing"
)

// Both errors below are mcpproxy's OWN wording, produced before or during the
// connect attempt — yet both classified as MCPX_UNKNOWN_UNCLASSIFIED, whose
// catalog entry tells the user to file a bug report against us. The strings are
// copied verbatim from a live v0.61.0 install.
func TestOwnErrorMessagesDoNotClassifyAsUnknown(t *testing.T) {
	cases := []struct {
		name  string
		err   string
		hints ClassifierHints
		want  Code
	}{
		{
			name:  "package runner with no package to run",
			err:   `failed to connect: server "demo-filesystem": command "npx" has no args — the npm package to run (e.g. ["-y", "some-mcp-server"]) is required`,
			hints: ClassifierHints{Transport: "stdio"},
			want:  ConfigInvalidCommand,
		},
		{
			name:  "uvx variant of the same validation failure",
			err:   `server "x": command "uvx" has no args — the Python package to run is required`,
			hints: ClassifierHints{Transport: "stdio"},
			want:  ConfigInvalidCommand,
		},
		{
			name:  "streamable-HTTP handshake refused by a legacy SSE server",
			err:   "failed to connect: MCP initialize failed during no-auth strategy: transport error: server returned 4xx for initialize POST, likely a legacy SSE server",
			hints: ClassifierHints{Transport: "http"},
			want:  HTTPLegacySSE,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(errors.New(tc.err), tc.hints)
			if got != tc.want {
				t.Fatalf("Classify() = %q, want %q", got, tc.want)
			}
			if got == UnknownUnclassified {
				t.Fatal("mcpproxy's own error message must never ask the user to file a bug")
			}
		})
	}
}

// The legacy-SSE match must win over the generic HTTP status-text fallback:
// reading it as a bare 403/404 sends the user hunting for a credential problem
// that does not exist.
func TestLegacySSEBeatsGenericStatusText(t *testing.T) {
	err := errors.New("transport error: request failed with status 404: server returned 4xx for initialize POST, likely a legacy SSE server")
	if got := Classify(err, ClassifierHints{Transport: "http"}); got != HTTPLegacySSE {
		t.Fatalf("Classify() = %q, want %q", got, HTTPLegacySSE)
	}
}

// Every code the classifier can return must be registered, or the API emits a
// diagnostic with no user message, no fix steps and no docs URL.
func TestNewCodesAreRegistered(t *testing.T) {
	for _, c := range []Code{ConfigInvalidCommand, HTTPLegacySSE} {
		entry, ok := Get(c)
		if !ok {
			t.Fatalf("%s is not registered in the catalog", c)
		}
		if entry.UserMessage == "" {
			t.Errorf("%s has no user message", c)
		}
		if len(entry.FixSteps) == 0 {
			t.Errorf("%s has no fix steps", c)
		}
		for _, step := range entry.FixSteps {
			if step.Type == FixStepLink && step.URL == "" {
				t.Errorf("%s has a link fix step with no URL", c)
			}
		}
	}
}

// A genuine 404 with no legacy-SSE marker must still classify as a plain 404 —
// the new rule must not swallow the generic HTTP status path.
func TestOrdinaryHTTPStatusesAreUnaffected(t *testing.T) {
	err := errors.New("transport error: request failed with status 404: not found")
	if got := Classify(err, ClassifierHints{Transport: "http"}); got != HTTPNotFound {
		t.Fatalf("Classify() = %q, want %q", got, HTTPNotFound)
	}
}
