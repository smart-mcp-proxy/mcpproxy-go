package dialgate

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capability rows asserted on a transport by mcp-go v1.0.0 and by core
// (data-model §5, review R8.1).
type capability struct {
	name string
	has  func(transport.Interface) bool
}

var capabilities = []capability{
	{"BidirectionalInterface", func(t transport.Interface) bool {
		_, ok := t.(transport.BidirectionalInterface)
		return ok
	}},
	{"HTTPConnection", func(t transport.Interface) bool {
		_, ok := t.(transport.HTTPConnection)
		return ok
	}},
	{"SetConnectionLostHandler", func(t transport.Interface) bool {
		_, ok := t.(interface{ SetConnectionLostHandler(func(error)) })
		return ok
	}},
	{"RequiresLegacyProtocol", func(t transport.Interface) bool {
		_, ok := t.(interface{ RequiresLegacyProtocol() bool })
		return ok
	}},
	{"GetOAuthHandler", func(t transport.Interface) bool {
		_, ok := t.(interface {
			GetOAuthHandler() *transport.OAuthHandler
		})
		return ok
	}},
	{"IsOAuthEnabled", func(t transport.Interface) bool {
		_, ok := t.(interface{ IsOAuthEnabled() bool })
		return ok
	}},
}

func productionKinds(t *testing.T) map[string]transport.Interface {
	t.Helper()
	plain, err := transport.NewStreamableHTTP("http://127.0.0.1:1/mcp")
	require.NoError(t, err)
	oauthHTTP, err := transport.NewStreamableHTTP("http://127.0.0.1:1/mcp",
		transport.WithHTTPOAuth(transport.OAuthConfig{ClientID: "x", TokenStore: transport.NewMemoryTokenStore()}))
	require.NoError(t, err)
	sse, err := transport.NewSSE("http://127.0.0.1:1/sse")
	require.NoError(t, err)
	oauthSSE, err := transport.NewSSE("http://127.0.0.1:1/sse",
		transport.WithOAuth(transport.OAuthConfig{ClientID: "x", TokenStore: transport.NewMemoryTokenStore()}))
	require.NoError(t, err)
	return map[string]transport.Interface{
		"stdio":                 transport.NewStdio("true", nil),
		"streamable-http":       plain,
		"streamable-http-oauth": oauthHTTP,
		"sse":                   sse,
		"sse-oauth":             oauthSSE,
	}
}

// TestWrapTransportCapabilityParity: for every production kind and every
// capability row, the wrapper satisfies the row exactly when the inner value
// does, and Unwrap returns the inner value.
func TestWrapTransportCapabilityParity(t *testing.T) {
	g := New()
	for name, inner := range productionKinds(t) {
		w := WrapTransport(g, inner)
		require.NotSame(t, inner, w, name)
		assert.Same(t, inner, Unwrap(w), "%s: Unwrap returns the inner transport", name)
		assert.Equal(t, w, WrapTransport(g, w), "%s: wrapping twice is a no-op", name)
		for _, c := range capabilities {
			assert.Equal(t, c.has(inner), c.has(w), "%s: capability %s parity", name, c.name)
		}
		_, isGeneric := w.(*gatedGeneric)
		assert.False(t, isGeneric, "%s: a production kind must have its own wrapper", name)
	}
}

// Retired wrapper: Start, SendRequest and SendNotification refuse; a
// server-to-client request gets a JSON-RPC error and the handler is not
// called (T4.17 f).
func TestWrapTransport_RetiredRefusesAndAnswersRequestsWithError(t *testing.T) {
	g := New()
	inner, err := transport.NewStreamableHTTP("http://127.0.0.1:1/mcp")
	require.NoError(t, err)
	w := WrapTransport(g, inner)
	g.Retire()

	assert.ErrorIs(t, w.Start(context.Background()), ErrRetired)
	_, err = w.SendRequest(context.Background(), transport.JSONRPCRequest{Method: "ping"})
	assert.ErrorIs(t, err, ErrRetired)

	called := false
	h := gatedRequestHandler(g, func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
		called = true
		return nil, nil
	})
	resp, err := h(context.Background(), transport.JSONRPCRequest{Method: "ping"})
	require.NoError(t, err)
	require.NotNil(t, resp.Error)
	assert.False(t, called)
}

// TestMCPGoTransportMethodSetPinned lists the exported methods of the three
// mcp-go transport kinds and compares them with the reviewed list, so an
// mcp-go upgrade that adds an optional interface fails until the wrappers
// are reviewed.
func TestMCPGoTransportMethodSetPinned(t *testing.T) {
	pinned := map[string][]string{
		"Stdio": {"Close", "GetSessionId", "SendNotification", "SendRequest", "SetNotificationHandler",
			"SetRequestHandler", "Start", "Stderr"},
		"StreamableHTTP": {"Close", "GetOAuthHandler", "GetSessionId", "IsOAuthEnabled", "RequiresLegacyProtocol",
			"SendNotification", "SendRequest", "SetNotificationHandler", "SetProtocolVersion", "SetRequestHandler", "Start"},
		"SSE": {"Close", "GetBaseURL", "GetEndpoint", "GetOAuthHandler", "GetSessionId", "IsOAuthEnabled",
			"SendNotification", "SendRequest", "SetConnectionLostHandler", "SetNotificationHandler", "SetProtocolVersion", "Start"},
	}
	types := map[string]reflect.Type{
		"Stdio":          reflect.TypeOf(&transport.Stdio{}),
		"StreamableHTTP": reflect.TypeOf(&transport.StreamableHTTP{}),
		"SSE":            reflect.TypeOf(&transport.SSE{}),
	}
	for name, typ := range types {
		var got []string
		for i := 0; i < typ.NumMethod(); i++ {
			got = append(got, typ.Method(i).Name)
		}
		sort.Strings(got)
		assert.Equal(t, pinned[name], got, "mcp-go %s method set changed: review dialgate's wrapper for it", name)
	}
}
