package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Spec 116 PR2b (#1607) review r8: a login completion of an old incarnation
// (recorded in memory for its own binding) must not let a same-name
// replacement bypass the automatic OAuth deferral (connection_oauth.go's
// isDeferOAuthForTray gates) and open a browser authorization unasked.
func TestOldIncarnationCompletionDoesNotBypassReplacementDeferral(t *testing.T) {
	// The completion record is process-global: a fresh name per run.
	name := fmt.Sprintf("r8-defer-replacement-%d", time.Now().UnixNano())
	old := commitview.CredentialBinding{Incarnation: 1, OAuthEpoch: 1}
	repl := commitview.CredentialBinding{Incarnation: 3, OAuthEpoch: 3}

	c, err := NewClient(name, &config.ServerConfig{Name: name, URL: "https://acme.example/mcp", Protocol: "http", Enabled: true},
		zap.NewNop(), nil, &config.Config{}, nil, nil)
	require.NoError(t, err)
	c.SetCredentialBinding(repl)

	require.True(t, c.isDeferOAuthForTray(context.Background()), "precondition: automatic attempt defers")

	oauth.GetTokenStoreManager().MarkOAuthCompletedBound(name, old)
	assert.True(t, c.isDeferOAuthForTray(context.Background()),
		"the old incarnation's completion must not bypass the replacement's deferral")

	oauth.GetTokenStoreManager().MarkOAuthCompletedBound(name, repl)
	assert.False(t, c.isDeferOAuthForTray(context.Background()),
		"the replacement's own completion still allows the flow (unchanged behaviour)")
}
