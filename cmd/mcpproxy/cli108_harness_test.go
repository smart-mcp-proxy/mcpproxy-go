package main

// Shared harness for the Spec 108-g CLI tests: a recording httptest daemon that
// answers canned REST envelopes, so each test asserts both the request the CLI
// sends (method, path, query, body) and what it prints.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type recordedRequest struct {
	Method   string
	Path     string
	RawQuery string
	Body     string
	Surface  string
}

// jsonBody decodes the recorded body into a generic map.
func (r recordedRequest) jsonBody(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.Body), &m), r.Body)
	return m
}

type cannedResponse struct {
	Status int
	Body   string
}

// restRecorder is the fake daemon. Routes are keyed "METHOD /path".
type restRecorder struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]cannedResponse
	// sequences answer a route's successive calls in order; the last entry
	// repeats.
	sequences map[string][]cannedResponse
	requests  []recordedRequest
}

// newRESTRecorder starts the fake daemon and points the CLI at it.
func newRESTRecorder(t *testing.T, routes map[string]cannedResponse) *restRecorder {
	t.Helper()
	rec := &restRecorder{t: t, routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.requests = append(rec.requests, recordedRequest{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Body: string(body), Surface: r.Header.Get("X-MCPProxy-Surface")})
		key := r.Method + " " + r.URL.Path
		resp, found := rec.routes[key]
		if seq := rec.sequences[key]; len(seq) > 0 {
			resp, found = seq[0], true
			if len(seq) > 1 {
				rec.sequences[key] = seq[1:]
			}
		}
		rec.mu.Unlock()
		if !found {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"error":"not found"}`))
			return
		}
		if resp.Status == 0 {
			resp.Status = http.StatusOK
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write([]byte(resp.Body))
	}))
	t.Cleanup(srv.Close)
	withClientDaemon(t, srv.URL)
	return rec
}

// newSequencedRecorder answers one route with a sequence of canned responses.
func newSequencedRecorder(t *testing.T, key string, seq []cannedResponse) *restRecorder {
	t.Helper()
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	rec.mu.Lock()
	rec.sequences = map[string][]cannedResponse{key: seq}
	rec.mu.Unlock()
	return rec
}

func (r *restRecorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

// only returns the single recorded request, failing when there is not exactly one.
func (r *restRecorder) only(t *testing.T) recordedRequest {
	t.Helper()
	all := r.all()
	require.Len(t, all, 1, "expected exactly one request, got %+v", all)
	return all[0]
}

func okResp(data string) cannedResponse {
	return cannedResponse{http.StatusOK, `{"success":true,"data":` + data + `}`}
}
func createdResp(data string) cannedResponse {
	return cannedResponse{http.StatusCreated, `{"success":true,"data":` + data + `}`}
}
func refuse(status int, body string) cannedResponse { return cannedResponse{status, body} }

// captureStd runs f with stdout and stderr captured.
func captureStd(f func()) (stdout, stderrOut string) {
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	outC, errC := make(chan string), make(chan string)
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, ro); outC <- b.String() }()
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, re); errC <- b.String() }()
	func() {
		defer func() {
			_ = wo.Close()
			_ = we.Close()
			os.Stdout, os.Stderr = oldOut, oldErr
		}()
		f()
	}()
	return <-outC, <-errC
}

// runCLI executes a command built by factory with args, returning stdout, stderr
// and the command error. Output globals are set to the given format.
func runCLI(t *testing.T, factory func() *cobra.Command, format string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("MCPPROXY_OUTPUT", "")
	setOutputGlobals(t, format, false)
	var err error
	out, errOut := captureStd(func() {
		cmd := factory()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		err = cmd.Execute()
	})
	return out, errOut, err
}

// assertGolden108 compares got with testdata/cli108/<name>; MCPPROXY_UPDATE_GOLDEN=1
// rewrites it.
func assertGolden108(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "cli108", name)
	if os.Getenv("MCPPROXY_UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden %s missing; actual:\n%s", name, got)
	norm := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	require.Equal(t, norm(string(want)), norm(got))
}

// mustJSON marshals a value for a canned body.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
