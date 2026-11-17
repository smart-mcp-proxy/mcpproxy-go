package cliclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 125 PR-2: approve, block and unblock calls.

func TestClient_ApproveServerSkills_SendsBodyAndDecodes(t *testing.T) {
	var gotBody, gotCT, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotCT, gotPath = string(b), r.Header.Get("Content-Type"), r.URL.Path
		_, _ = w.Write([]byte(`{"success":true,"data":{"approved":2}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)
	got, err := c.ApproveServerSkills(context.Background(), "alpha", contracts.SkillsApproveRequest{
		Skills: []string{"a", "b"}, ExpectedHashes: map[string]string{"a": "sk1-1", "b": "sk1-2"}, ExpectedIncarnation: 3,
	})
	require.NoError(t, err)
	require.Equal(t, 2, got.Approved)
	require.Equal(t, "/api/v1/servers/alpha/skills/approve", gotPath)
	require.Equal(t, "application/json", gotCT)
	require.JSONEq(t, `{"skills":["a","b"],"expected_hashes":{"a":"sk1-1","b":"sk1-2"},"expected_incarnation":3}`, gotBody)
}

func TestClient_ApproveServerSkills_TypedRefusals(t *testing.T) {
	respond := func(status int, body string) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return NewClient(srv.URL, nil)
	}
	req := contracts.SkillsApproveRequest{Skills: []string{"a"}}

	c := respond(http.StatusConflict, `{"success":false,"error":"review_out_of_date: x","error_code":"review_out_of_date","changed":["a"],"unreviewed":["b"],"request_id":"r9"}`)
	_, err := c.ApproveServerSkills(context.Background(), "alpha", req)
	var stale *SkillsStaleReviewError
	require.True(t, errors.As(err, &stale), "got %T", err)
	require.Equal(t, []string{"a"}, stale.Changed)
	require.Equal(t, []string{"b"}, stale.Unreviewed)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, "r9", apiErr.RequestID)

	c = respond(http.StatusUnprocessableEntity, `{"success":false,"error":"skill_unusable: y","error_code":"skill_unusable","skills":["dyn"]}`)
	_, err = c.ApproveServerSkills(context.Background(), "alpha", req)
	var unusable *SkillsUnusableError
	require.True(t, errors.As(err, &unusable), "got %T", err)
	require.Equal(t, []string{"dyn"}, unusable.Skills)

	// server_replaced stays a plain API error with its message.
	c = respond(http.StatusConflict, `{"success":false,"error":"server_replaced: gone"}`)
	_, err = c.ApproveServerSkills(context.Background(), "alpha", req)
	require.False(t, errors.As(err, &stale))
	require.ErrorContains(t, err, "server_replaced")
}

func TestClient_SetServerSkillsBlocked_Routes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"success":true,"data":{"changed":1}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)
	for blocked, want := range map[bool]string{true: "/api/v1/servers/a/skills/block", false: "/api/v1/servers/a/skills/unblock"} {
		got, err := c.SetServerSkillsBlocked(context.Background(), "a", blocked, contracts.SkillsBlockRequest{Skills: []string{"s"}})
		require.NoError(t, err)
		require.Equal(t, 1, got.Changed)
		require.Equal(t, want, gotPath)
	}
}
