package diagnostics

import "testing"

// TestIsPermanent_UnknownUnclassifiedIsRetryable pins the hard constraint from
// GH #1145: an error we could not classify is NOT evidence that retrying is
// futile. Marking the fallback code permanent would park every server whose
// stderr we do not yet recognise.
func TestIsPermanent_UnknownUnclassifiedIsRetryable(t *testing.T) {
	if IsPermanent(UnknownUnclassified) {
		t.Fatalf("MCPX_UNKNOWN_UNCLASSIFIED must stay retryable")
	}
	if IsPermanent(Code("MCPX_NOT_A_REAL_CODE")) {
		t.Fatalf("an unregistered code must default to retryable")
	}
	if IsPermanent(Code("")) {
		t.Fatalf("the empty code must default to retryable")
	}
}

// TestPermanentCodes_AreRegistered pins the declared permanent set. Every entry
// is a config/toolchain fact that cannot change without a human editing
// something, so an automatic retry can never succeed.
func TestPermanentCodes_AreRegistered(t *testing.T) {
	permanent := []Code{
		STDIOSpawnENOENT,
		STDIOSpawnEACCES,
		STDIOSpawnExecFormat,
		DockerCLINotFound,
		DockerExecNotFound,
		DockerOCIRuntime,
		ConfigInvalidCommand,
		ConfigParseError,
		HTTPLegacySSE,
	}
	for _, c := range permanent {
		if !Has(c) {
			t.Fatalf("%s is not registered in the catalog", c)
		}
		if !IsPermanent(c) {
			t.Errorf("%s should classify as permanent (retrying it cannot succeed)", c)
		}
	}
}

// TestTransientCodes_StayRetryable is the anti-table: each of these heals
// without a config edit (daemon start, registry recovery, keyring write, a
// flaky install), so escalating one to permanent would strand a server that
// would have recovered on its own. A future "helpful" escalation trips here.
func TestTransientCodes_StayRetryable(t *testing.T) {
	transient := []Code{
		// Docker environment, not configuration.
		DockerDaemonDown,
		DockerImagePullFailed,
		DockerNoPermission,
		DockerSnapAppArmor,
		// A secret can appear in the keyring with NO config change, so a
		// terminal park would strand a user who did exactly what we asked.
		ConfigMissingSecret,
		ConfigDeprecatedField,
		// The generic "process died" bucket: an OOM or a flaky npx install
		// lands here alongside genuine config faults.
		STDIOExitNonzero,
		STDIOExitBeforeInitialize,
		STDIOHandshakeTimeout,
		STDIOHandshakeInvalid,
		// OAuth codes are paced by their own coarse ladder and are explicitly
		// user-actionable.
		OAuthLoginRequired,
		OAuthReauthRequired,
		OAuthRefreshExpired,
		OAuthRefresh403,
		OAuthDiscoveryFailed,
		OAuthCallbackTimeout,
		OAuthCallbackMismatch,
		// Network/HTTP reachability.
		HTTPDNSFailed,
		HTTPTLSFailed,
		HTTPUnauth,
		HTTPForbidden,
		HTTPNotFound,
		HTTPServerErr,
		HTTPConnRefuse,
		HTTPTimeout,
		NetworkProxyMisconfig,
		NetworkOffline,
		// Quarantine is an admin state, not a connection failure.
		QuarantinePendingApproval,
		QuarantineToolChanged,
		UnknownUnclassified,
	}
	for _, c := range transient {
		if !Has(c) {
			t.Fatalf("%s is not registered in the catalog", c)
		}
		if IsPermanent(c) {
			t.Errorf("%s must stay retryable — it can heal without a human editing config", c)
		}
	}
}
