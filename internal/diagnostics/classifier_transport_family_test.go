package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

// httpFamilyTransports is every string that can reach ClassifierHints.Transport
// for a URL-configured server.
//
// transport.DetermineTransportType returns config.Protocol VERBATIM when it is
// set, and "streamable-http" when it is not — and config validation accepts
// "http", "sse", "streamable-http" and "auto" (internal/config/config.go:2465).
// Both spellings are live in the field: the registry add path, the Claude Code
// and Gemini config importers and the Add-Server modal write "http", while a
// URL server added with no explicit protocol resolves to "streamable-http".
//
// Before this test the classifier's HTTP arms were gated on the exact string
// "http", so the SAME failure classified differently depending on which of
// those spellings the server happened to carry — three of the four columns
// below fell through to MCPX_UNKNOWN_UNCLASSIFIED.
var httpFamilyTransports = []string{"http", "sse", "streamable-http", "auto"}

func TestClassify_HTTPFamilyTransportsAgree(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Code
	}{
		{
			// mcp-go streamable_http.go / sse.go: "request failed with status %d: %s".
			name: "401 in status text",
			err:  errors.New(`transport error: request failed with status 401: Unauthorized`),
			want: HTTPUnauth,
		},
		{
			// The HTTP transport bubbles context.DeadlineExceeded up wrapped.
			name: "wrapped context deadline",
			err:  fmt.Errorf("post %q: %w", "https://example.invalid/mcp", context.DeadlineExceeded),
			want: HTTPTimeout,
		},
		{
			// mcp-go client/transport/sse.go:293 — the SSE CONNECT path words it
			// differently, and the digits do not follow "status ".
			name: "sse unexpected status code marker",
			err:  errors.New("unexpected status code: 502"),
			want: HTTPServerErr,
		},
		{
			name: "429 rate limited",
			err:  errors.New("transport error: request failed with status 429"),
			want: HTTPRateLimited,
		},
		{
			name: "connection reset by peer",
			err:  fmt.Errorf("failed to connect: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}),
			want: HTTPConnReset,
		},
	}

	for _, tc := range cases {
		for _, tr := range httpFamilyTransports {
			t.Run(tc.name+"/"+tr, func(t *testing.T) {
				if got := Classify(tc.err, ClassifierHints{Transport: tr}); got != tc.want {
					t.Errorf("Classify(%q, transport=%q) = %q, want %q", tc.err, tr, got, tc.want)
				}
			})
		}
	}
}

// TestClassify_HTTPFamilyGenericStatuses covers the statuses that previously had
// no code at all: they reached DiagnoseHTTPStatus, got "", and fell through to
// MCPX_UNKNOWN_UNCLASSIFIED even on the "http" spelling.
func TestClassify_HTTPFamilyGenericStatuses(t *testing.T) {
	cases := map[string]Code{
		`transport error: request failed with status 400: Bad Request`:      HTTPClientErr,
		`transport error: request failed with status 408: Request Timeout`:  HTTPClientErr,
		`transport error: request failed with status 409: Conflict`:         HTTPClientErr,
		`transport error: request failed with status 410: Gone`:             HTTPClientErr,
		`transport error: request failed with status 451: Unavailable`:      HTTPClientErr,
		`notification failed with status 429: slow down`:                    HTTPRateLimited,
		`transport error: request failed with status 402: Payment Required`: UnknownUnclassified, // deliberately unmapped
	}
	for msg, want := range cases {
		for _, tr := range httpFamilyTransports {
			if got := Classify(errors.New(msg), ClassifierHints{Transport: tr}); got != want {
				t.Errorf("Classify(%q, transport=%q) = %q, want %q", msg, tr, got, want)
			}
		}
	}
}

// TestClassify_TypedTimeoutAndCancel pins the two typed shapes that are NOT
// context.DeadlineExceeded and so were invisible to the existing arms: an
// i/o timeout on the socket (any net.Error reporting Timeout), and a deliberate
// cancellation, which is not a fault and must not read as one.
func TestClassify_TypedTimeoutAndCancel(t *testing.T) {
	ioTimeout := fmt.Errorf("failed to send request: %w",
		&net.OpError{Op: "read", Net: "tcp", Err: &timeoutErr{}})
	for _, tr := range httpFamilyTransports {
		if got := Classify(ioTimeout, ClassifierHints{Transport: tr}); got != HTTPTimeout {
			t.Errorf("Classify(i/o timeout, transport=%q) = %q, want %q", tr, got, HTTPTimeout)
		}
		canceled := fmt.Errorf("connect aborted: %w", context.Canceled)
		if got := Classify(canceled, ClassifierHints{Transport: tr}); got != HTTPCanceled {
			t.Errorf("Classify(canceled, transport=%q) = %q, want %q", tr, got, HTTPCanceled)
		}
	}
	entry, ok := Get(HTTPCanceled)
	if !ok {
		t.Fatalf("%q is not registered in the catalog", HTTPCanceled)
	}
	if entry.Severity != SeverityInfo {
		t.Errorf("%q severity = %q, want %q — a cancellation is not a fault", HTTPCanceled, entry.Severity, SeverityInfo)
	}
}

// timeoutErr is a net.Error that reports Timeout()==true without being
// context.DeadlineExceeded — the shape `read tcp …: i/o timeout` and an
// http.Client deadline both take.
type timeoutErr struct{}

func (*timeoutErr) Error() string { return "i/o timeout" }
func (*timeoutErr) Timeout() bool { return true }
func (*timeoutErr) Temporary() bool {
	return true
}

// TestClassify_OAuthKeepsPrecedenceOverHTTPStatus guards the bucket this change
// could plausibly have stolen from. Unlocking the HTTP status arms for "sse" and
// "streamable-http" moves them AHEAD of classifyOAuth for those spellings, so
// the OAuth user-states have to keep winning on their own strength:
//
//   - typed: ErrOAuthPending carries a Code(), and Classify's typed fast path
//     runs before every classifier — including this one. The wrappers on the way
//     out ("all authentication strategies failed, last error: %w") use %w, so
//     the chain survives.
//   - stringified: the two ErrOAuthPending messages carry no status number, so
//     the status-text arm cannot match them even once it is switched on.
//
// A sign-in prompt misreported as MCPX_HTTP_401 would send the user hunting for
// a credential bug instead of clicking "log in".
func TestClassify_OAuthKeepsPrecedenceOverHTTPStatus(t *testing.T) {
	typed := fmt.Errorf("all authentication strategies failed, last error: %w",
		codedStub{msg: "OAuth authentication required for slack: server returned status 401", code: OAuthLoginRequired})
	stringified := errors.New("OAuth authentication required for github: login available via Web UI, system tray menu, or 'mcpproxy auth login' CLI command")

	for _, tr := range httpFamilyTransports {
		if got := Classify(typed, ClassifierHints{Transport: tr}); got != OAuthLoginRequired {
			t.Errorf("Classify(typed oauth, transport=%q) = %q, want %q", tr, got, OAuthLoginRequired)
		}
		if got := Classify(stringified, ClassifierHints{Transport: tr}); got != OAuthLoginRequired {
			t.Errorf("Classify(stringified oauth, transport=%q) = %q, want %q", tr, got, OAuthLoginRequired)
		}
	}
}

// TestClassify_LegacySSEKeepsPrecedence — the legacy-SSE arm sits ahead of the
// status-text fallback on purpose. Widening the family must not let a bare 4xx
// reading claim an error that already identified itself as a transport
// mismatch.
func TestClassify_LegacySSEKeepsPrecedence(t *testing.T) {
	err := errors.New("transport error: request failed with status 404: likely a legacy SSE server")
	for _, tr := range httpFamilyTransports {
		if got := Classify(err, ClassifierHints{Transport: tr}); got != HTTPLegacySSE {
			t.Errorf("Classify(legacy sse, transport=%q) = %q, want %q", tr, got, HTTPLegacySSE)
		}
	}
}

// TestClassify_StdioTransportUnaffected guards the blast radius: folding the
// HTTP spellings together must not fold stdio in with them. A stdio server's
// failures keep their STDIO codes, and the docker-isolation arms still win.
func TestClassify_StdioTransportUnaffected(t *testing.T) {
	cases := []struct {
		err   error
		hints ClassifierHints
		want  Code
	}{
		{errors.New("failed to start: transport closed"), ClassifierHints{Transport: "stdio"}, STDIOExitBeforeInitialize},
		{fmt.Errorf("handshake: %w", context.DeadlineExceeded), ClassifierHints{Transport: "stdio"}, STDIOHandshakeTimeout},
		{errors.New("exec: \"npx\": executable file not found in $PATH"), ClassifierHints{Transport: "stdio"}, STDIOSpawnENOENT},
	}
	for _, tc := range cases {
		if got := Classify(tc.err, tc.hints); got != tc.want {
			t.Errorf("Classify(%q, %+v) = %q, want %q", tc.err, tc.hints, got, tc.want)
		}
	}
}

// TestCanonicalTransport pins the family membership itself, including the
// pass-through for stdio: the mapping is the single place the classifier and
// hints.For agree on what "HTTP" means.
func TestCanonicalTransport(t *testing.T) {
	http := map[string]bool{
		"http": true, "https": true, "sse": true, "streamable-http": true,
		"streamable_http": true, "auto": true, "": true, "Streamable-HTTP": true,
	}
	for in := range http {
		if got := CanonicalTransport(in); got != TransportHTTP {
			t.Errorf("CanonicalTransport(%q) = %q, want %q", in, got, TransportHTTP)
		}
	}
	for _, in := range []string{"stdio", "STDIO", "docker"} {
		if got := CanonicalTransport(in); got == TransportHTTP {
			t.Errorf("CanonicalTransport(%q) = %q, must not join the HTTP family", in, got)
		}
	}
	if got := CanonicalTransport("STDIO"); got != TransportStdio {
		t.Errorf("CanonicalTransport(%q) = %q, want %q", "STDIO", got, TransportStdio)
	}
}
