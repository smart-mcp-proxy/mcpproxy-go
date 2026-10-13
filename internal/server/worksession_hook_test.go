package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// newWorkSessionProxy builds the smallest MCPProxyServer that can resolve a work
// session: a real session store (persistence needs a storage manager) and a stub
// resolver, so the test does not have to stand up a whole Runtime.
func newWorkSessionProxy(t *testing.T) (*MCPProxyServer, *SessionStore) {
	t.Helper()

	logger := zap.NewNop()
	sm, err := storage.NewManager(t.TempDir(), logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })

	store := NewSessionStore(logger)
	store.SetStorageManager(sm)

	p := &MCPProxyServer{
		logger:       logger,
		sessionStore: store,
		workSessionResolver: func(id runtime.WorkSessionIdentity) string {
			return "ws-" + id.ClientName
		},
	}
	return p, store
}

func workSessionCtx(sessionID string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	return helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
}

// The regression: EVERY tools/call is work, whichever tool it names.
//
// Work used to be stamped inside individual handlers (retrieve_tools, the
// call_tool_* variants, code_execution). The other built-ins — list_registries,
// upstream_servers, quarantine_security, … — never marked the session, so the
// activity they wrote carried an empty work_session_id. The Web UI then grouped
// those rows under the raw TRANSPORT session id while the rest of the same
// connection grouped under its work-session id, and one opencode connection
// appeared in the session picker as two sessions (Spec 082 SC-002).
func TestWorkSession_AnyToolCallMarksWork(t *testing.T) {
	p, store := newWorkSessionProxy(t)
	store.SetSession("s1", "opencode", "1.17.18", false, false, nil)

	// A tools/call naming a built-in that never called markSessionWorked itself.
	got := p.markWorkIfToolCall(workSessionCtx("s1"), mcp.MethodToolsCall)

	assert.Equal(t, "ws-opencode", got,
		"a tools/call must resolve a work session regardless of which tool it names")
	assert.Equal(t, "ws-opencode", store.WorkSessionID("s1"),
		"the work session must be cached on the connection, so every later record agrees")
}

// The counterweight, and the reason this is a method allow-list rather than
// "mark on any request": a connection that only shakes hands and lists tools has
// done no work and must leave nothing behind. On a real machine 99 of 100
// session records were background agents doing exactly this, every ~15 minutes.
func TestWorkSession_HandshakeAndListingAreNotWork(t *testing.T) {
	p, store := newWorkSessionProxy(t)
	store.SetSession("s1", "background-agent", "1.0", false, false, nil)

	for _, method := range []mcp.MCPMethod{
		mcp.MethodInitialize,
		mcp.MethodToolsList,
		mcp.MethodPing,
	} {
		assert.Empty(t, p.markWorkIfToolCall(workSessionCtx("s1"), method),
			"%s is not work and must not persist a session", method)
	}

	assert.Empty(t, store.WorkSessionID("s1"),
		"a session that only handshook and listed must have no work session")
}

// No client session in the context (internal calls, tests, odd transports) must
// be a no-op rather than a panic.
func TestWorkSession_NoClientSessionIsNoop(t *testing.T) {
	p, _ := newWorkSessionProxy(t)
	assert.Empty(t, p.markWorkIfToolCall(context.Background(), mcp.MethodToolsCall))
}
