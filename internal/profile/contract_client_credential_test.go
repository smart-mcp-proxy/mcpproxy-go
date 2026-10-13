package profile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClientCredentialContractSpellings pins the 108-c2 wire spellings shared
// by REST, CLI, SSE and the generated frontend contracts.
func TestClientCredentialContractSpellings(t *testing.T) {
	require.Equal(t, "unknown", string(CredentialStateUnknown))
	require.Equal(t, "binding_bypassable_without_auth", ErrorCodeBindingBypassable)
	require.Equal(t, "no_client_credential", ErrorCodeNoClientCredential)
	require.Equal(t, "require_mcp_auth", GuardFixRequireMCPAuth)
	require.Equal(t, "set_anonymous_profile", GuardFixSetAnonymousProfile)
	require.Equal(t,
		[]ChangeKind{"create", "update", "delete", "rename", "classify", "assign", "lock", "unlock", "forget", "rotate", "anonymous"},
		[]ChangeKind{ChangeCreate, ChangeUpdate, ChangeDelete, ChangeRename, ChangeClassify, ChangeAssign, ChangeLock, ChangeUnlock, ChangeForget, ChangeRotate, ChangeAnonymous})
	require.Equal(t, "finalized", RotationFinalized)
	require.Equal(t, "rolled_back", RotationRolledBack)
	require.Equal(t, "pending", RotationPending)
}
