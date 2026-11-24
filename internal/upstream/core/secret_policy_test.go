package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewClientWithOptions_PolicyDenialFailsClient(t *testing.T) {
	srv := &config.ServerConfig{
		Name:    "blocked",
		Command: "echo",
		Env:     map[string]string{"K": "${env:MCPPROXY_API_KEY}"},
	}
	r := secret.NewResolverWithOptions(secret.ResolverOptions{
		Scope:      secret.ScopeUpstream,
		FilePolicy: secret.NewWildcardFilePolicy(secret.ScopeUpstream),
	})
	_, err := NewClientWithOptions("id", srv, zap.NewNop(), nil, &config.Config{}, nil, false, r)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUpstreamSecretPolicy)
	var pe *UpstreamSecretPolicyError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, secret.ErrReservedEnvVar.Error(), pe.Public)
}
