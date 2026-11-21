package clientrelay

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSession is a client session that records the requests relayed to it.
type fakeSession struct {
	caps     mcp.ClientCapabilities
	protocol string
	asked    []string
	notes    chan mcp.JSONRPCNotification
}

func (s *fakeSession) SessionID() string                                   { return "fake" }
func (s *fakeSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.notes }
func (s *fakeSession) Initialize()                                         {}
func (s *fakeSession) Initialized() bool                                   { return true }
func (s *fakeSession) GetClientInfo() mcp.Implementation                   { return mcp.Implementation{} }
func (s *fakeSession) SetClientInfo(mcp.Implementation)                    {}
func (s *fakeSession) GetClientCapabilities() mcp.ClientCapabilities       { return s.caps }
func (s *fakeSession) SetClientCapabilities(mcp.ClientCapabilities)        {}
func (s *fakeSession) ProtocolVersion() string                             { return s.protocol }

func (s *fakeSession) RequestElicitation(_ context.Context, _ mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	s.asked = append(s.asked, "elicit")
	return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}}, nil
}

func (s *fakeSession) RequestSampling(_ context.Context, _ mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
	s.asked = append(s.asked, "sample")
	return &mcp.CreateMessageResult{Model: "m"}, nil
}

func (s *fakeSession) ListRoots(_ context.Context, _ mcp.ListRootsRequest) (*mcp.ListRootsResult, error) {
	s.asked = append(s.asked, "roots")
	return &mcp.ListRootsResult{Roots: []mcp.Root{{URI: "file:///work"}}}, nil
}

func withSession(s *fakeSession) context.Context {
	return mcpserver.NewMCPServer("t", "0").WithContext(context.Background(), s)
}

func allCaps() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{
		Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{},
		Sampling:    &mcp.SamplingCapability{},
		Elicitation: &mcp.ElicitationCapability{},
	}
}

func formRequest() mcp.ElicitationRequest {
	return mcp.ElicitationRequest{Params: mcp.ElicitationParams{Message: "ok?", RequestedSchema: map[string]any{"type": "object"}}}
}

func urlRequest() mcp.ElicitationRequest {
	return mcp.ElicitationRequest{Params: mcp.ElicitationParams{Mode: mcp.ElicitationModeURL, Message: "log in", ElicitationID: "e1", URL: "https://example.test"}}
}

func TestHandler_ForwardsToCallingClient(t *testing.T) {
	s := &fakeSession{caps: allCaps()}
	ctx := withSession(s)
	h := Handler{}

	er, err := h.Elicit(ctx, formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionAccept, er.Action)

	sr, err := h.CreateMessage(ctx, mcp.CreateMessageRequest{})
	require.NoError(t, err)
	assert.Equal(t, "m", sr.Model)

	rr, err := h.ListRoots(ctx, mcp.ListRootsRequest{})
	require.NoError(t, err)
	require.Len(t, rr.Roots, 1)
	assert.Equal(t, []string{"elicit", "sample", "roots"}, s.asked)
}

func TestHandler_UsesCallerContext(t *testing.T) {
	s := &fakeSession{caps: allCaps()}
	callCtx := withSession(s)
	h := Handler{Caller: func(context.Context) context.Context { return callCtx }}

	er, err := h.Elicit(context.Background(), formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionAccept, er.Action)
	assert.Equal(t, []string{"elicit"}, s.asked)
}

func TestHandler_NoClientAnswersWithoutAsking(t *testing.T) {
	h := Handler{}
	ctx := context.Background()

	er, err := h.Elicit(ctx, formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionDecline, er.Action)

	_, err = h.CreateMessage(ctx, mcp.CreateMessageRequest{})
	require.ErrorIs(t, err, ErrNoClient)

	rr, err := h.ListRoots(ctx, mcp.ListRootsRequest{})
	require.NoError(t, err)
	encoded, err := json.Marshal(rr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"roots":[]}`, string(encoded), "an empty list, never null")
}

func TestHandler_ClientWithoutCapabilityIsNotAsked(t *testing.T) {
	s := &fakeSession{} // declared nothing
	ctx := withSession(s)
	h := Handler{}

	er, err := h.Elicit(ctx, formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionDecline, er.Action)
	_, err = h.CreateMessage(ctx, mcp.CreateMessageRequest{})
	require.ErrorIs(t, err, ErrNoClient)
	rr, err := h.ListRoots(ctx, mcp.ListRootsRequest{})
	require.NoError(t, err)
	assert.Empty(t, rr.Roots)
	assert.Empty(t, s.asked)
}

func TestHandler_ElicitationModes(t *testing.T) {
	cases := []struct {
		name string
		caps *mcp.ElicitationCapability
		req  mcp.ElicitationRequest
		want bool
	}{
		{"empty capability means form", &mcp.ElicitationCapability{}, formRequest(), true},
		{"empty capability is not url", &mcp.ElicitationCapability{}, urlRequest(), false},
		{"url only", &mcp.ElicitationCapability{URL: &struct{}{}}, urlRequest(), true},
		{"url only is not form", &mcp.ElicitationCapability{URL: &struct{}{}}, formRequest(), false},
		{"both", &mcp.ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}}, urlRequest(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSession{caps: mcp.ClientCapabilities{Elicitation: tc.caps}}
			_, err := Handler{}.Elicit(withSession(s), tc.req)
			require.NoError(t, err)
			assert.Equal(t, tc.want, len(s.asked) == 1)
		})
	}
}

func TestHandler_AnswerDispatchesByMethod(t *testing.T) {
	s := &fakeSession{caps: allCaps()}
	h := Handler{}
	ctx := withSession(s)

	res, rpcErr := h.Answer(ctx, string(mcp.MethodElicitationCreate), json.RawMessage(`{"message":"ok?","requestedSchema":{"type":"object"}}`))
	require.Nil(t, rpcErr)
	assert.Equal(t, mcp.ElicitationResponseActionAccept, res.(*mcp.ElicitationResult).Action)

	res, rpcErr = h.Answer(ctx, string(mcp.MethodSamplingCreateMessage), json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}],"maxTokens":5}`))
	require.Nil(t, rpcErr)
	assert.Equal(t, "m", res.(*mcp.CreateMessageResult).Model)

	res, rpcErr = h.Answer(ctx, string(mcp.MethodListRoots), nil)
	require.Nil(t, rpcErr)
	assert.Len(t, res.(*mcp.ListRootsResult).Roots, 1)
	assert.Equal(t, []string{"elicit", "sample", "roots"}, s.asked)

	_, rpcErr = h.Answer(ctx, string(mcp.MethodElicitationCreate), json.RawMessage(`{"message":"no schema"}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, mcp.INVALID_PARAMS, rpcErr.Code)

	_, rpcErr = h.Answer(ctx, "tasks/get", nil)
	require.NotNil(t, rpcErr)
	assert.Equal(t, mcp.METHOD_NOT_FOUND, rpcErr.Code)

	_, rpcErr = h.Answer(context.Background(), string(mcp.MethodSamplingCreateMessage), nil)
	require.NotNil(t, rpcErr, "no client: sampling fails")
	assert.Equal(t, mcp.INTERNAL_ERROR, rpcErr.Code)
}

func TestHandler_ModernClientIsNotAsked(t *testing.T) {
	s := &fakeSession{caps: allCaps(), protocol: "2026-07-28"}
	require.True(t, mcp.IsModernProtocol(s.protocol))
	er, err := Handler{}.Elicit(withSession(s), formRequest())
	require.NoError(t, err)
	assert.Equal(t, mcp.ElicitationResponseActionDecline, er.Action)
	assert.Empty(t, s.asked)
}
