package main

import (
	"testing"

	"github.com/spf13/cobra"
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
	assert.True(t, names["delete"], "should have 'delete' subcommand")
	assert.True(t, names["regenerate"], "should have 'regenerate' subcommand")
}

func TestGetTokenCommand_IncludesDelete(t *testing.T) {
	cmd := GetTokenCommand()

	// Find the delete subcommand
	var deleteCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "delete" {
			deleteCmd = sub
			break
		}
	}

	assert.NotNil(t, deleteCmd, "delete subcommand must exist")
	assert.Equal(t, "delete <name>", deleteCmd.Use)
	assert.Contains(t, deleteCmd.Short, "delete")
	assert.NotEmpty(t, deleteCmd.Long)
	assert.Contains(t, deleteCmd.Aliases, "rm", "delete should alias 'rm'")

	// Verify it requires exactly 1 argument
	assert.Error(t, deleteCmd.Args(deleteCmd, []string{}), "should reject zero args")
	assert.NoError(t, deleteCmd.Args(deleteCmd, []string{"my-token"}), "should accept one arg")
	assert.Error(t, deleteCmd.Args(deleteCmd, []string{"a", "b"}), "should reject two args")
}

func TestGetTokenCommand_IncludesRegenerate(t *testing.T) {
	cmd := GetTokenCommand()

	// Find the regenerate subcommand
	var regenCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "regenerate" {
			regenCmd = sub
			break
		}
	}

	assert.NotNil(t, regenCmd, "regenerate subcommand must exist")
	assert.Equal(t, "regenerate <name>", regenCmd.Use)
	assert.Contains(t, regenCmd.Short, "Regenerate")
	assert.NotEmpty(t, regenCmd.Long)

	// Verify it requires exactly 1 argument
	assert.Error(t, regenCmd.Args(regenCmd, []string{}), "should reject zero args")
	assert.NoError(t, regenCmd.Args(regenCmd, []string{"my-token"}), "should accept one arg")
	assert.Error(t, regenCmd.Args(regenCmd, []string{"a", "b"}), "should reject two args")
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
	assert.Equal(t, "", getMapString(m, "nonexistent")) // missing key
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

// GH #897 verification follow-up: the REST API wraps token responses in the
// standard envelope {"success":true,"data":{...}}, but the CLI table paths
// read top-level keys — so `token list` always printed "No agent tokens
// configured" and `token create` never displayed the minted token.
// parseTokenAPIResponse must unwrap the envelope (and tolerate the bare
// legacy shape).
func TestParseTokenAPIResponse(t *testing.T) {
	t.Run("unwraps success envelope", func(t *testing.T) {
		body := []byte(`{"success":true,"data":{"tokens":[{"name":"a"}],"token":"mcp_agt_x"}}`)
		result, err := parseTokenAPIResponse(body)
		assert.NoError(t, err)
		assert.Equal(t, "mcp_agt_x", result["token"])
		tokens, ok := result["tokens"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, tokens, 1)
	})

	t.Run("passes through bare legacy shape", func(t *testing.T) {
		body := []byte(`{"tokens":[],"token":"mcp_agt_y"}`)
		result, err := parseTokenAPIResponse(body)
		assert.NoError(t, err)
		assert.Equal(t, "mcp_agt_y", result["token"])
	})

	t.Run("invalid json errors", func(t *testing.T) {
		_, err := parseTokenAPIResponse([]byte("not json"))
		assert.Error(t, err)
	})
}
