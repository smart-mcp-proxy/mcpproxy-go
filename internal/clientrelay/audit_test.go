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
	require.NotNil(t, Capabilities([]string{"roots"}).Roots)
	assert.True(t, Capabilities([]string{"roots"}).Roots.ListChanged)
}

func TestCapabilities_OnlyTheGrantedFeatures(t *testing.T) {
	assert.Equal(t, mcp.ClientCapabilities{}, Capabilities(nil))
	c := Capabilities([]string{"elicitation"})
	assert.NotNil(t, c.Elicitation)
	assert.Nil(t, c.Sampling)
	assert.Nil(t, c.Roots)
}

// The fifth request in flight is shed at once: declined, or an error, and
// without an audit event.
func TestHandler_ShedsBeyondTheInFlightLimit(t *testing.T) {
	var events []Event
	s := &fakeSession{caps: allCaps()}
	h := Handler{InFlight: &InFlight{}, Audit: func(e Event) { events = append(events, e) }}
	for range maxInFlight {
		require.True(t, h.InFlight.acquire())
	}

	er, err := h.Elicit(withSession(s), formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionDecline, er.Action)
	_, err = h.CreateMessage(withSession(s), mcp.CreateMessageRequest{})
	assert.ErrorIs(t, err, ErrBusy)
	_, err = h.ListRoots(withSession(s), mcp.ListRootsRequest{})
	assert.ErrorIs(t, err, ErrBusy)
	assert.Empty(t, s.asked)
	assert.Empty(t, events)

	h.InFlight.release()
	_, err = h.Elicit(withSession(s), formRequest())
	require.NoError(t, err)
	_, err = h.ListRoots(withSession(s), mcp.ListRootsRequest{})
	require.NoError(t, err, "a slot is released after each request")
	assert.Equal(t, []string{"elicit", "roots"}, s.asked)
}

func TestHandler_AnswerRefusesFeaturesNotGranted(t *testing.T) {
	s := &fakeSession{caps: allCaps()}
	h := Handler{Features: []string{"elicitation"}}
	for _, method := range []mcp.MCPMethod{mcp.MethodSamplingCreateMessage, mcp.MethodListRoots} {
		_, rpcErr := h.Answer(withSession(s), string(method), nil)
		require.NotNil(t, rpcErr)
		assert.Equal(t, mcp.METHOD_NOT_FOUND, rpcErr.Code)
	}
	assert.Empty(t, s.asked)
}
