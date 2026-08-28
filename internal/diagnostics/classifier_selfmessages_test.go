package diagnostics

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
)

// Both errors below reached the user as MCPX_UNKNOWN_UNCLASSIFIED, whose catalog
// entry tells them to file a bug report against us — for failures that are
// entirely diagnosable. The strings are copied verbatim from a live v0.61.0
// install.
//
// Only the first is mcpproxy's own wording (internal/upstream/core/
// connection_stdio.go). The second is mcp-go's exported `transport
// .ErrLegacySSEServer` sentinel; see TestLegacySSESentinelStillMatches for why
// that distinction needs its own guard.
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

// The legacy-SSE rule matches a string mcp-go owns, not one of ours. Nothing
// else in this repo produces it:
//
//	$ grep -rn "legacy SSE" --include="*.go" internal/ | grep -v _test
//	internal/diagnostics/classifier.go:...   (the matcher itself)
//
// So a routine mcp-go bump that rewords the sentinel would silently regress
// MCPX_HTTP_LEGACY_SSE back to MCPX_HTTP_404 with the whole suite green — the
// user gets sent hunting for a credential problem again. The typed errors.Is
// path in classifyHTTP survives a reword; the substring fallback (needed
// because the upstream layer stringifies the error) does not. This pins it, so
// the bump fails HERE with a clear reason instead of degrading in the field.
func TestLegacySSESentinelStillMatches(t *testing.T) {
	msg := strings.ToLower(transport.ErrLegacySSEServer.Error())

	matchesWording := strings.Contains(msg, "likely a legacy sse server") ||
		(strings.Contains(msg, "4xx for initialize") && strings.Contains(msg, "post"))
	if !matchesWording {
		t.Fatalf("mcp-go reworded ErrLegacySSEServer to %q — update the substring fallback "+
			"in classifyHTTP to match, or MCPX_HTTP_LEGACY_SSE silently degrades to MCPX_HTTP_404",
			transport.ErrLegacySSEServer.Error())
	}

	// The typed path must work even when nothing about the wording matches.
	if got := Classify(fmt.Errorf("connect: %w", transport.ErrLegacySSEServer),
		ClassifierHints{Transport: "http"}); got != HTTPLegacySSE {
		t.Fatalf("wrapped sentinel classified as %q, want %q", got, HTTPLegacySSE)
	}
}
