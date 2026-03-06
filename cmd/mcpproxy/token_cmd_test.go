package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetTokenCommand(t *testing.T) {
	cmd := GetTokenCommand()
	assert.Equal(t, "token", cmd.Use)
	assert.NotEmpty(t, cmd.Short)

	// Check subcommands
	subCmds := cmd.Commands()
	names := make(map[string]bool)
	for _, sub := range subCmds {
		names[sub.Name()] = true
	}

	assert.True(t, names["create"], "should have 'create' subcommand")
	assert.True(t, names["list"], "should have 'list' subcommand")
	assert.True(t, names["show"], "should have 'show' subcommand")
	assert.True(t, names["revoke"], "should have 'revoke' subcommand")
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"github,gitlab", []string{"github", "gitlab"}},
		{" github , gitlab ", []string{"github", "gitlab"}},
		{"read,write,destructive", []string{"read", "write", "destructive"}},
		{"*", []string{"*"}},
		{"", []string{}},
	}

	for _, tt := range tests {
		result := splitAndTrim(tt.input)
		assert.Equal(t, tt.expected, result, "splitAndTrim(%q)", tt.input)
	}
}

func TestGetMapString(t *testing.T) {
	m := map[string]interface{}{
		"name":   "deploy-bot",
		"count":  42,
		"nested": map[string]interface{}{"key": "value"},
	}

	assert.Equal(t, "deploy-bot", getMapString(m, "name"))
	assert.Equal(t, "", getMapString(m, "count"))       // not a string
	assert.Equal(t, "", getMapString(m, "nonexistent"))  // missing key
}

func TestJoinInterfaceSlice(t *testing.T) {
	m := map[string]interface{}{
		"servers": []interface{}{"github", "gitlab", "bitbucket"},
	}

	assert.Equal(t, "github,gitlab,bitbucket", joinInterfaceSlice(m, "servers", 0))
	// String is exactly 23 chars, so maxLen=23 doesn't truncate
	assert.Equal(t, "github,gitlab,bitbucket", joinInterfaceSlice(m, "servers", 23))
	// maxLen=20 triggers truncation: result[:17] + "..." = 20 chars
	assert.Equal(t, "github,gitlab,bit...", joinInterfaceSlice(m, "servers", 20))
	assert.Equal(t, "", joinInterfaceSlice(m, "missing", 0))
}

func TestTokenCreateCmd_RequiredFlags(t *testing.T) {
	cmd := newTokenCreateCmd()

	// Verify required flags are defined
	nameFlag := cmd.Flags().Lookup("name")
	assert.NotNil(t, nameFlag, "should have --name flag")

	serversFlag := cmd.Flags().Lookup("servers")
	assert.NotNil(t, serversFlag, "should have --servers flag")

	permsFlag := cmd.Flags().Lookup("permissions")
	assert.NotNil(t, permsFlag, "should have --permissions flag")

	expiresFlag := cmd.Flags().Lookup("expires")
	assert.NotNil(t, expiresFlag, "should have --expires flag")
	assert.Equal(t, "30d", expiresFlag.DefValue, "default expires should be 30d")
}
