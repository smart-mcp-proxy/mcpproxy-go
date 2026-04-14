package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveInstructions_Default(t *testing.T) {
	result := resolveInstructions("")
	assert.Equal(t, defaultInstructions, result)
}

func TestResolveInstructions_Custom(t *testing.T) {
	custom := "Use retrieve_tools to find tools. Custom instructions."
	result := resolveInstructions(custom)
	assert.Equal(t, custom, result)
}

func TestDefaultInstructions_ContainsKeyTerms(t *testing.T) {
	assert.Contains(t, defaultInstructions, "retrieve_tools")
	assert.Contains(t, defaultInstructions, "search_servers")
	assert.Contains(t, defaultInstructions, "call_tool_read")
	assert.Contains(t, defaultInstructions, "upstream_servers")
}
