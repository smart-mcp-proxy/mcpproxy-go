package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 117 PR-2 (#1566, T215): `mcpproxy review approve` binds the approval
// to the review the operator saw.

// bindingDaemon serves one review per server whose fingerprints the test can
// move (simulating an upstream change), and answers an approval whose
// expected_hashes do not match with the core's 409 review_out_of_date.
type bindingDaemon struct {
	mu       sync.Mutex
	reviews  map[string]string // server -> review data JSON
	hashes   map[string]map[string]string
	requests []reviewRequest
}

func (d *bindingDaemon) setReview(server string, quarantined bool, tools ...[3]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reviews == nil {
		d.reviews, d.hashes = map[string]string{}, map[string]map[string]string{}
	}
	parts := make([]string, 0, len(tools))
	d.hashes[server] = map[string]string{}
	for _, t := range tools { // name, status, hash
		parts = append(parts, fmt.Sprintf(`{"name":%q,"tier":"read","approval_status":%q,"description":"Does %s\nsecond line","default_allowed":true,"current_hash":%q}`, t[0], t[1], t[0], t[2]))
		d.hashes[server][t[0]] = t[2]
	}
	d.reviews[server] = fmt.Sprintf(`{"server":{"name":%q,"quarantined":%t,"definitions_captured":%t},"tools":[%s]}`, server, quarantined, len(tools) > 0, strings.Join(parts, ","))
}

func (d *bindingDaemon) posts() []reviewRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []reviewRequest
	for _, r := range d.requests {
		if r.method == http.MethodPost {
			out = append(out, r)
		}
	}
	return out
}

func (d *bindingDaemon) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.requests = append(d.requests, reviewRequest{method: r.Method, path: r.URL.Path, body: string(body)})
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/servers/"), "/")
		server := parts[0]
		d.mu.Lock()
		review, ok := d.reviews[server]
		current := d.hashes[server]
		d.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"error":"server not found"}`))
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"success":true,"data":` + review + `}`))
			return
		}
		var req struct {
			ExpectedHashes map[string]string `json:"expected_hashes"`
			Block          []string          `json:"block"`
		}
		_ = json.Unmarshal(body, &req)
		if req.ExpectedHashes != nil {
			var changed []string
			// The stub checks only the bound names (the real core validates
			// the whole reachable set).
			for name, want := range req.ExpectedHashes {
				if h, ok := current[name]; ok && want != h && !contains(req.Block, name) {
					changed = append(changed, name)
				}
			}
			if len(changed) > 0 {
				w.WriteHeader(http.StatusConflict)
				_, _ = fmt.Fprintf(w, `{"success":false,"error":"tool review for server '%s' is out of date (definition changed since review: %s); nothing was approved — fetch the review again","error_code":"review_out_of_date","stale_tools":{"changed":[%q],"unreviewed":[],"missing_blocks":[]}}`, server, strings.Join(changed, ", "), changed[0])
				return
			}
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"status":"approved","server_name":"` + server + `","message":"Approved"}}`))
	}))
	t.Cleanup(srv.Close)
	withReviewDaemon(t, srv.URL)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func lastBody(t *testing.T, d *bindingDaemon) map[string]any {
	t.Helper()
	posts := d.posts()
	require.NotEmpty(t, posts)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(posts[len(posts)-1].body), &body))
	return body
}

func TestReviewApproveBinding_QuarantinedSendsAllReviewedHashes(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("notes2", true, [3]string{"read_note", "pending", "rv1-h1"}, [3]string{"wipe", "pending", "rv1-w1"})
	d.start(t)
	_, err := runReviewApprove(t, "table", nil, "notes2", "--yes")
	require.NoError(t, err)
	body := lastBody(t, d)
	assert.Equal(t, map[string]any{"read_note": "rv1-h1", "wipe": "rv1-w1"}, body["expected_hashes"])
}

func TestReviewApproveBinding_EmptyReviewBindsEmptySnapshot(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("bare", true)
	d.start(t)
	_, err := runReviewApprove(t, "table", nil, "bare", "--yes")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, lastBody(t, d)["expected_hashes"])
}

func TestReviewApproveBinding_TrustedPaths(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("trusted2", false,
		[3]string{"a", "pending", "rv1-a"}, [3]string{"b", "changed", "rv1-b"}, [3]string{"c", "approved", "rv1-c"})
	d.start(t)

	_, err := runReviewApprove(t, "table", nil, "trusted2", "--tools", "a", "--yes")
	require.NoError(t, err)
	body := lastBody(t, d)
	assert.Equal(t, []any{"a"}, body["tools"])
	assert.Equal(t, map[string]any{"a": "rv1-a"}, body["expected_hashes"])

	_, err = runReviewApprove(t, "table", nil, "trusted2", "--yes")
	require.NoError(t, err)
	body = lastBody(t, d)
	assert.Equal(t, true, body["approve_all"])
	assert.Equal(t, map[string]any{"a": "rv1-a", "b": "rv1-b"}, body["expected_hashes"], "approve_all binds every held (pending/changed) tool")
}

// Failing-first (design review r2 #1): the saved review is H1, the server
// now serves H2; the approval carries H1 and the CLI exits 1 on the 409.
func TestReviewApproveBinding_FromReviewOutOfDate(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("notes2", true, [3]string{"read_note", "pending", "rv1-h1"})
	d.start(t)

	saved, err := runReviewShowJSON(t, "notes2")
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "r.json")
	require.NoError(t, os.WriteFile(file, []byte(saved), 0o600))

	d.setReview("notes2", true, [3]string{"read_note", "pending", "rv1-h2"})
	out, err := runReviewApprove(t, "table", nil, "notes2", "--from-review", file, "--yes")
	require.Error(t, err)
	assert.Equal(t, 1, classifyError(err))
	assert.Contains(t, err.Error(), "out of date")
	assert.Contains(t, err.Error(), "Run 'mcpproxy review show notes2' and approve again.")
	assert.NotContains(t, out, "Approved")
	body := lastBody(t, d)
	assert.Equal(t, map[string]any{"read_note": "rv1-h1"}, body["expected_hashes"], "bound to the saved review, not a fresh fetch")

	// -o json prints the 409 envelope.
	out, err = runReviewApprove(t, "json", nil, "notes2", "--from-review", file, "--yes")
	require.Error(t, err)
	var env map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &env), out)
	assert.Equal(t, "review_out_of_date", env["error_code"])
}

func TestReviewApproveBinding_FromReviewMustMatchServer(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("a", true, [3]string{"t", "pending", "rv1-a"})
	d.setReview("b", true, [3]string{"t", "pending", "rv1-a"})
	d.start(t)
	saved, err := runReviewShowJSON(t, "a")
	require.NoError(t, err)
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.json")
	require.NoError(t, os.WriteFile(fileA, []byte(saved), 0o600))
	before := len(d.posts())

	for name, path := range map[string]string{
		"other server":   fileA,
		"malformed":      writeTemp(t, dir, "bad.json", "{not json"),
		"error envelope": writeTemp(t, dir, "err.json", `{"success":false,"error":"server not found"}`),
		"no tools":       writeTemp(t, dir, "notools.json", `{"server":{"name":"b","quarantined":true}}`),
	} {
		_, err := runReviewApprove(t, "table", nil, "b", "--from-review", path, "--yes")
		require.Error(t, err, name)
		assert.Equal(t, 1, classifyError(err), name)
	}
	assert.Len(t, d.posts(), before, "no approve request is sent for a rejected review file")

	// The REST envelope of the same server is accepted.
	env := writeTemp(t, dir, "env.json", `{"success":true,"data":`+saved+`}`)
	_, err = runReviewApprove(t, "table", nil, "a", "--from-review", env, "--yes")
	require.NoError(t, err)
}

func TestReviewApproveBinding_PromptListsReachableTools(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("notes2", true, [3]string{"read_note", "pending", "rv1-h1"})
	d.start(t)
	var prompt string
	out, err := runReviewApprove(t, "table", func(message string) (bool, error) {
		prompt = message
		return false, nil
	}, "notes2")
	require.NoError(t, err)
	require.NotEmpty(t, prompt)
	assert.Contains(t, out, "read_note (read, pending): Does read_note")
	assert.NotContains(t, out, "second line")
	assert.Empty(t, d.posts())
}

// An older core reports no fingerprints: the request stays unbound.
func TestReviewApproveBinding_OlderCoreUnbound(t *testing.T) {
	d := &bindingDaemon{}
	d.setReview("old", true, [3]string{"t", "pending", ""})
	d.start(t)
	_, err := runReviewApprove(t, "table", nil, "old", "--yes")
	require.NoError(t, err)
	_, has := lastBody(t, d)["expected_hashes"]
	assert.False(t, has)
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func runReviewShowJSON(t *testing.T, server string) (string, error) {
	t.Helper()
	setOutputGlobals(t, "json", false)
	var runErr error
	out := captureReviewOutput(t, func() error {
		cmd := newReviewCommand(nil)
		cmd.SetArgs([]string{"show", server})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		runErr = cmd.Execute()
		return nil
	})
	return out, runErr
}
