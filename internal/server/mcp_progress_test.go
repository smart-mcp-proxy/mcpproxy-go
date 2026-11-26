package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/progressrelay"
)

type progressSession struct {
	notes chan mcp.JSONRPCNotification
}

func (s *progressSession) SessionID() string                                   { return "s1" }
func (s *progressSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.notes }
func (s *progressSession) Initialize()                                         {}
func (s *progressSession) Initialized() bool                                   { return true }

func progressRequest(tok mcp.ProgressToken) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	if tok != nil {
		req.Params.Meta = &mcp.Meta{ProgressToken: tok}
	}
	return req
}

// GH #1967: a call whose client sent no token, or that has no session to
// send to, forwards nothing.
func TestWithProgressRelay_NoTokenOrNoSession(t *testing.T) {
	p := &MCPProxyServer{config: &config.Config{}}
	sess := &progressSession{notes: make(chan mcp.JSONRPCNotification, 4)}
	sctx := mcpserver.NewMCPServer("t", "1").WithContext(context.Background(), sess)

	assert.Nil(t, progressrelay.SinkFrom(p.withProgressRelay(sctx, progressRequest(nil))))
	assert.Nil(t, progressrelay.SinkFrom(p.withProgressRelay(context.Background(), progressRequest("tok"))))
	assert.NotNil(t, progressrelay.SinkFrom(p.withProgressRelay(sctx, progressRequest("tok"))))
}

// The upstream-written message passes the output policy like tool output:
// redacted under redact, dropped (numbers kept) under block.
func TestWithProgressRelay_MessageThroughOutputPolicy(t *testing.T) {
	const secretMsg = "uploading with key AKIA1234567890ABCDEF now"
	for _, tc := range []struct {
		action string
		check  func(t *testing.T, msg any, has bool)
	}{
		{"spotlight", func(t *testing.T, msg any, has bool) {
			assert.True(t, has)
			assert.Equal(t, secretMsg, msg)
		}},
		{"redact", func(t *testing.T, msg any, has bool) {
			require.True(t, has)
			assert.NotContains(t, msg, "AKIA1234567890ABCDEF")
		}},
		{"block", func(t *testing.T, _ any, has bool) {
			assert.False(t, has, "a withheld message is left out")
		}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			cfg := config.DefaultOutputSanitisationConfig()
			cfg.ResponseAction = tc.action
			p := &MCPProxyServer{config: &config.Config{OutputSanitisation: cfg}}
			sess := &progressSession{notes: make(chan mcp.JSONRPCNotification, 4)}
			sctx := mcpserver.NewMCPServer("t", "1").WithContext(context.Background(), sess)
			sink := progressrelay.SinkFrom(p.withProgressRelay(sctx, progressRequest(7)))
			require.NotNil(t, sink)
			total := 10.0
			sink(progressrelay.Update{Progress: 2, Total: &total, Message: secretMsg})
			n := <-sess.notes
			f := n.Params.AdditionalFields
			assert.Equal(t, 7, f["progressToken"], "the client's own token, as it sent it")
			assert.Equal(t, 2.0, f["progress"])
			assert.Equal(t, 10.0, f["total"])
			msg, has := f["message"]
			tc.check(t, msg, has)
		})
	}
}
