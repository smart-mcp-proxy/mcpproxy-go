package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDirectToolName(t *testing.T) {
	tests := []struct {
		name       string
		directName string
		wantServer string
		wantTool   string
		wantOk     bool
	}{
		{
			name:       "simple tool name",
			directName: "github__create_issue",
			wantServer: "github",
			wantTool:   "create_issue",
			wantOk:     true,
		},
		{
			name:       "tool with underscores",
			directName: "my-server__my_tool_name",
			wantServer: "my-server",
			wantTool:   "my_tool_name",
			wantOk:     true,
		},
		{
			name:       "tool name contains double underscore",
			directName: "server__tool__with__double",
			wantServer: "server",
			wantTool:   "tool__with__double",
			wantOk:     true,
		},
		{
			name:       "no separator",
			directName: "noseparator",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "single underscore only",
			directName: "server_tool",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "empty string",
			directName: "",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "separator at start",
			directName: "__toolname",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "separator at end",
			directName: "server__",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "just separator",
			directName: "__",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, tool, ok := ParseDirectToolName(tt.directName)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantServer, server)
			assert.Equal(t, tt.wantTool, tool)
		})
	}
}

func TestFormatDirectToolName(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		toolName   string
		want       string
	}{
		{
			name:       "simple names",
			serverName: "github",
			toolName:   "create_issue",
			want:       "github__create_issue",
		},
		{
			name:       "server with hyphens",
			serverName: "my-server",
			toolName:   "get_user",
			want:       "my-server__get_user",
		},
		{
			name:       "tool with underscores",
			serverName: "api",
			toolName:   "list_all_items",
			want:       "api__list_all_items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDirectToolName(tt.serverName, tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDirectToolNameRoundTrip(t *testing.T) {
	// Test that formatting and parsing are inverse operations
	testCases := []struct {
		serverName string
		toolName   string
	}{
		{"github", "create_issue"},
		{"my-server", "list_repos"},
		{"api", "search_files"},
		{"db-server", "query_users_table"},
	}

	for _, tc := range testCases {
		formatted := FormatDirectToolName(tc.serverName, tc.toolName)
		parsedServer, parsedTool, ok := ParseDirectToolName(formatted)
		assert.True(t, ok, "should parse successfully for %s/%s", tc.serverName, tc.toolName)
		assert.Equal(t, tc.serverName, parsedServer)
		assert.Equal(t, tc.toolName, parsedTool)
	}
}

func TestDirectModeToolSeparator(t *testing.T) {
	assert.Equal(t, "__", DirectModeToolSeparator)
}
