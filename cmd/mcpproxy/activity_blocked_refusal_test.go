package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 108 D39 (T148): the CLI prints a blocked record's refusal text
// verbatim. The text is the disclosed refusal of
// internal/profile/testdata/contract/tool_refusals.json: the activity record is
// what the operator reads, so the CLI must neither truncate, re-wrap nor
// "tidy" the quoted profile title.

func toolRefusalFromGolden(t *testing.T, name, label string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "profile", "testdata", "contract", "tool_refusals.json"))
	require.NoError(t, err)
	var g struct {
		Refusals []struct {
			Name      string `json:"name"`
			Disclosed string `json:"disclosed"`
		} `json:"refusals"`
	}
	require.NoError(t, json.Unmarshal(raw, &g))
	for _, r := range g.Refusals {
		if r.Name == name {
			return strings.NewReplacer(
				"<server>", "github", "<tool>", "create_issue", "<tier>", "write", "<cap>", "read", "<label>", label,
			).Replace(r.Disclosed)
		}
	}
	t.Fatalf("no golden refusal %q", name)
	return ""
}

func TestActivityBlockedRefusalTextVerbatim_CLI(t *testing.T) {
	text := toolRefusalFromGolden(t, "tier", `"Work Read-only" (work-readonly)`)
	require.Contains(t, text, `profile "Work Read-only" (work-readonly) allows read tools only`)

	record := map[string]any{
		"id": "01BLOCKED", "source": "mcp", "type": "policy_decision", "server_name": "github", "tool_name": "create_issue",
		"status": "blocked", "timestamp": "2026-10-02T09:00:00Z", "error_message": text,
		"client_id": "cursor", "profile": "work-readonly", "profile_source": "pin", "token_name": "client-cursor",
		"metadata": map[string]any{"block_reason": "profile_tier", "reason": text},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
		case "/api/v1/activity":
			body, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{
				"activities": []map[string]any{record}, "total": 1, "limit": 50, "offset": 0,
			}})
			_, _ = w.Write(body)
		case "/api/v1/activity/01BLOCKED":
			body, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"activity": record}})
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	withClientDaemon(t, srv.URL)

	t.Run("activity list -o json carries the text verbatim", func(t *testing.T) {
		p108ResetFlags(activityListCmd)
		t.Cleanup(func() { p108ResetFlags(activityListCmd) })
		out, _, err := runCLI(t, GetActivityCommand, "json", "list", "--status", "blocked")
		require.NoError(t, err)
		var doc struct {
			Activities []struct {
				ErrorMessage string `json:"error_message"`
				Metadata     struct {
					Reason string `json:"reason"`
				} `json:"metadata"`
			} `json:"activities"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
		require.Len(t, doc.Activities, 1)
		assert.Equal(t, text, doc.Activities[0].ErrorMessage)
		assert.Equal(t, text, doc.Activities[0].Metadata.Reason)
	})

	t.Run("activity show (table) prints the text verbatim", func(t *testing.T) {
		out, _, err := runCLI(t, GetActivityCommand, "table", "show", "01BLOCKED")
		require.NoError(t, err)
		assert.Contains(t, out, "Error:        "+text)
	})
}
