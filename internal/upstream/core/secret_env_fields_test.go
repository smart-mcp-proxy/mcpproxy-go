package core

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/require"
)

func TestUpstreamEnvGuard_AllServerFields(t *testing.T) {
	ref := "${env:MCPPROXY_API_KEY}"
	r := secret.NewResolverWithOptions(secret.ResolverOptions{
		Scope:      secret.ScopeUpstream,
		FilePolicy: secret.NewWildcardFilePolicy(secret.ScopeUpstream),
	})

	srv := &config.ServerConfig{
		Name:    "test",
		Command: ref,
		URL:     ref,
		Args:    []string{ref},
		Env:     map[string]string{"K": ref},
		Headers: map[string]string{"H": ref},
		OAuth: &config.OAuthConfig{
			ClientSecret: ref,
		},
		Isolation: &config.IsolationConfig{
			WorkingDir: ref,
		},
	}
	errs := r.ExpandStructSecretsCollectErrors(context.Background(), srv)
	require.NotEmpty(t, errs)
	for _, e := range errs {
		require.ErrorIs(t, e.Err, secret.ErrReservedEnvVar, e.FieldPath)
	}
}
