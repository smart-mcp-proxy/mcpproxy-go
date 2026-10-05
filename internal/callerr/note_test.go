package callerr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

func TestNoteObserveWithRecorder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer srv.Close()

	ctx := WithNote(context.Background())
	dctx := BeginDispatch(ctx)
	proxytransport.MarkDispatched(dctx)
	// Drive one request through the recorder round tripper via a tiny client.
	cfgRT := newRecordingClientForTest()
	req, _ := http.NewRequestWithContext(dctx, http.MethodPost, srv.URL, nil)
	resp, err := cfgRT.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	Observe(ctx, nil, errors.New("request failed with status 502: <html>"))
	o, ok := OutcomeFrom(ctx)
	require.True(t, ok)
	assert.Equal(t, ClassHTTP, o.Class)
	assert.Equal(t, DomainUpstream, o.Domain)
	assert.Equal(t, 502, o.HTTPStatus)
}

func TestNoteObserveSuccessClearsAndIsErrorIsToolError(t *testing.T) {
	ctx := WithNote(context.Background())
	NoteOutcome(ctx, InternalOutcome())
	Observe(ctx, &mcp.CallToolResult{}, nil)
	_, ok := OutcomeFrom(ctx)
	assert.False(t, ok)

	Observe(ctx, &mcp.CallToolResult{IsError: true}, nil)
	o, ok := OutcomeFrom(ctx)
	require.True(t, ok)
	assert.Equal(t, ClassToolError, o.Class)
}

func TestNoteNilSafeAndFreshPerWithNote(t *testing.T) {
	// No note: every call is a safe no-op.
	Observe(context.Background(), nil, errors.New("x"))
	NoteOutcome(context.Background(), InternalOutcome())
	_, ok := OutcomeFrom(context.Background())
	assert.False(t, ok)

	// A sub-call's fresh note must not leak into (or from) its parent's.
	parent := WithNote(context.Background())
	NoteOutcome(parent, UnavailableOutcome())
	child := WithNote(parent)
	_, ok = OutcomeFrom(child)
	assert.False(t, ok)
	NoteOutcome(child, ValidationOutcome())
	o, _ := OutcomeFrom(parent)
	assert.Equal(t, ClassNetwork, o.Class)
}

func TestObserveCancelledCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(WithNote(context.Background()))
	BeginDispatch(ctx)
	cancel()
	Observe(ctx, nil, errors.New("aborted"))
	o, ok := OutcomeFrom(ctx)
	require.True(t, ok)
	assert.Equal(t, ClassCancelled, o.Class)
	assert.Equal(t, DomainClient, o.Domain)
}
