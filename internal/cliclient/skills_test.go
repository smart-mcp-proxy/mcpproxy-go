package cliclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClient_ListServerSkills(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"server":"a b","supported":true,"skills":[{"id":"a b:s","path":"s","status":"pending","files":[{"path":"SKILL.md","size":3}],"total_size":3}]}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)
	got, err := c.ListServerSkills(context.Background(), "a b")
	require.NoError(t, err)
	require.Equal(t, "/api/v1/servers/a%20b/skills", gotPath)
	require.True(t, got.Enabled)
	require.Len(t, got.Skills, 1)
	require.EqualValues(t, 3, got.Skills[0].TotalSize)
}

func TestClient_GetServerSkillContent_Query(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"success":true,"data":{"text":"hi","offset":5,"next_offset":7,"size":9,"digest":"sha256:x"}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)
	got, err := c.GetServerSkillContent(context.Background(), "alpha", "my-skill", "refs/a b.md", true, 5)
	require.NoError(t, err)
	require.Equal(t, "copy=approved&file=refs%2Fa+b.md&offset=5&skill=my-skill", gotQuery)
	require.Equal(t, "hi", got.Text)
	require.NotNil(t, got.NextOffset)
	require.EqualValues(t, 7, *got.NextOffset)
}

func TestClient_RefreshServerSkills_ErrorBodyKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"success":false,"error":"skills_not_supported: server a does not announce the skills extension","error_code":"skills_not_supported","request_id":"r1"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)
	_, err := c.RefreshServerSkills(context.Background(), "a")
	require.Error(t, err)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr), "got %T", err)
	require.Contains(t, apiErr.Message, "skills_not_supported")
	require.Equal(t, "r1", apiErr.RequestID)
}
