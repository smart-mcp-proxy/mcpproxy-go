package runtime

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// seedClientCredentials mints n custom client credentials bound to the "ro"
// profile, none expiring soon.
func seedClientCredentials(t testing.TB, rt *Runtime, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		raw := fmt.Sprintf("mcp_cli_attn108_bench_%09d_secret", i)
		_, err := rt.StorageManager().MintClientCredential(fmt.Sprintf("bench-%d", i), raw, funnelKey, auth.ProfileModeLocked, "ro", time.Now().Add(200*24*time.Hour))
		require.NoError(t, err)
	}
}

// TestAttention108ClientWarningInputBudget pins R3: the periodic client-warning
// read (one credential-store scan plus an onboarding-state read) stays far under
// the SC-011 budget with the deployment cap (auth.MaxTokens = 100) of client
// credentials; the plan's 500-token case cannot exist, the store refuses it.
func TestAttention108ClientWarningInputBudget(t *testing.T) {
	rt := newFunnelRuntime(t)
	seedClientCredentials(t, rt, auth.MaxTokens)

	const runs = 20
	start := time.Now()
	for i := 0; i < runs; i++ {
		rt.attentionClientState()
	}
	per := time.Since(start) / runs
	t.Logf("attentionClientState with auth.MaxTokens client credentials: %s per read", per)
	require.Less(t, per, 20*time.Millisecond)
}
