package core

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Covers the production enrichment path the stdio premature-exit branch in
// connectAndInitialize() calls (MCP-1093 / #599): the error a subprocess that
// exits before the MCP initialize handshake produces must carry the captured
// stderr tail (the actionable cause) and must still wrap the original cause.
func TestEnrichTransportClosedError_WithStderr(t *testing.T) {
	cause := io.EOF
	got := enrichTransportClosedError("  | Error: --brave-api-key is required", cause)
	msg := got.Error()

	if !strings.Contains(msg, "exited before completing the MCP initialize handshake") {
		t.Errorf("missing handshake phrase: %q", msg)
	}
	if !strings.Contains(msg, "recent stderr") || !strings.Contains(msg, "brave-api-key is required") {
		t.Errorf("enriched error must carry the captured stderr tail: %q", msg)
	}
	if !errors.Is(got, cause) {
		t.Errorf("enriched error must wrap the original cause for errors.Is/As")
	}
}

// The stdio premature-exit enrichment must apply ONLY to the stdio transport —
// HTTP/SSE share initialize() but have no subprocess, so a closed-transport error
// there must keep generic diagnostics. (Codex review on PR #606)
func TestShouldEnrichStdioPrematureExit_GatedToStdio(t *testing.T) {
	closed := errors.New("transport error: transport closed")
	cases := []struct {
		transport string
		err       error
		want      bool
	}{
		{transportStdio, closed, true},
		{transportStdio, io.EOF, true},
		{transportHTTP, closed, false},
		{transportSSE, closed, false},
		{transportStdio, errors.New("some unrelated error"), false},
	}
	for _, tc := range cases {
		if got := shouldEnrichStdioPrematureExit(tc.transport, tc.err); got != tc.want {
			t.Errorf("shouldEnrichStdioPrematureExit(%q, %v) = %v, want %v", tc.transport, tc.err, got, tc.want)
		}
	}
}

func TestEnrichTransportClosedError_NoStderr(t *testing.T) {
	cause := errors.New("transport closed")
	got := enrichTransportClosedError("", cause)
	msg := got.Error()

	if !strings.Contains(msg, "produced no stderr output") {
		t.Errorf("no-stderr branch must say so: %q", msg)
	}
	if strings.Contains(msg, "recent stderr") {
		t.Errorf("must not mention recent stderr when none was captured: %q", msg)
	}
	if !errors.Is(got, cause) {
		t.Errorf("enriched error must wrap the original cause")
	}
}
