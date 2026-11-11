package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 123 T031: every key-resolution failure exits 4 through the typed
// *config.StartupError path of classifyError, not through its string
// heuristics.
func TestClassifyError_OAuthTokenKeyFailuresExit4(t *testing.T) {
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	missing := filepath.Join(t.TempDir(), "absent")

	for _, field := range []string{
		"${file:" + missing + "}", // unresolvable
		"not-a-key",               // malformed
		"   ",                     // empty
	} {
		_, _, err := config.ResolveOAuthTokenKey(&config.Config{OAuthTokenEncryptionKey: field})
		require.Error(t, err)

		// Wrapped the way the storage-open sites wrap it.
		wrapped := fmt.Errorf("failed to create server: %w", err)
		msg := strings.ToLower(wrapped.Error())
		// Premise: the string heuristics alone would not classify it as a
		// config error, so only the typed path can produce 4.
		require.False(t, strings.Contains(msg, "config"), "premise: message must not mention config: %q", msg)
		require.False(t, strings.Contains(msg, "invalid configuration"))

		assert.Equal(t, ExitCodeConfigError, classifyError(wrapped), "field %q", field)
		assert.Equal(t, 4, classifyError(wrapped))
	}

	t.Setenv(config.EnvOAuthTokenKey, "a")
	t.Setenv(config.EnvOAuthTokenKeyFile, missing)
	_, _, err := config.ResolveOAuthTokenKey(&config.Config{})
	require.Error(t, err)
	assert.Equal(t, ExitCodeConfigError, classifyError(fmt.Errorf("open storage: %w", err)))
}
