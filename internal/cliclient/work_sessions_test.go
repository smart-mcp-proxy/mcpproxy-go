package cliclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// Spec 124 PR-2 (T102): client calls of the three work-session doors.

func TestClient_ListWorkSessions_QueryAndDecode(t *testing.T) {
	var gotPath, gotQuery, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotKey = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"success":true,"data":{"sessions":[{"id":"ws-1","status":"ended","started_at":"2026-11-26T22:00:00Z","last_activity_at":"2026-11-26T22:10:00Z","duration_ms":600000,"attempts":12,"failures":2,"policy_blocks":1,"stored_failures":2,"last_failure":{"at":"2026-11-26T22:09:00Z","error_class":"network","reason":"connection refused"}}],"total":7,"limit":10,"offset":20}}`))
	}))
	defer srv.Close()

	c := NewClientWithAPIKey(srv.URL, "k1", nil)
	noBlocked := false
	got, err := c.ListWorkSessions(context.Background(), WorkSessionListParams{
		Failed: true, IncludeBlocked: &noBlocked,
		Token: "ci bot", Client: "cursor", Profile: "work", Run: "nightly", ErrorClass: "network",
		Since: "2026-11-26T00:00:00Z", Until: "2026-11-27T00:00:00+02:00",
		Sort: "last_failure", Limit: 10, Offset: 20,
	})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/work-sessions", gotPath)
	require.Equal(t, "k1", gotKey)
	require.Equal(t,
		"client=cursor&error_class=network&failed=true&include_blocked=false&limit=10&offset=20&profile=work&run=nightly&since=2026-11-26T00%3A00%3A00Z&sort=last_failure&token=ci+bot&until=2026-11-27T00%3A00%3A00%2B02%3A00",
		gotQuery)
	require.Equal(t, 7, got.Total)
	require.Len(t, got.Sessions, 1)
	require.Equal(t, "ws-1", got.Sessions[0].ID)
	require.NotNil(t, got.Sessions[0].LastFailure)
	require.Equal(t, "connection refused", got.Sessions[0].LastFailure.Reason)
}

// The zero value asks for nothing: the door's defaults apply.
func TestClient_ListWorkSessions_ZeroParamsSendNoQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"success":true,"data":{"sessions":[],"total":0,"limit":25,"offset":0}}`))
	}))
	defer srv.Close()
	got, err := NewClient(srv.URL, nil).ListWorkSessions(context.Background(), WorkSessionListParams{})
	require.NoError(t, err)
	require.Empty(t, gotQuery)
	require.Empty(t, got.Sessions)
}

func TestClient_GetWorkSession_EscapesTheID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"ws a/b","status":"active","started_at":"2026-11-26T22:00:00Z","last_activity_at":"2026-11-26T22:10:00Z","duration_ms":1,"attempts":1,"failures":0,"policy_blocks":0,"stored_failures":0}}`))
	}))
	defer srv.Close()
	got, err := NewClient(srv.URL, nil).GetWorkSession(context.Background(), "ws a/b")
	require.NoError(t, err)
	require.Equal(t, "/api/v1/work-sessions/ws%20a%2Fb", gotPath)
	require.Equal(t, "active", got.Status)
}

func TestClient_GetWorkSessionTimeline_QueryAndDecode(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"success":true,"data":{"session":{"id":"ws-1","status":"ended","started_at":"2026-11-26T22:00:00Z","last_activity_at":"2026-11-26T22:10:00Z","duration_ms":1,"attempts":3,"failures":1,"policy_blocks":0,"stored_failures":1},"rows":[{"index":4,"kind":"call","at":"2026-11-26T22:09:00.123Z","failed":true,"activity":{"id":"01A","type":"tool_call","status":"error","timestamp":"2026-11-26T22:09:00.123Z","has_sensitive_data":false}}],"position":4,"total_rows":9,"failure_index":1,"failure_total":1,"lifetime_failures":1,"has_before":true,"has_after":true}}`))
	}))
	defer srv.Close()
	before, after := 5, 0
	got, err := NewClient(srv.URL, nil).GetWorkSessionTimeline(context.Background(), "ws-1", WorkSessionTimelineParams{
		At: "failure:2", Before: &before, After: &after, IncludeBodies: true,
	})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/work-sessions/ws-1/timeline", gotPath)
	require.Equal(t, "after=0&at=failure%3A2&before=5&include_bodies=true", gotQuery)
	require.Equal(t, 4, got.Position)
	require.Len(t, got.Rows, 1)
	require.True(t, got.Rows[0].Failed)
	require.NotNil(t, got.Rows[0].Activity)
	require.Equal(t, "01A", got.Rows[0].Activity.ID)
}

// A session that does not exist (or is not the caller's) is its own error, so
// the command can say so and point at the listing.
func TestClient_WorkSessionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"error":"Work session not found","request_id":"r-404"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)

	_, err := c.GetWorkSessionTimeline(context.Background(), "ws-gone", WorkSessionTimelineParams{})
	var notFound *WorkSessionNotFoundError
	require.True(t, errors.As(err, &notFound), "got %T: %v", err, err)
	require.Equal(t, "ws-gone", notFound.ID)
	require.Equal(t, "r-404", notFound.RequestID)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr), "the daemon's text and request id stay reachable")
	require.Equal(t, "Work session not found", apiErr.Message)

	_, err = c.GetWorkSession(context.Background(), "ws-gone")
	require.True(t, errors.As(err, &notFound))
}

// Any other refusal keeps the daemon's text and request id.
func TestClient_WorkSessionRefusalKeepsTextAndRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"error":"Invalid at. No such failure or row in this session","request_id":"r-400"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).GetWorkSessionTimeline(context.Background(), "ws-1", WorkSessionTimelineParams{At: "failure:9"})
	var notFound *WorkSessionNotFoundError
	require.False(t, errors.As(err, &notFound))
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr), "got %T", err)
	require.Contains(t, apiErr.Message, "No such failure")
	require.Equal(t, "r-400", apiErr.RequestID)
}

// A body that is not the JSON envelope (a proxy's error page) is reported with
// its status, never decoded into an empty success.
func TestClient_WorkSessionNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>bad gateway</html>`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).ListWorkSessions(context.Background(), WorkSessionListParams{})
	require.ErrorContains(t, err, "502")
}

// A 404 that is not the door's own (a reverse proxy without the route) is not
// "this session does not exist": its text and request id are reported as they
// came.
func TestClient_WorkSessionForeign404IsNotASessionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"error":"Route not found","request_id":"gw-42"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).GetWorkSessionTimeline(context.Background(), "ws-1", WorkSessionTimelineParams{})
	var notFound *WorkSessionNotFoundError
	require.False(t, errors.As(err, &notFound))
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr), "got %T", err)
	require.Equal(t, "Route not found", apiErr.Message)
	require.Equal(t, "gw-42", apiErr.RequestID)
}

// A value the door refuses reaches the door; it is not replaced by a default.
func TestClient_ListWorkSessions_SendsOutOfRangePaging(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"error":"Invalid limit. Use a number from 1 to 100"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).ListWorkSessions(context.Background(), WorkSessionListParams{Limit: -1, Offset: -5})
	require.ErrorContains(t, err, "Invalid limit")
	require.Equal(t, "limit=-1&offset=-5", gotQuery)
}

// A long body that is not the envelope is quoted in part.
func TestClient_WorkSessionNonJSONBodyIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).ListWorkSessions(context.Background(), WorkSessionListParams{})
	require.Error(t, err)
	require.Less(t, len(err.Error()), 400)
}

// A success without a data object is an error, not an empty timeline.
func TestClient_WorkSessionSuccessWithoutData(t *testing.T) {
	for _, body := range []string{`{"success":true,"data":null}`, `{"success":true}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		_, err := NewClient(srv.URL, nil).GetWorkSessionTimeline(context.Background(), "ws-1", WorkSessionTimelineParams{})
		srv.Close()
		require.ErrorContains(t, err, "no data", body)
	}
}

// "." and ".." are path segments a URL resolves away: a request for ".."
// would reach another endpoint. No request is made for them, or for an empty
// id; they are answered as an unknown id is.
func TestClient_WorkSessionDotIDsMakeNoRequest(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"success":true,"data":{"sessions":[],"total":0,"limit":25,"offset":0}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil)
	for _, id := range []string{".", "..", ""} {
		var notFound *WorkSessionNotFoundError
		_, err := c.GetWorkSession(context.Background(), id)
		require.True(t, errors.As(err, &notFound), "GetWorkSession(%q): %v", id, err)
		_, err = c.GetWorkSessionTimeline(context.Background(), id, WorkSessionTimelineParams{})
		require.True(t, errors.As(err, &notFound), "GetWorkSessionTimeline(%q): %v", id, err)
		require.Equal(t, id, notFound.ID)
	}
	require.Zero(t, requests)
}

// The preview is cut on a byte. A character that straddles the cut is dropped
// whole.
func TestClient_WorkSessionBodyPreviewKeepsCharactersWhole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("a", 299) + "\u00e9 and more"))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).ListWorkSessions(context.Background(), WorkSessionListParams{})
	require.Error(t, err)
	require.True(t, utf8.ValidString(err.Error()), "%q", err.Error())
	require.Contains(t, err.Error(), strings.Repeat("a", 299)+"...")
}
