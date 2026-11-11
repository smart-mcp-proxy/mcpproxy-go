package secret

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPProxyEnvGuard_ProxyScopeAllowsUpstreamRefuses(t *testing.T) {
	const ref = "${env:MCPPROXY_SCOPE_TEST_VAR}"
	t.Setenv("MCPPROXY_SCOPE_TEST_VAR", "ok")

	proxy := NewResolverWithOptions(ResolverOptions{
		Scope:      ScopeProxy,
		FilePolicy: NewWildcardFilePolicy(ScopeProxy),
	})
	got, err := proxy.ExpandSecretRefs(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "ok", got)

	upstream := NewResolverWithOptions(ResolverOptions{
		Scope:      ScopeUpstream,
		FilePolicy: NewWildcardFilePolicy(ScopeUpstream),
	})
	_, err = upstream.ExpandSecretRefs(context.Background(), ref)
	require.ErrorIs(t, err, ErrReservedEnvVar)
}
