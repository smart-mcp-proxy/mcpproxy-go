package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// memoryReviewPayload is a quarantined server with nine tools: three read tools
// the core pre-selects (default_allowed true), one read tool from an older core
// (no default_allowed key, so it must count as false), two write tools, one
// destructive and one unannotated tool, and one more write tool.
const memoryReviewPayload = `{"success":true,"data":{"server":{"name":"memory","quarantined":true,"definitions_captured":true},"tools":[
{"name":"read_a","tier":"read","approval_status":"pending","scan_verdict":"clean","default_allowed":true},
{"name":"read_b","tier":"read","approval_status":"pending","scan_verdict":"clean","default_allowed":true},
{"name":"read_c","tier":"read","approval_status":"pending","scan_verdict":"clean","default_allowed":true},
{"name":"legacy_read","tier":"read","approval_status":"pending","scan_verdict":"clean"},
{"name":"write_a","tier":"write","approval_status":"pending","scan_verdict":"clean","default_allowed":false},
{"name":"write_b","tier":"write","approval_status":"pending","scan_verdict":"clean","default_allowed":false},
{"name":"write_c","tier":"write","approval_status":"pending","scan_verdict":"clean","default_allowed":false},
{"name":"delete_a","tier":"destructive","approval_status":"pending","scan_verdict":"clean","default_allowed":false},
{"name":"plain_a","tier":"unannotated","approval_status":"pending","scan_verdict":"not_scanned","default_allowed":false}
]}}`

const memoryDefaultBlock = "legacy_read, write_a, write_b, write_c, delete_a, plain_a"

type reviewRecorder struct {
	mu       sync.Mutex
	requests []reviewRequest
}

func (r *reviewRecorder) add(req reviewRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

func (r *reviewRecorder) writes() []reviewRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []reviewRequest
	for _, req := range r.requests {
		if req.method == http.MethodPost {
			out = append(out, req)
		}
	}
	return out
}

func newMemoryReviewDaemon(t *testing.T, recorder *reviewRecorder) {
	t.Helper()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		recorder.add(reviewRequest{method: r.Method, path: r.URL.Path, body: string(body)})
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/servers/memory/review":
			_, _ = w.Write([]byte(memoryReviewPayload))
		case "/api/v1/servers/bare/review":
			_, _ = w.Write([]byte(`{"success":true,"data":{"server":{"name":"bare","quarantined":true,"definitions_captured":false},"tools":[]}}`))
		case "/api/v1/servers/trusted/review":
			_, _ = w.Write([]byte(`{"success":true,"data":{"server":{"name":"trusted","quarantined":false},"tools":[]}}`))
		case "/api/v1/servers/memory/security/approve", "/api/v1/servers/bare/security/approve":
			_, _ = w.Write([]byte(`{"success":true,"data":{"status":"approved","server_name":"memory"}}`))
		case "/api/v1/servers/trusted/tools/approve":
			_, _ = w.Write([]byte(`{"success":true,"data":{"message":"Approved 1 tool for server trusted"}}`))
		default:
			t.Errorf("unexpected review request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(daemon.Close)
	withReviewDaemon(t, daemon.URL)
}

func runReviewApprove(t *testing.T, format string, confirm func(string) (bool, error), args ...string) (string, error) {
	t.Helper()
	setOutputGlobals(t, format, false)
	var runErr error
	out := captureReviewOutput(t, func() error {
		cmd := newReviewCommand(confirm)
		cmd.SetArgs(append([]string{"approve"}, args...))
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		runErr = cmd.Execute()
		return nil
	})
	return out, runErr
}

func approveBlock(t *testing.T, recorder *reviewRecorder, path string) []any {
	t.Helper()
	for _, req := range recorder.writes() {
		if req.path == path {
			var body map[string]any
			require.NoError(t, json.Unmarshal([]byte(req.body), &body))
			block, _ := body["block"].([]any)
			return block
		}
	}
	t.Fatalf("no write to %s in %#v", path, recorder.requests)
	return nil
}

func TestReviewApproveQuarantinedUsesDefaultSelection(t *testing.T) {
	recorder := &reviewRecorder{}
	newMemoryReviewDaemon(t, recorder)

	out, err := runReviewApprove(t, "table", nil, "memory", "--yes")
	require.NoError(t, err)
	require.Equal(t, []any{"legacy_read", "write_a", "write_b", "write_c", "delete_a", "plain_a"},
		approveBlock(t, recorder, "/api/v1/servers/memory/security/approve"),
		"no flag blocks every tool the core did not pre-select; a missing default_allowed counts as false")
	require.Contains(t, out, "Allowing 3 of 9 tools; blocking 6: "+memoryDefaultBlock)
	assertReviewGolden(t, "review-approve-default.golden", out+"\n")
}

func TestReviewApproveAllFlag(t *testing.T) {
	t.Run("--all allows every tool", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		out, err := runReviewApprove(t, "table", nil, "memory", "--all", "--yes")
		require.NoError(t, err)
		require.Empty(t, approveBlock(t, recorder, "/api/v1/servers/memory/security/approve"))
		assertReviewGolden(t, "review-approve-all.golden", out+"\n")
	})
	t.Run("--all --except subtracts", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "memory", "--all", "--except", "delete_a", "--yes")
		require.NoError(t, err)
		require.Equal(t, []any{"delete_a"}, approveBlock(t, recorder, "/api/v1/servers/memory/security/approve"))
	})
	t.Run("--except subtracts from the default selection", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "memory", "--except", "read_a", "--yes")
		require.NoError(t, err)
		require.Equal(t, []any{"read_a", "legacy_read", "write_a", "write_b", "write_c", "delete_a", "plain_a"},
			approveBlock(t, recorder, "/api/v1/servers/memory/security/approve"))
	})
	t.Run("--all on a trusted server is the approve-all default", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "trusted", "--all", "--yes")
		require.NoError(t, err)
		assertReviewRequest(t, recorder.requests, "POST", "/api/v1/servers/trusted/tools/approve", map[string]any{"approve_all": true, "expected_hashes": map[string]any{}})
	})
	t.Run("--except stays rejected on a trusted server", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "trusted", "--except", "x", "--yes")
		require.Error(t, err)
		require.Empty(t, recorder.writes())
	})
}

func TestReviewApproveToolsSelectsExactly(t *testing.T) {
	t.Run("blocks the rest", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		out, err := runReviewApprove(t, "table", nil, "memory", "--tools", "read_a,write_a", "--yes")
		require.NoError(t, err)
		require.Equal(t, []any{"read_b", "read_c", "legacy_read", "write_b", "write_c", "delete_a", "plain_a"},
			approveBlock(t, recorder, "/api/v1/servers/memory/security/approve"))
		require.Contains(t, out, "Allowing 2 of 9 tools; blocking 7: ")
	})
	t.Run("unknown tool is an error and writes nothing", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "memory", "--tools", "read_a,nope", "--yes")
		require.EqualError(t, err, "unknown tool 'nope' for server 'memory'")
		require.Empty(t, recorder.writes())
	})
	t.Run("--all with --tools is an error and writes nothing", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "memory", "--all", "--tools", "read_a", "--yes")
		require.Error(t, err)
		require.Contains(t, err.Error(), "--all")
		require.Empty(t, recorder.writes())
	})
}

func TestReviewApproveSelectionFlagsNeedCapturedTools(t *testing.T) {
	for _, flags := range [][]string{{"--tools", "nonexistent"}, {"--except", "nonexistent"}} {
		t.Run(flags[0], func(t *testing.T) {
			recorder := &reviewRecorder{}
			newMemoryReviewDaemon(t, recorder)
			_, err := runReviewApprove(t, "table", nil, append([]string{"bare"}, append(flags, "--yes")...)...)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no tool definitions captured")
			require.Contains(t, err.Error(), "'bare'")
			require.Empty(t, recorder.writes(), "selection flags are never silently discarded")
		})
	}
}

func TestReviewApprovePromptNamesCount(t *testing.T) {
	t.Run("default selection", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		var prompt string
		_, err := runReviewApprove(t, "table", func(message string) (bool, error) { prompt = message; return false, nil }, "memory")
		require.NoError(t, err)
		require.Equal(t, "Approve server 'memory' with 3 of 9 tools? Blocked: "+memoryDefaultBlock+".", prompt)
		require.Empty(t, recorder.writes(), "declining sends no write")
	})
	t.Run("all", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		var prompt string
		_, err := runReviewApprove(t, "table", func(message string) (bool, error) { prompt = message; return false, nil }, "memory", "--all")
		require.NoError(t, err)
		require.Equal(t, "Approve server 'memory' with all 9 tools?", prompt)
		require.Empty(t, recorder.writes())
	})
	t.Run("nothing captured", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		var prompt string
		_, err := runReviewApprove(t, "table", func(message string) (bool, error) { prompt = message; return false, nil }, "bare")
		require.NoError(t, err)
		require.Equal(t, "Approve server 'bare' without seeing tools?", prompt)
		require.Empty(t, recorder.writes())
	})
	t.Run("confirming sends the write", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", func(string) (bool, error) { return true, nil }, "memory")
		require.NoError(t, err)
		require.Len(t, recorder.writes(), 1)
	})
}

func TestReviewApproveJSONIsTheBareRESTObject(t *testing.T) {
	recorder := &reviewRecorder{}
	newMemoryReviewDaemon(t, recorder)
	out, err := runReviewApprove(t, "json", nil, "memory", "--yes")
	require.NoError(t, err)
	require.NotContains(t, out, "Allowing")
	require.JSONEq(t, `{"status":"approved","server_name":"memory"}`, strings.TrimSpace(out))
}

func TestReviewApproveSelection(t *testing.T) {
	yes, no := true, false
	tools := []reviewToolState{
		{Name: "a", Tier: "read", DefaultAllowed: &yes},
		{Name: "b", Tier: "read", DefaultAllowed: &yes},
		{Name: "c", Tier: "write", DefaultAllowed: &no},
		{Name: "d", Tier: "read"},
	}
	for _, tc := range []struct {
		name    string
		all     bool
		only    []string
		except  []string
		block   []string
		allowed int
		err     string
	}{
		{name: "default", block: []string{"c", "d"}, allowed: 2},
		{name: "all", all: true, block: nil, allowed: 4},
		{name: "all except", all: true, except: []string{"c"}, block: []string{"c"}, allowed: 3},
		{name: "only", only: []string{"c", "a"}, block: []string{"b", "d"}, allowed: 2},
		{name: "only except", only: []string{"a", "b"}, except: []string{"b"}, block: []string{"b", "c", "d"}, allowed: 1},
		{name: "default except", except: []string{"a"}, block: []string{"a", "c", "d"}, allowed: 1},
		{name: "unknown only", only: []string{"zz"}, err: "unknown tool 'zz'"},
		{name: "unknown except", except: []string{"zz"}, err: "unknown tool 'zz'"},
		{name: "all and only", all: true, only: []string{"a"}, err: "--all cannot be combined with --tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, allowed, err := reviewApproveSelection(tools, tc.all, tc.only, tc.except)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.block, block)
			require.Equal(t, tc.allowed, allowed)
		})
	}
}
