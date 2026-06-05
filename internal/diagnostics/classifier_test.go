package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestClassify_Nil(t *testing.T) {
	if got := Classify(nil, ClassifierHints{}); got != "" {
		t.Errorf("Classify(nil) = %q, want empty", got)
	}
}

// TestClassify_STDIO_ExitBeforeInitialize covers GitHub #599 / MCP-1093: a
// subprocess that exits before completing the MCP initialize handshake must
// classify to MCPX_STDIO_EXIT_BEFORE_INITIALIZE (not MCPX_UNKNOWN_UNCLASSIFIED),
// and the surfaced error must carry the child's exit code + stderr tail.
func TestClassify_STDIO_ExitBeforeInitialize(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "raw transport-closed under stdio hint",
			err:  errors.New(`stdio transport (command="docker", docker_isolation=true): transport error: transport closed`),
		},
		{
			name: "enriched message carrying exit code and stderr tail",
			err: errors.New("server process exited before completing the MCP initialize handshake (exit code 127); recent stderr:\n" +
				"  | Error: --brave-api-key is required: transport closed"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err, ClassifierHints{Transport: "stdio"})
			if got != STDIOExitBeforeInitialize {
				t.Fatalf("Classify(%q) = %q, want %q", tc.err, got, STDIOExitBeforeInitialize)
			}
		})
	}

	// The enriched error the backend produces must carry the exit code and the
	// actionable stderr line so the UI banner and per-server logs show the real
	// cause instead of telling the user to file a bug.
	enriched := "server process exited before completing the MCP initialize handshake (exit code 127); recent stderr:\n  | Error: --brave-api-key is required"
	if !strings.Contains(enriched, "exit code 127") || !strings.Contains(enriched, "brave-api-key is required") {
		t.Fatalf("enriched error must carry exit code + stderr tail, got: %q", enriched)
	}
}

// TestClassify_STDIO_ExitBeforeInitialize_NotForHTTP guards against over-match:
// the same "transport closed" wording on a non-stdio transport must not be
// captured by the stdio rule.
func TestClassify_STDIO_ExitBeforeInitialize_NotForHTTP(t *testing.T) {
	err := errors.New("transport error: transport closed")
	if got := Classify(err, ClassifierHints{Transport: "http"}); got == STDIOExitBeforeInitialize {
		t.Fatalf("HTTP transport must not classify as %q", STDIOExitBeforeInitialize)
	}
}

func TestClassify_STDIO_SpawnENOENT(t *testing.T) {
	// os/exec wraps ENOENT into *exec.Error when the binary is missing.
	wrapped := &exec.Error{
		Name: "/nonexistent/does-not-exist",
		Err:  syscall.ENOENT,
	}
	got := Classify(wrapped, ClassifierHints{Transport: "stdio"})
	if got != STDIOSpawnENOENT {
		t.Errorf("Classify(enoent) = %q, want %q", got, STDIOSpawnENOENT)
	}
}

func TestClassify_STDIO_SpawnEACCES(t *testing.T) {
	wrapped := &exec.Error{
		Name: "/tmp/not-executable",
		Err:  syscall.EACCES,
	}
	got := Classify(wrapped, ClassifierHints{Transport: "stdio"})
	if got != STDIOSpawnEACCES {
		t.Errorf("Classify(eacces) = %q, want %q", got, STDIOSpawnEACCES)
	}
}

func TestClassify_STDIO_HandshakeTimeout(t *testing.T) {
	err := fmt.Errorf("handshake: %w", context.DeadlineExceeded)
	got := Classify(err, ClassifierHints{Transport: "stdio"})
	if got != STDIOHandshakeTimeout {
		t.Errorf("Classify(timeout) = %q, want %q", got, STDIOHandshakeTimeout)
	}
}

func TestClassify_HTTP_DNSFailed(t *testing.T) {
	err := &net.DNSError{Name: "nope.invalid", Err: "no such host"}
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPDNSFailed {
		t.Errorf("Classify(dns) = %q, want %q", got, HTTPDNSFailed)
	}
}

func TestClassify_HTTP_TLSFailed(t *testing.T) {
	err := errors.New("x509: certificate signed by unknown authority")
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPTLSFailed {
		t.Errorf("Classify(tls) = %q, want %q", got, HTTPTLSFailed)
	}
}

func TestClassify_HTTP_ConnRefused(t *testing.T) {
	err := fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPConnRefuse {
		t.Errorf("Classify(connrefuse) = %q, want %q", got, HTTPConnRefuse)
	}
}

func TestClassify_HTTP_Timeout(t *testing.T) {
	// Real upstream timeout — the http transport wraps context.DeadlineExceeded
	// with the operation name. Must classify as MCPX_HTTP_TIMEOUT, not
	// MCPX_UNKNOWN_UNCLASSIFIED.
	err := fmt.Errorf("post %q: %w", "https://hf.co/mcp", context.DeadlineExceeded)
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPTimeout {
		t.Errorf("Classify(timeout) = %q, want %q", got, HTTPTimeout)
	}
}

func TestClassify_HTTP_TimeoutStringWrapped(t *testing.T) {
	// The upstream manager often re-wraps as a plain string ("transport error: ...
	// context deadline exceeded"). The typed errors.Is path can't see through that,
	// so the classifier must also catch the substring on the http transport hint.
	err := errors.New(`failed to list tools: transport error: failed to send request: failed to send request: Post "https://hf.co/mcp": context deadline exceeded`)
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPTimeout {
		t.Errorf("Classify(string-wrapped timeout) = %q, want %q", got, HTTPTimeout)
	}
}

func TestClassify_HTTP_StatusFromText(t *testing.T) {
	// 5xx responses arrive at the classifier as a plain string from the
	// upstream layer (the typed statusError path is bypassed by the wrapping).
	// Must map to HTTPServerErr / HTTPUnauth / etc. instead of UNCLASSIFIED.
	cases := []struct {
		err  string
		want Code
	}{
		{`transport error: request failed with status 504: <html><body>504</body></html>`, HTTPServerErr},
		{`transport error: request failed with status 502 Bad Gateway`, HTTPServerErr},
		{`failed to send initialized notification: notification failed with status 504: <html>...`, HTTPServerErr},
		{`transport error: request failed with status 401`, HTTPUnauth},
		{`transport error: request failed with status 403 Forbidden`, HTTPForbidden},
		{`request failed with status 404`, HTTPNotFound},
	}
	for _, tc := range cases {
		got := Classify(errors.New(tc.err), ClassifierHints{Transport: "http"})
		if got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestClassify_NetworkOffline(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}
	got := Classify(err, ClassifierHints{})
	if got != NetworkOffline {
		t.Errorf("Classify(netunreach) = %q, want %q", got, NetworkOffline)
	}
}

func TestClassify_Fallback(t *testing.T) {
	err := errors.New("something we don't know about")
	got := Classify(err, ClassifierHints{})
	if got != UnknownUnclassified {
		t.Errorf("Classify(unknown) = %q, want %q", got, UnknownUnclassified)
	}
}

func TestDiagnoseHTTPStatus(t *testing.T) {
	cases := map[int]Code{
		200: "",
		401: HTTPUnauth,
		403: HTTPForbidden,
		404: HTTPNotFound,
		500: HTTPServerErr,
		502: HTTPServerErr,
		599: HTTPServerErr,
	}
	for status, want := range cases {
		got := DiagnoseHTTPStatus(status)
		if got != want {
			t.Errorf("DiagnoseHTTPStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestFixerRegistry(t *testing.T) {
	Register("test_fixer", func(_ context.Context, req FixRequest) (FixResult, error) {
		return FixResult{Outcome: OutcomeSuccess, Preview: "preview for " + req.ServerID}, nil
	})
	res, err := InvokeFixer(context.Background(), "test_fixer", FixRequest{ServerID: "s1", Mode: ModeDryRun})
	if err != nil {
		t.Fatalf("InvokeFixer returned error: %v", err)
	}
	if res.Outcome != OutcomeSuccess {
		t.Errorf("outcome = %q, want success", res.Outcome)
	}
	if res.Preview == "" {
		t.Errorf("preview is empty")
	}

	// Unknown fixer returns ErrUnknownFixer with blocked outcome.
	res, err = InvokeFixer(context.Background(), "does-not-exist", FixRequest{})
	if !errors.Is(err, ErrUnknownFixer) {
		t.Errorf("expected ErrUnknownFixer, got %v", err)
	}
	if res.Outcome != OutcomeBlocked {
		t.Errorf("outcome = %q, want blocked", res.Outcome)
	}
}
