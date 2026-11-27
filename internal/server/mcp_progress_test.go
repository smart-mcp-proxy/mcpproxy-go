package server

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/progressrelay"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
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
	relay := func(ctx context.Context, tok mcp.ProgressToken) progressrelay.Sink {
		return progressrelay.SinkFrom(p.withProgressRelay(ctx, progressRequest(tok), "srv", "tool", contracts.ContentTrustUntrusted))
	}
	assert.Nil(t, relay(sctx, nil))
	assert.Nil(t, relay(context.Background(), "tok"))
	assert.NotNil(t, relay(sctx, "tok"))
}

const progressTestSecret = "AKIA1234567890ABCDEF"

// progressMessage sends msg through the sink of one call under cfg and the
// tool's trust and returns the forwarded notification's fields.
func progressMessage(t *testing.T, cfg *config.OutputSanitisationConfig, trust, msg string) map[string]any {
	t.Helper()
	p := &MCPProxyServer{config: &config.Config{OutputSanitisation: cfg}}
	p.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
	sess := &progressSession{notes: make(chan mcp.JSONRPCNotification, 4)}
	sctx := mcpserver.NewMCPServer("t", "1").WithContext(context.Background(), sess)
	sink := progressrelay.SinkFrom(p.withProgressRelay(sctx, progressRequest(7), "srv", "tool", trust))
	require.NotNil(t, sink)
	total := 10.0
	require.True(t, sink(progressrelay.Update{Progress: 2, Total: &total, Message: msg}))
	f := (<-sess.notes).Params.AdditionalFields
	assert.Equal(t, 7, f["progressToken"], "the client's own token, as it sent it")
	assert.Equal(t, 2.0, f["progress"])
	assert.Equal(t, 10.0, f["total"])
	return f
}

func policy(action string, mutate ...func(*config.OutputSanitisationConfig)) *config.OutputSanitisationConfig {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = action
	for _, m := range mutate {
		m(cfg)
	}
	return cfg
}

// The message is treated like the text of the same tool's result.
func TestWithProgressRelay_MessageLikeToolResult(t *testing.T) {
	untrusted, trusted := contracts.ContentTrustUntrusted, contracts.ContentTrustTrusted
	secretMsg := "uploading with key " + progressTestSecret + " now"

	t.Run("spotlight default leaves it alone", func(t *testing.T) {
		assert.Equal(t, secretMsg, progressMessage(t, policy("spotlight"), untrusted, secretMsg)["message"])
	})
	t.Run("redact", func(t *testing.T) {
		got := progressMessage(t, policy("redact"), untrusted, secretMsg)["message"]
		assert.NotContains(t, got, progressTestSecret)
	})
	t.Run("block leaves the message out, keeps the numbers", func(t *testing.T) {
		_, has := progressMessage(t, policy("block"), untrusted, secretMsg)["message"]
		assert.False(t, has)
	})
	t.Run("block without a critical secret forwards it", func(t *testing.T) {
		assert.Equal(t, "step 2", progressMessage(t, policy("block"), untrusted, "step 2")["message"])
	})
	strip := func(c *config.OutputSanitisationConfig) { c.StripControlChars = true }
	t.Run("strip applies to untrusted tools only", func(t *testing.T) {
		ansi := "\x1b[31mred\x1b[0m"
		assert.Equal(t, "red", progressMessage(t, policy("spotlight", strip), untrusted, ansi)["message"])
		assert.Equal(t, ansi, progressMessage(t, policy("spotlight", strip), trusted, ansi)["message"])
	})
	spot := func(c *config.OutputSanitisationConfig) { c.SpotlightUntrusted = true }
	t.Run("spotlight frame for untrusted tools, after the cut", func(t *testing.T) {
		long := strings.Repeat("a", progressrelay.MaxMessageBytes+100)
		got, _ := progressMessage(t, policy("spotlight", spot), untrusted, long)["message"].(string)
		assert.Equal(t, security.SpotlightUntrusted(long[:progressrelay.MaxMessageBytes], "srv", "tool"), got)
		assert.Equal(t, "plain", progressMessage(t, policy("spotlight", spot), trusted, "plain")["message"])
	})
	t.Run("a secret across the cut is redacted first", func(t *testing.T) {
		msg := strings.Repeat("a", progressrelay.MaxMessageBytes-10) + " " + progressTestSecret
		got, _ := progressMessage(t, policy("redact"), untrusted, msg)["message"].(string)
		assert.NotContains(t, got, "AKIA123456")
		assert.LessOrEqual(t, len(got), progressrelay.MaxMessageBytes)
	})
}
