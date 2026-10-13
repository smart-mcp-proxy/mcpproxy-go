package profile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 115 T008: the credential lifecycle change kinds and the `credentials`
// tool's error codes have fixed wire spellings (contracts/errors.md).
func TestCredentialContractSpellings(t *testing.T) {
	require.Equal(t, ChangeKind("issue"), ChangeIssue)
	require.Equal(t, ChangeKind("revoke"), ChangeRevoke)
	require.Equal(t,
		[]string{"secret_in_argument", "arguments_too_large", "unknown_operation", "missing_argument",
			"invalid_argument", "profile_required", "unknown_profile", "invalid_expiry", "identity_exists",
			"reserved_identity", "identity_not_found", "token_limit_reached", "read_only_mode",
			"management_disabled", "unsupported_edition", "credentials_unavailable"},
		[]string{CredentialErrorCodeSecretInArgument, CredentialErrorCodeArgumentsTooLarge,
			CredentialErrorCodeUnknownOperation, CredentialErrorCodeMissingArgument,
			CredentialErrorCodeInvalidArgument, CredentialErrorCodeProfileRequired,
			CredentialErrorCodeUnknownProfile, CredentialErrorCodeInvalidExpiry,
			CredentialErrorCodeIdentityExists, CredentialErrorCodeReservedIdentity,
			CredentialErrorCodeIdentityNotFound, CredentialErrorCodeTokenLimitReached,
			CredentialErrorCodeReadOnlyMode, CredentialErrorCodeManagementDisabled,
			CredentialErrorCodeUnsupportedEdition, CredentialErrorCodeCredentialsUnavailable})
}
