package secret

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvProvider_ReservedMCPProxyNamespace(t *testing.T) {
	p := NewEnvProvider(true)
	refs := []string{
		"MCPPROXY_API_KEY",
		"MCPPROXY_API_KEY_FILE",
		"MCPPROXY_CRED_KEY",
	}
	for _, name := range refs {
		_, err := p.Resolve(context.Background(), Ref{Type: "env", Name: name})
		require.ErrorIs(t, err, ErrReservedEnvVar, name)
	}

	// Wrong spelling must not be guarded (regression: MCPROXY_ typo).
	_, err := p.Resolve(context.Background(), Ref{Type: "env", Name: "MCPROXY_API_KEY"})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrReservedEnvVar)

	t.Setenv("MCPROXY_X", "value")
	val, err := p.Resolve(context.Background(), Ref{Type: "env", Name: "MCPROXY_X"})
	require.NoError(t, err)
	assert.Equal(t, "value", val)
}

func TestEnvProvider_ProxyScopeDoesNotGuard(t *testing.T) {
	t.Setenv("MCPPROXY_API_KEY", "from-env")
	p := NewEnvProvider(false)
	val, err := p.Resolve(context.Background(), Ref{Type: "env", Name: "MCPPROXY_API_KEY"})
	require.NoError(t, err)
	assert.Equal(t, "from-env", val)
}
