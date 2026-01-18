package configimport

import (
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		wantFormat     ConfigFormat
		wantConfidence string
		wantErr        bool
	}{
		{
			name: "claude_desktop_with_global_shortcut",
			content: `{
				"globalShortcut": "Ctrl+Shift+M",
				"mcpServers": {
					"github": {
						"command": "uvx",
						"args": ["mcp-server-github"]
					}
				}
			}`,
			wantFormat:     FormatClaudeDesktop,
			wantConfidence: "high",
		},
		{
			name: "claude_desktop_stdio_only",
			content: `{
				"mcpServers": {
					"github": {
						"command": "uvx",
						"args": ["mcp-server-github"]
					},
					"filesystem": {
						"command": "npx",
						"args": ["-y", "@modelcontextprotocol/server-filesystem"]
					}
				}
			}`,
			wantFormat:     FormatClaudeDesktop,
			wantConfidence: "medium",
		},
		{
			name: "claude_code_websocket",
			content: `{
				"mcpServers": {
					"myserver": {
						"type": "websocket",
						"url": "ws://localhost:8080"
					}
				}
			}`,
			wantFormat:     FormatClaudeCode,
			wantConfidence: "high",
		},
		{
			name: "claude_code_http",
			content: `{
				"mcpServers": {
					"myserver": {
						"type": "http",
						"url": "http://localhost:8080"
					}
				}
			}`,
			wantFormat:     FormatClaudeCode,
			wantConfidence: "medium",
		},
		{
			name: "cursor_streamable_http",
			content: `{
				"mcpServers": {
					"myserver": {
						"type": "streamable-http",
						"url": "http://localhost:8080/mcp"
					}
				}
			}`,
			wantFormat:     FormatCursor,
			wantConfidence: "high",
		},
		{
			name: "cursor_with_auth",
			content: `{
				"mcpServers": {
					"myserver": {
						"type": "sse",
						"url": "http://localhost:8080",
						"auth": {
							"CLIENT_ID": "my-client-id",
							"scopes": ["read", "write"]
						}
					}
				}
			}`,
			wantFormat:     FormatCursor,
			wantConfidence: "high",
		},
		{
			name: "cursor_with_envfile",
			content: `{
				"mcpServers": {
					"myserver": {
						"command": "node",
						"args": ["server.js"],
						"envFile": ".env"
					}
				}
			}`,
			wantFormat:     FormatCursor,
			wantConfidence: "medium",
		},
		{
			name: "gemini_with_httpurl",
			content: `{
				"mcpServers": {
					"myserver": {
						"httpUrl": "http://localhost:8080/mcp",
						"headers": {"Authorization": "Bearer token"}
					}
				}
			}`,
			wantFormat:     FormatGemini,
			wantConfidence: "high",
		},
		{
			name: "gemini_with_trust",
			content: `{
				"mcpServers": {
					"myserver": {
						"command": "node",
						"args": ["server.js"],
						"trust": true
					}
				}
			}`,
			wantFormat:     FormatGemini,
			wantConfidence: "medium",
		},
		{
			name: "gemini_with_include_tools",
			content: `{
				"mcpServers": {
					"myserver": {
						"command": "node",
						"args": ["server.js"],
						"includeTools": ["tool1", "tool2"]
					}
				}
			}`,
			wantFormat:     FormatGemini,
			wantConfidence: "medium",
		},
		{
			name: "gemini_with_mcp_global",
			content: `{
				"mcp": {
					"allowed": ["server1", "server2"]
				},
				"mcpServers": {
					"myserver": {
						"command": "node",
						"args": ["server.js"]
					}
				}
			}`,
			wantFormat:     FormatGemini,
			wantConfidence: "medium",
		},
		{
			name: "codex_toml",
			content: `
[mcp_servers.github]
command = "uvx"
args = ["mcp-server-github"]

[mcp_servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem"]
`,
			wantFormat:     FormatCodex,
			wantConfidence: "high",
		},
		{
			name: "generic_json_fallback_to_cursor",
			content: `{
				"mcpServers": {
					"myserver": {
						"type": "sse",
						"url": "http://localhost:8080"
					}
				}
			}`,
			wantFormat:     FormatCursor,
			wantConfidence: "low",
		},
		{
			name:    "invalid_json",
			content: `{invalid json`,
			wantErr: true,
		},
		{
			name:    "no_mcp_servers",
			content: `{"other": "config"}`,
			wantErr: true,
		},
		{
			name:    "empty_content",
			content: ``,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DetectFormat([]byte(tt.content))
			if (err != nil) != tt.wantErr {
				t.Errorf("DetectFormat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if result.Format != tt.wantFormat {
				t.Errorf("DetectFormat() format = %v, want %v", result.Format, tt.wantFormat)
			}
			if result.Confidence != tt.wantConfidence {
				t.Errorf("DetectFormat() confidence = %v, want %v", result.Confidence, tt.wantConfidence)
			}
		})
	}
}

func TestTryDetectTOML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    *DetectionResult
	}{
		{
			name: "valid_codex_toml",
			content: `
[mcp_servers.test]
command = "test"
`,
			want: &DetectionResult{
				Format:     FormatCodex,
				Confidence: "high",
			},
		},
		{
			name: "toml_without_mcp_servers",
			content: `
[other]
key = "value"
`,
			want: nil,
		},
		{
			name:    "invalid_toml",
			content: `not valid toml {`,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tryDetectTOML([]byte(tt.content))
			if tt.want == nil {
				if result != nil {
					t.Errorf("tryDetectTOML() = %v, want nil", result)
				}
				return
			}
			if result == nil {
				t.Errorf("tryDetectTOML() = nil, want %v", tt.want)
				return
			}
			if result.Format != tt.want.Format {
				t.Errorf("tryDetectTOML() format = %v, want %v", result.Format, tt.want.Format)
			}
		})
	}
}

func TestTryDetectJSON(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    *DetectionResult
	}{
		{
			name:    "invalid_json",
			content: `{invalid}`,
			want:    nil,
		},
		{
			name:    "json_without_mcpservers",
			content: `{"other": "value"}`,
			want:    nil,
		},
		{
			name: "json_with_mcpservers_not_object",
			content: `{"mcpServers": "not an object"}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tryDetectJSON([]byte(tt.content))
			if tt.want == nil {
				if result != nil {
					t.Errorf("tryDetectJSON() = %v, want nil", result)
				}
				return
			}
		})
	}
}
