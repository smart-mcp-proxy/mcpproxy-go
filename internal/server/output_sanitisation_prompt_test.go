package server

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func promptResult(msgs ...mcp.PromptMessage) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{Messages: msgs}
}

func textMsg(s string) mcp.PromptMessage {
	return mcp.PromptMessage{Role: mcp.RoleUser, Content: mcp.TextContent{Type: "text", Text: s}}
}

func firstPromptText(r *mcp.GetPromptResult) string {
	if r == nil || len(r.Messages) == 0 {
		return ""
	}
	if tc, ok := r.Messages[0].Content.(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func promptRedactCfg() *config.OutputSanitisationConfig {
	c := config.DefaultOutputSanitisationConfig()
	c.ResponseAction = "redact"
	return c
}

func promptBlockCfg() *config.OutputSanitisationConfig {
	c := config.DefaultOutputSanitisationConfig()
	c.ResponseAction = "block"
	return c
}

// TestApplyPromptResultSanitisation is the Finding F2 regression: prompt result
// content was forwarded with no redact/block/strip, unlike tool results. It must
// now redact secrets, block on critical secrets, and reach embedded text
// resources — reusing the tool path's detector + policy.
func TestApplyPromptResultSanitisation(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.OutputSanitisationConfig
		withDet     bool
		in          *mcp.GetPromptResult
		wantBlocked bool
		wantText    string // "" = don't assert exact
		wantNoKey   bool
	}{
		{
			name:     "default config is inert",
			cfg:      config.DefaultOutputSanitisationConfig(),
			withDet:  false,
			in:       promptResult(textMsg("hello " + awsKeyFixture)),
			wantText: "hello " + awsKeyFixture,
		},
		{
			name:      "redact masks secret in message",
			cfg:       promptRedactCfg(),
			withDet:   true,
			in:        promptResult(textMsg("key is " + awsKeyFixture + " ok")),
			wantNoKey: true,
		},
		{
			name:    "redact masks secret in embedded text resource",
			cfg:     promptRedactCfg(),
			withDet: true,
			in: promptResult(mcp.PromptMessage{
				Role: mcp.RoleUser,
				Content: mcp.EmbeddedResource{Type: "resource",
					Resource: mcp.TextResourceContents{URI: "x://y", Text: "creds " + awsKeyFixture}},
			}),
			wantNoKey: true,
		},
		{
			name:        "block on critical secret",
			cfg:         promptBlockCfg(),
			withDet:     true,
			in:          promptResult(textMsg("leak " + awsKeyFixture)),
			wantBlocked: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newSanProxy(t, tc.cfg, tc.withDet)
			got, blocked := p.applyPromptResultSanitisation(context.Background(), "srv", "pr", "req-test", tc.in)
			if blocked != tc.wantBlocked {
				t.Fatalf("blocked = %v, want %v", blocked, tc.wantBlocked)
			}
			if tc.wantBlocked {
				if got != nil {
					t.Fatalf("blocked result must be nil")
				}
				return
			}
			if tc.wantText != "" && firstPromptText(got) != tc.wantText {
				t.Fatalf("text = %q, want %q", firstPromptText(got), tc.wantText)
			}
			if tc.wantNoKey {
				var b strings.Builder
				for i := range got.Messages {
					switch c := got.Messages[i].Content.(type) {
					case mcp.TextContent:
						b.WriteString(c.Text)
					case mcp.EmbeddedResource:
						if tr, ok := c.Resource.(mcp.TextResourceContents); ok {
							b.WriteString(tr.Text)
						}
					}
				}
				if strings.Contains(b.String(), awsKeyFixture) {
					t.Fatalf("secret not redacted: %q", b.String())
				}
			}
		})
	}
}
