package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifier_InternalTools(t *testing.T) {
	c := NewClassifier(nil)

	tests := []struct {
		toolName       string
		wantClass      Classification
		wantConfidence float64
		wantRead       bool
		wantExfil      bool
	}{
		{"Read", ClassInternal, 0.9, true, false},
		{"Write", ClassInternal, 0.9, false, false},
		{"Edit", ClassInternal, 0.9, false, false},
		{"Glob", ClassInternal, 0.9, true, false},
		{"Grep", ClassInternal, 0.9, true, false},
		{"NotebookEdit", ClassInternal, 0.9, false, false},
		{"WebFetch", ClassExternal, 0.9, false, true},
		{"WebSearch", ClassExternal, 0.9, false, true},
		{"Bash", ClassHybrid, 0.8, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := c.Classify("", tt.toolName)
			assert.Equal(t, tt.wantClass, result.Classification, "classification")
			assert.Equal(t, "builtin", result.Method, "method should be builtin for known tools")
			assert.GreaterOrEqual(t, result.Confidence, tt.wantConfidence, "confidence")
			assert.Equal(t, tt.wantRead, result.CanReadData, "CanReadData")
			assert.Equal(t, tt.wantExfil, result.CanExfiltrate, "CanExfiltrate")
		})
	}
}

func TestClassifier_ServerNameHeuristics(t *testing.T) {
	c := NewClassifier(nil)

	tests := []struct {
		name      string
		server    string
		wantClass Classification
	}{
		// Internal patterns: database, filesystem, storage
		{"postgres database", "postgres-db", ClassInternal},
		{"mysql server", "mysql-data", ClassInternal},
		{"redis cache", "redis-cache", ClassInternal},
		{"sqlite storage", "sqlite-storage", ClassInternal},
		{"filesystem server", "filesystem-tools", ClassInternal},
		{"git server", "git-ops", ClassInternal},
		{"github server", "github", ClassInternal},
		{"gitlab server", "gitlab-ci", ClassInternal},

		// External patterns: communication, web, messaging
		{"slack notifications", "slack-notifications", ClassExternal},
		{"email sender", "email-service", ClassExternal},
		{"discord bot", "discord-bot", ClassExternal},
		{"webhook handler", "webhook-handler", ClassExternal},
		{"smtp server", "smtp-mailer", ClassExternal},
		{"twilio sms", "twilio-sms", ClassExternal},
		{"telegram bot", "telegram-alerts", ClassExternal},

		// Hybrid patterns: cloud, compute
		{"aws lambda", "aws-lambda", ClassHybrid},
		{"docker runner", "docker-runner", ClassHybrid},

		// Unknown: no matching pattern
		{"unknown server", "my-custom-server", ClassUnknown},
		{"another unknown", "foobar", ClassUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.Classify(tt.server, "some_tool")
			assert.Equal(t, tt.wantClass, result.Classification, "classification for server %q", tt.server)
			if tt.wantClass != ClassUnknown {
				assert.Equal(t, "heuristic", result.Method, "method should be heuristic")
				assert.GreaterOrEqual(t, result.Confidence, 0.8, "confidence should be >= 0.8")
			}
		})
	}
}

func TestClassifier_ConfigOverrides(t *testing.T) {
	overrides := map[string]string{
		"my-private-slack": "internal",
		"public-github":    "external",
		"custom-hybrid":    "hybrid",
	}
	c := NewClassifier(overrides)

	tests := []struct {
		name      string
		server    string
		wantClass Classification
	}{
		{"override slack to internal", "my-private-slack", ClassInternal},
		{"override github to external", "public-github", ClassExternal},
		{"override to hybrid", "custom-hybrid", ClassHybrid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.Classify(tt.server, "any_tool")
			assert.Equal(t, tt.wantClass, result.Classification)
			assert.Equal(t, "config", result.Method, "method should be config for overrides")
			assert.Equal(t, 1.0, result.Confidence, "config overrides should have confidence 1.0")
		})
	}
}

func TestClassifier_MCPToolNamespacing(t *testing.T) {
	overrides := map[string]string{
		"github": "internal",
	}
	c := NewClassifier(overrides)

	tests := []struct {
		name      string
		server    string
		toolName  string
		wantClass Classification
		wantMethod string
	}{
		// mcp__<server>__<tool> format should look up the server
		{"mcp namespaced tool", "", "mcp__github__get_file", ClassInternal, "config"},
		// Regular server:tool format
		{"colon namespaced tool", "github", "get_file", ClassInternal, "config"},
		// Unknown MCP server
		{"unknown mcp server", "", "mcp__unknown_server__do_thing", ClassUnknown, "heuristic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.Classify(tt.server, tt.toolName)
			assert.Equal(t, tt.wantClass, result.Classification)
			assert.Equal(t, tt.wantMethod, result.Method)
		})
	}
}

func TestClassifier_CapabilityFlags(t *testing.T) {
	c := NewClassifier(nil)

	tests := []struct {
		name          string
		server        string
		toolName      string
		wantRead      bool
		wantExfiltrate bool
	}{
		// Internal tools that can read data
		{"Read can read", "", "Read", true, false},
		{"Glob can read", "", "Glob", true, false},
		{"Grep can read", "", "Grep", true, false},

		// External tools that can exfiltrate
		{"WebFetch can exfiltrate", "", "WebFetch", false, true},
		{"WebSearch can exfiltrate", "", "WebSearch", false, true},

		// Hybrid tools can do both
		{"Bash can do both", "", "Bash", true, true},

		// Internal tools that write (not read)
		{"Write cannot read", "", "Write", false, false},
		{"Edit cannot read", "", "Edit", false, false},

		// Server heuristic: slack is external, can exfiltrate
		{"slack can exfiltrate", "slack-bot", "send_message", false, true},

		// Server heuristic: postgres is internal, can read
		{"postgres can read", "postgres-db", "query", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.Classify(tt.server, tt.toolName)
			assert.Equal(t, tt.wantRead, result.CanReadData, "CanReadData for %s/%s", tt.server, tt.toolName)
			assert.Equal(t, tt.wantExfiltrate, result.CanExfiltrate, "CanExfiltrate for %s/%s", tt.server, tt.toolName)
		})
	}
}

func TestClassifier_BuiltinToolPrecedence(t *testing.T) {
	// Even with a server name, if the tool is a known builtin, use builtin classification
	c := NewClassifier(nil)

	result := c.Classify("some-server", "Read")
	assert.Equal(t, ClassInternal, result.Classification, "builtin tool should override server heuristic")
	assert.Equal(t, "builtin", result.Method)
}

func TestClassifier_EmptyInputs(t *testing.T) {
	c := NewClassifier(nil)

	t.Run("empty server and tool", func(t *testing.T) {
		result := c.Classify("", "")
		assert.Equal(t, ClassUnknown, result.Classification)
	})

	t.Run("empty server with known tool", func(t *testing.T) {
		result := c.Classify("", "Read")
		assert.Equal(t, ClassInternal, result.Classification)
	})
}

func TestClassifier_ReasonProvided(t *testing.T) {
	c := NewClassifier(nil)

	result := c.Classify("", "Read")
	require.NotEmpty(t, result.Reason, "reason should be provided")
}

func TestClassifier_ImplementsInterface(t *testing.T) {
	// Verify Classifier satisfies the ServerClassifier interface pattern
	c := NewClassifier(nil)
	var _ interface {
		Classify(serverName, toolName string) ClassificationResult
	} = c
}
