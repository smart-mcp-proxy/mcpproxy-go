package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 058 FR-028 keeps the client-facing Streamable HTTP surface on the legacy
// protocol era until the stateless work lands. These tests assert that on the
// wire, against a transport built from the same helper production uses, because
// the alternative — reading the option list back — cannot distinguish a pin that
// is applied from one that is merely constructed.
//
// Test names here deliberately avoid the substring "MCP": release-qa-gate.yml
// and e2e-tests.yml skip on that bare substring, so a test named for it runs in
// unit CI and is silently skipped by the race suite.

// legacyProtocolEra is the era the client-facing surface must negotiate while
// the FR-028 pin is in place.
const legacyProtocolEra = "2025-11-25"

// pinnedTestServer serves a bare tool over the same options every client-facing
// endpoint is built with. It intentionally does NOT go through the proxy: the
// subject under test is clientFacingStreamableOptions, and a full proxy would
// add slow setup without making the assertion stronger.
func pinnedTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := mcpserver.NewMCPServer("pin-probe", "1.0.0")
	srv.AddTool(
		mcp.NewTool("ping_probe", mcp.WithDescription("probe tool")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		},
	)

	streamable := mcpserver.NewStreamableHTTPServer(srv, clientFacingStreamableOptions()...)
	httpServer := httptest.NewServer(streamable)
	t.Cleanup(httpServer.Close)
	return httpServer
}

func connectProbeClient(t *testing.T, url string, opts ...client.ClientOption) *client.Client {
	t.Helper()

	httpTransport, err := transport.NewStreamableHTTP(url)
	require.NoError(t, err)

	c := client.NewClient(httpTransport, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, c.Start(ctx))
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	require.NoError(t, err)
	return c
}

// The load-bearing assertion. An UNPINNED v1.0.0 client is modern-first: it
// probes server/discover before falling back to a legacy handshake. Against the
// pinned surface it must land on the legacy era anyway.
func TestClientFacingSurfacePinsLegacyEra(t *testing.T) {
	httpServer := pinnedTestServer(t)

	c := connectProbeClient(t, httpServer.URL)

	assert.Equal(t, legacyProtocolEra, c.ProtocolVersion(),
		"an unpinned client must negotiate down to the legacy era while FR-028's pin is in place; "+
			"a modern negotiation here means the pin is missing from this transport")
}

// A session id proves the legacy path was actually taken. The modern era binds
// none, so this is the observable that distinguishes "negotiated legacy" from
// "reported legacy but served statelessly".
func TestClientFacingSurfaceBindsASessionIDUnderThePin(t *testing.T) {
	httpServer := pinnedTestServer(t)

	c := connectProbeClient(t, httpServer.URL)

	streamable, ok := c.GetTransport().(*transport.StreamableHTTP)
	require.True(t, ok, "expected a Streamable HTTP transport")
	assert.NotEmpty(t, streamable.GetSessionId(),
		"the legacy era binds a session id; an empty one means the modern, stateless path served this request")
}

// The pin must not break tool use — the whole point is that it is inert for
// existing clients. Asserted for a client pinned to EITHER era, because a
// client that asks for 2026-07-28 must still be served (by negotiating down)
// rather than refused.
func TestClientFacingSurfaceStillServesToolsUnderThePin(t *testing.T) {
	httpServer := pinnedTestServer(t)

	forEachEra(t, func(t *testing.T, era protocolEra) {
		c := connectProbeClient(t, httpServer.URL, era.options...)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
		require.NoError(t, err, "a client requesting the %s era must still be served while the pin holds", era.name)
		require.Len(t, tools.Tools, 1)
		assert.Equal(t, "ping_probe", tools.Tools[0].Name)
	})
}

// ─── Era-pinned harness (Spec 058, plan Phase 2) ────────────────────────────
//
// Every later acceptance test must run under BOTH eras, and must pin the era it
// claims to test. The reason is specific: while the FR-028 pin holds, an
// unpinned client negotiates DOWN, so an assertion of the form
// `c.ProtocolVersion() == "2026-07-28"` is satisfied by absence — the test
// passes without ever exercising the modern path. forEachEra removes that
// failure mode by making the era an explicit input and verifying the client
// actually reached it before the body runs.

type protocolEra struct {
	name    string
	version string
	options []client.ClientOption
}

// legacyEra and modernEra are the two eras a client may be pinned to.
func legacyEra() protocolEra {
	return protocolEra{
		name:    "legacy",
		version: mcp.LATEST_LEGACY_PROTOCOL_VERSION,
		options: []client.ClientOption{client.WithProtocolVersion(mcp.LATEST_LEGACY_PROTOCOL_VERSION)},
	}
}

func modernEra() protocolEra {
	return protocolEra{
		name:    "modern",
		version: mcp.LATEST_PROTOCOL_VERSION,
		options: []client.ClientOption{client.WithProtocolVersion(mcp.LATEST_PROTOCOL_VERSION)},
	}
}

// forEachEra runs body once per protocol era as a subtest.
//
// It does not assert the negotiated era itself — a server may legitimately be
// pinned, and refusing to run would make the harness unusable under FR-028.
// Instead it hands the era to the body, which decides what the negotiated
// version must be. Use requireEra when the body's assertions are only meaningful
// on a specific era.
func forEachEra(t *testing.T, body func(t *testing.T, era protocolEra)) {
	t.Helper()

	for _, era := range []protocolEra{legacyEra(), modernEra()} {
		era := era
		t.Run(era.name, func(t *testing.T) {
			body(t, era)
		})
	}
}

// requireEra fails the test unless the client actually negotiated the era it was
// pinned to. Call this before asserting era-specific behavior; without it, a
// server-side pin silently turns a modern-era test into a second legacy run.
//
// It takes require.TestingT rather than *testing.T so the harness can be
// verified against a recorder — see TestEraHarnessRejectsAnEraTheServerDidNotServe.
func requireEra(t require.TestingT, c *client.Client, era protocolEra) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}

	require.Equal(t, era.version, c.ProtocolVersion(),
		"this test asserts %s-era behavior but the connection negotiated %q; "+
			"the assertions below would pass without exercising the %s path",
		era.name, c.ProtocolVersion(), era.name)
}

// eraRecorder captures a testify failure without aborting the calling goroutine,
// so the harness's own guard can be asserted. A bare &testing.T{} cannot serve
// here: require calls FailNow, which runtime.Goexit()s and panics outside a real
// test goroutine.
type eraRecorder struct {
	failed  bool
	message string
}

func (r *eraRecorder) Errorf(format string, args ...interface{}) {
	r.message = fmt.Sprintf(format, args...)
}

func (r *eraRecorder) FailNow() { r.failed = true }

// Proves the harness bites, rather than trusting that it does. Against the
// pinned surface the legacy era is reachable and the modern era is not, so
// requireEra must accept the first and reject the second. A harness that
// silently accepted both would let every later modern-era test pass vacuously.
func TestEraHarnessRejectsAnEraTheServerDidNotServe(t *testing.T) {
	httpServer := pinnedTestServer(t)

	t.Run("legacy is reachable and accepted", func(t *testing.T) {
		era := legacyEra()
		c := connectProbeClient(t, httpServer.URL, era.options...)
		requireEra(t, c, era)
	})

	t.Run("modern is unreachable and rejected", func(t *testing.T) {
		era := modernEra()
		c := connectProbeClient(t, httpServer.URL, era.options...)

		recorder := &eraRecorder{}
		requireEra(recorder, c, era)

		require.True(t, recorder.failed,
			"requireEra must fail when the server pin prevented the requested era; "+
				"otherwise every modern-era test in this package can pass without testing anything")
		assert.Contains(t, recorder.message, legacyProtocolEra,
			"the failure should name the era actually negotiated, so the cause is obvious")
	})
}
