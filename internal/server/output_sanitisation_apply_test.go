package server

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// awsKeyFixture is the same fake AWS key the security package's Redact tests
// use: matches aws_access_key, not a known example, critical severity.
const awsKeyFixture = "AKIA1234567890ABCDEF"

func newSanProxy(t *testing.T, cfg *config.OutputSanitisationConfig, withDetector bool) *MCPProxyServer {
	t.Helper()
	p := &MCPProxyServer{
		config: &config.Config{OutputSanitisation: cfg},
		logger: zap.NewNop(),
	}
	if withDetector {
		p.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
	}
	return p
}

func textResult(parts ...mcp.Content) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: parts}
}

func firstText(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func spotlightOnCfg() *config.OutputSanitisationConfig {
	c := config.DefaultOutputSanitisationConfig()
	c.SpotlightUntrusted = true
	return c
}

func TestApplyOutputSanitisation_DefaultIsInert(t *testing.T) {
	// Fully opt-in: default config leaves untrusted output byte-identical.
	p := newSanProxy(t, config.DefaultOutputSanitisationConfig(), false)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "hello world"})
	if block := p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustUntrusted, fwd); block != nil {
		t.Fatalf("default config must not block")
	}
	if got := firstText(fwd); got != "hello world" {
		t.Fatalf("default config must leave untrusted output unchanged, got %q", got)
	}
}

func TestSpotlightForwarded_UntrustedSpotlighted(t *testing.T) {
	p := newSanProxy(t, spotlightOnCfg(), false)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "hello world"})
	p.spotlightForwarded("srv", "tool", contracts.ContentTrustUntrusted, fwd)
	got := firstText(fwd)
	if !strings.Contains(got, "«untrusted:srv/tool»") || !strings.Contains(got, "hello world") {
		t.Fatalf("untrusted text not spotlighted: %q", got)
	}
}

func TestSpotlightForwarded_TrustedUnchanged(t *testing.T) {
	p := newSanProxy(t, spotlightOnCfg(), false)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "hello world"})
	p.spotlightForwarded("srv", "tool", contracts.ContentTrustTrusted, fwd)
	if got := firstText(fwd); got != "hello world" {
		t.Fatalf("trusted text must not be spotlighted, got %q", got)
	}
}

func TestApplyOutputSanitisation_TrustedUnchanged(t *testing.T) {
	p := newSanProxy(t, config.DefaultOutputSanitisationConfig(), false)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "hello world"})
	p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustTrusted, fwd)
	if got := firstText(fwd); got != "hello world" {
		t.Fatalf("trusted text must be byte-identical, got %q", got)
	}
}

func TestApplyOutputSanitisation_NonTextPreserved(t *testing.T) {
	// Use redact (an active mutating path) so the content loop actually runs.
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = "redact"
	p := newSanProxy(t, cfg, true)
	img := mcp.ImageContent{Type: "image", Data: "BASE64DATA", MIMEType: "image/png"}
	fwd := textResult(mcp.TextContent{Type: "text", Text: "key=" + awsKeyFixture}, img)
	p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustUntrusted, fwd)
	// image block must be byte-identical and still present (FR-B5)
	gotImg, ok := fwd.Content[1].(mcp.ImageContent)
	if !ok || gotImg.Data != "BASE64DATA" || gotImg.MIMEType != "image/png" {
		t.Fatalf("non-text block mutated: %+v", fwd.Content[1])
	}
}

func TestApplyOutputSanitisation_RedactMasksSecret(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = "redact"
	p := newSanProxy(t, cfg, true)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "key=" + awsKeyFixture + " end"})
	p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustTrusted, fwd)
	got := firstText(fwd)
	if strings.Contains(got, awsKeyFixture) {
		t.Fatalf("secret not redacted: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:cloud_credentials]") {
		t.Fatalf("expected category placeholder, got %q", got)
	}
}

func TestApplyOutputSanitisation_BlockOnCritical(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = "block"
	p := newSanProxy(t, cfg, true)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "leaked " + awsKeyFixture})
	block := p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustUntrusted, fwd)
	if block == nil || !block.IsError {
		t.Fatalf("block mode + critical detection must return an error result")
	}
}

func TestApplyOutputSanitisation_BlockNonCriticalPasses(t *testing.T) {
	cfg := spotlightOnCfg()
	cfg.ResponseAction = "block"
	p := newSanProxy(t, cfg, true)
	fwd := textResult(mcp.TextContent{Type: "text", Text: "nothing sensitive here"})
	if block := p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustUntrusted, fwd); block != nil {
		t.Fatalf("block mode without critical detection must pass through")
	}
	// untrusted output is still spotlighted post-forward in block mode
	p.spotlightForwarded("srv", "tool", contracts.ContentTrustUntrusted, fwd)
	if got := firstText(fwd); !strings.Contains(got, "«untrusted:srv/tool»") {
		t.Fatalf("untrusted output should be spotlighted in block mode, got %q", got)
	}
}

func TestApplyOutputSanitisation_StripControlChars(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.StripControlChars = true
	p := newSanProxy(t, cfg, false)
	const bidiOverride = '\u202e' // RIGHT-TO-LEFT OVERRIDE
	fwd := textResult(mcp.TextContent{Type: "text", Text: "clean" + string(bidiOverride) + "text"})
	p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustUntrusted, fwd)
	got := firstText(fwd)
	if strings.ContainsRune(got, bidiOverride) {
		t.Fatalf("bidi override not stripped: %q", got)
	}
}
