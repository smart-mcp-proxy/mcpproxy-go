package clientrelay

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_ElicitationNamesTheServer(t *testing.T) {
	s := &fakeSession{caps: allCaps()}
	_, err := Handler{Server: "ssh"}.Elicit(withSession(s), formRequest())
	require.NoError(t, err)
	assert.Equal(t, "[ssh] ok?", s.message)
}

func TestHandler_AuditsEveryRequest(t *testing.T) {
	var events []Event
	h := Handler{Server: "srv", Audit: func(e Event) { events = append(events, e) }}

	s := &fakeSession{caps: allCaps()}
	_, err := h.Elicit(withSession(s), formRequest())
	require.NoError(t, err)
	_, err = h.CreateMessage(context.Background(), mcp.CreateMessageRequest{})
	require.Error(t, err)
	_, err = h.ListRoots(context.Background(), mcp.ListRootsRequest{})
	require.NoError(t, err)

	require.Len(t, events, 3)
	forwarded := events[0]
	assert.Equal(t, "srv", forwarded.Server)
	assert.Equal(t, mcp.MethodElicitationCreate, forwarded.Method)
	assert.Equal(t, "fake", forwarded.SessionID)
	assert.NoError(t, forwarded.Err)
	assert.Equal(t, "ok?", forwarded.Params.(mcp.ElicitationParams).Message, "audit keeps what the upstream asked")
	assert.Equal(t, mcp.ElicitationResponseActionAccept, forwarded.Result.(*mcp.ElicitationResult).Action)

	for _, e := range events[1:] {
		assert.True(t, errors.Is(e.Err, ErrNoClient), "%s: nobody was asked", e.Method)
		assert.Empty(t, e.SessionID)
	}
	assert.Nil(t, events[1].Result, "sampling had no answer")
	assert.Empty(t, events[2].Result.(*mcp.ListRootsResult).Roots, "roots answered with none")
}

func TestHandler_RootsOutsideACallGoToTheLastCaller(t *testing.T) {
	last := &fakeSession{caps: allCaps()}
	h := Handler{LastCaller: func() context.Context { return withSession(last) }}

	res, err := h.ListRoots(context.Background(), mcp.ListRootsRequest{})
	require.NoError(t, err)
	require.Len(t, res.Roots, 1)
	assert.Equal(t, []string{"roots"}, last.asked)

	// Elicitation and sampling never go to the last caller: nobody is asked.
	er, err := h.Elicit(context.Background(), formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionDecline, er.Action)
	assert.Equal(t, []string{"roots"}, last.asked)
}

func TestHandler_GoneLastCallerAnswersNoRoots(t *testing.T) {
	gone := &fakeSession{} // declared nothing any more
	h := Handler{LastCaller: func() context.Context { return withSession(gone) }}
	res, err := h.ListRoots(context.Background(), mcp.ListRootsRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Roots)
}

func TestCapabilities_RootsListChanged(t *testing.T) {
	require.NotNil(t, Capabilities().Roots)
	assert.True(t, Capabilities().Roots.ListChanged)
}
