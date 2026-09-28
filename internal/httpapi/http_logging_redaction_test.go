package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// SEC-01: the access logger writes every request's raw query string and Referer
// to ~/Library/Logs/mcpproxy/http.log. `?apikey=` is an accepted credential
// source (Server.resolveAuth), and the Web UI SSE stream plus the tray client
// both send the ROOT admin key that way, so the credential was landing at rest
// in a plaintext log file.
//
// The middleware only touches s.httpLogger, so no router or live server is
// needed here.
const sec01Secret = "SUPERSECRETKEY0123456789abcdef01"

func newLoggingMiddlewareHarness(t *testing.T) (http.Handler, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	srv := &Server{httpLogger: zap.New(core)}
	h := srv.httpLoggingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return h, logs
}

// renderedFields flattens one observed entry into a single string holding the
// message and every field value, which is what actually reaches the log file.
func renderedFields(t *testing.T, logs *observer.ObservedLogs) (string, map[string]string) {
	t.Helper()
	all := logs.All()
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 log entry, got %d", len(all))
	}
	entry := all[0]
	var sb strings.Builder
	sb.WriteString(entry.Message)
	fields := map[string]string{}
	for k, v := range entry.ContextMap() {
		sb.WriteString(" ")
		s := fmt.Sprintf("%v", v)
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(s)
		fields[k] = s
	}
	return sb.String(), fields
}

func TestHTTPLoggingMiddleware_RedactsCredentialQuery(t *testing.T) {
	h, logs := newLoggingMiddlewareHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/events?apikey="+sec01Secret+"&foo=bar", nil)
	req.Header.Set("Referer", "http://127.0.0.1:8080/ui/?apikey="+sec01Secret)
	h.ServeHTTP(httptest.NewRecorder(), req)

	rendered, fields := renderedFields(t, logs)
	if strings.Contains(rendered, sec01Secret) {
		t.Fatalf("SEC-01: raw API key reached the http log line: %s", rendered)
	}
	if !strings.Contains(fields["query"], "foo=bar") {
		t.Fatalf("non-sensitive query parameter must survive verbatim, got %q", fields["query"])
	}
	if !strings.Contains(fields["query"], "apikey") {
		t.Fatalf("the parameter NAME is the diagnostic value of the line, got %q", fields["query"])
	}
}

// codex round 1 finding 1: a Referer is entirely client-controlled and does not
// have to parse. Before the fix, one bad escape in its path made the redactor
// fall back to a regex that has no rule for `apikey`, so the 64-hex admin key
// reached http.log verbatim.
func TestHTTPLoggingMiddleware_RedactsMalformedReferer(t *testing.T) {
	const adminKey = "6930184070f362383e7cddbd1184b3b8ed66f84717eedd8906ab8743dfa746cd"

	for _, referer := range []string{
		"http://127.0.0.1:8080/%zz?apikey=" + adminKey,
		"http://127.0.0.1:8080/ui%/?apikey=" + adminKey,
		"http://127.0.0.1:8080/ui/?apikey=" + adminKey,
	} {
		h, logs := newLoggingMiddlewareHarness(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
		req.Header.Set("Referer", referer)
		h.ServeHTTP(httptest.NewRecorder(), req)

		rendered, _ := renderedFields(t, logs)
		if strings.Contains(rendered, adminKey) {
			t.Fatalf("admin key reached the log line via Referer %q: %s", referer, rendered)
		}
	}
}

func TestHTTPLoggingMiddleware_RedactionTable(t *testing.T) {
	cases := []struct {
		name       string
		rawQuery   string
		mustSurive []string
	}{
		{name: "empty query", rawQuery: "", mustSurive: nil},
		{name: "unparseable query", rawQuery: "%zz&apikey=" + sec01Secret},
		{name: "token param", rawQuery: "token=" + sec01Secret},
		{name: "key param", rawQuery: "key=" + sec01Secret + "&page=2", mustSurive: []string{"page=2"}},
		{name: "api_key param", rawQuery: "api_key=" + sec01Secret},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := newLoggingMiddlewareHarness(t)
			target := "/api/v1/servers"
			if tc.rawQuery != "" {
				target += "?" + tc.rawQuery
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)

			rendered, fields := renderedFields(t, logs)
			if strings.Contains(rendered, sec01Secret) {
				t.Fatalf("raw credential reached the log line: %s", rendered)
			}
			if tc.rawQuery == "" && fields["query"] != "" {
				t.Fatalf("empty query must stay empty, got %q", fields["query"])
			}
			for _, want := range tc.mustSurive {
				if !strings.Contains(fields["query"], want) {
					t.Fatalf("expected %q to survive in %q", want, fields["query"])
				}
			}
		})
	}
}
