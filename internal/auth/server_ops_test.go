package auth

import "testing"

func TestAgentDeniedServerOp(t *testing.T) {
	denied := []string{
		ServerOpAdd, ServerOpRemove, ServerOpUpdate, ServerOpPatch,
		ServerOpEnable, ServerOpDisable, ServerOpRestart, ServerOpRefresh,
		ServerOpDiscoverTools, ServerOpQuarantine, ServerOpUnquarantine,
		ServerOpAddFromRegistry, ServerOpLogin, ServerOpLogout,
		ServerOpConfigToSecret, ServerOpApproveTools, ServerOpBlockTools,
		ServerOpConfigWrite, ServerOpScan, ServerOpSecurityApprove,
		ServerOpSecurityReject, ServerOpSecretWrite, ServerOpDiagnosticsFix,
	}
	for _, op := range denied {
		if !AgentDeniedServerOp(op) {
			t.Errorf("expected op %q to be denied to agents", op)
		}
	}

	// Read/observability ops that agents keep.
	allowed := []string{"list", "tail_log", "inspect", "", "LIST", "Enable"}
	for _, op := range allowed {
		if AgentDeniedServerOp(op) {
			t.Errorf("expected op %q to be allowed for agents", op)
		}
	}
}

func TestAuthorizeServerOp(t *testing.T) {
	agent := &AuthContext{Type: AuthTypeAgent, AgentName: "a"}
	admin := AdminContext()
	adminUser := &AuthContext{Type: AuthTypeAdminUser}
	user := &AuthContext{Type: AuthTypeUser}

	tests := []struct {
		name string
		ac   *AuthContext
		op   string
		want bool
	}{
		{"nil passthrough (test/no-config)", nil, ServerOpEnable, true},
		{"admin allowed on denied op", admin, ServerOpRemove, true},
		{"admin_user allowed on denied op", adminUser, ServerOpQuarantine, true},
		{"agent denied on denied op", agent, ServerOpEnable, false},
		{"agent denied on quarantine", agent, ServerOpQuarantine, false},
		{"agent allowed on read op", agent, "list", true},
		{"non-admin user denied on denied op", user, ServerOpRestart, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AuthorizeServerOp(tc.ac, tc.op); got != tc.want {
				t.Errorf("AuthorizeServerOp(%+v, %q) = %v, want %v", tc.ac, tc.op, got, tc.want)
			}
		})
	}
}
