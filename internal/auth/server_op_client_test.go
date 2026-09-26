package auth

import "testing"

// TestAuthorizeServerOp_ClientCredentialIsNonAdmin pins T037a (FR-016/D14,
// no policy change): a kind=client AuthContext is non-admin and
// AuthorizeServerOp refuses every member of agentDeniedServerOps exactly as
// for a regular agent token; only the read/observability operations
// (list/tail_log, which never appear in agentDeniedServerOps) pass. This
// falls out of AgentToken.AuthContext() always setting Type=AuthTypeAgent
// regardless of Kind — asserted here so a future change to that constructor
// cannot silently grant a client credential admin-shaped server ops.
func TestAuthorizeServerOp_ClientCredentialIsNonAdmin(t *testing.T) {
	tok := &AgentToken{
		Name:           "client-cursor",
		Kind:           KindClient,
		ClientID:       "cursor",
		ProfileMode:    ProfileModeSwitchable,
		AllowedServers: []string{"*"},
		Permissions:    []string{PermRead, PermWrite, PermDestructive},
	}
	ac := tok.AuthContext()
	if ac.IsAdmin() {
		t.Fatal("a client credential's AuthContext must not be admin")
	}

	denied := []string{
		ServerOpAdd, ServerOpAddFromRegistry, ServerOpUpdate, ServerOpPatch,
		ServerOpEnable, ServerOpDisable, ServerOpRestart, ServerOpRefresh,
		ServerOpRemove, ServerOpQuarantine, ServerOpUnquarantine,
		ServerOpDiscoverTools, ServerOpConfigWrite,
	}
	for _, op := range denied {
		if AuthorizeServerOp(ac, op) {
			t.Errorf("client credential must be refused server op %q, exactly like a regular agent token", op)
		}
	}

	// Read/observability operations are never in the denylist and must pass.
	if !AuthorizeServerOp(ac, "list") {
		t.Error("client credential must be allowed the read-only 'list' operation")
	}
	if !AuthorizeServerOp(ac, "tail_log") {
		t.Error("client credential must be allowed the read-only 'tail_log' operation")
	}
}
