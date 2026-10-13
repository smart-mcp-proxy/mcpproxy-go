package storage

import (
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAgentTokenScopeRevalidatedAndNarrowOnly(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	token, raw := makeTestToken("owned")
	token.UserID = "owner"
	require.NoError(t, mgr.CreateAgentToken(token, raw, testHMACKey))
	current := []string{"github"}
	// Spec 107 T076: the single owner resolver replaces the scope resolver;
	// Entitled is the already-narrowed grant (here: whatever `current` says).
	mgr.SetAgentTokenOwnerResolver(func(owner string, grant []string) (OwnerResolution, error) {
		require.Equal(t, "owner", owner)
		return OwnerResolution{Active: true, UserID: owner, Entitled: current}, nil
	})
	got, err := mgr.ValidateAgentToken(raw, testHMACKey)
	require.NoError(t, err)
	require.Equal(t, []string{"github"}, got.AllowedServers)
	current = []string{"ungranted"}
	got, err = mgr.ValidateAgentToken(raw, testHMACKey)
	require.NoError(t, err)
	require.Empty(t, got.AllowedServers)
	stored, err := mgr.GetAgentTokenByOwnerAndName("owner", "owned")
	require.NoError(t, err)
	require.Equal(t, token.AllowedServers, stored.AllowedServers)
	mgr.SetAgentTokenOwnerResolver(func(string, []string) (OwnerResolution, error) {
		return OwnerResolution{}, errors.New("unavailable")
	})
	_, err = mgr.ValidateAgentToken(raw, testHMACKey)
	require.Error(t, err)
	operator, operatorRaw := makeTestToken("operator")
	require.NoError(t, mgr.CreateAgentToken(operator, operatorRaw, testHMACKey))
	_, err = mgr.ValidateAgentToken(operatorRaw, testHMACKey)
	require.NoError(t, err)
}
