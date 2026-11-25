package clientrelay

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func urlCaps() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}}}
}

func TestURLElicitations_CompletionReachesTheAskedClient(t *testing.T) {
	var urls URLElicitations
	asked := &fakeSession{caps: urlCaps(), notes: make(chan mcp.JSONRPCNotification, 1)}
	other := &fakeSession{caps: urlCaps(), notes: make(chan mcp.JSONRPCNotification, 1)}
	h := Handler{URLs: &urls}

	res, err := h.Elicit(withSession(asked), urlRequest()) // elicitationId "e1"
	require.NoError(t, err)
	require.Equal(t, mcp.ElicitationResponseActionAccept, res.Action)

	assert.False(t, urls.Complete(mcp.NewElicitationCompleteNotification("unknown")), "unknown id is dropped")
	require.True(t, urls.Complete(mcp.NewElicitationCompleteNotification("e1")))
	n := <-asked.notes
	assert.Equal(t, "e1", n.Params.AdditionalFields["elicitationId"])
	assert.Empty(t, other.notes)
	assert.False(t, urls.Complete(mcp.NewElicitationCompleteNotification("e1")), "delivered once")
}

func TestURLElicitations_NotAcceptedIsForgotten(t *testing.T) {
	var urls URLElicitations
	noURL := &fakeSession{caps: allCaps(), notes: make(chan mcp.JSONRPCNotification, 1)} // form only: declined
	res, err := Handler{URLs: &urls}.Elicit(withSession(noURL), urlRequest())
	require.NoError(t, err)
	require.Equal(t, mcp.ElicitationResponseActionDecline, res.Action)
	assert.False(t, urls.Complete(mcp.NewElicitationCompleteNotification("e1")))
	assert.Empty(t, noURL.notes)
}

func TestURLElicitations_FullChannelDoesNotBlock(t *testing.T) {
	var urls URLElicitations
	full := &fakeSession{caps: urlCaps(), notes: make(chan mcp.JSONRPCNotification)} // nobody reading
	_, err := Handler{URLs: &urls}.Elicit(withSession(full), urlRequest())
	require.NoError(t, err)
	assert.False(t, urls.Complete(mcp.NewElicitationCompleteNotification("e1")))
}
