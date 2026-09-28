package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108 (Profiles v3) shared test fixture (tasks T001/T002). Every
// profile-policy test across 108-a..108-h builds its callers and its policy
// fixture from here so the contracts/enforcement-matrix.md fixture is
// encoded exactly once.

// clientCtx returns a context authenticated as a Spec 108-c client
// credential (kind=client) bound to pin under mode ("locked"|"switchable")
// — moved here from 108-a's placeholder per T027b, now that
// AuthContext.TokenKind/ClientID/ProfileMode exist (108-c T034).
func clientCtx(clientID, pin, mode string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      fmt.Sprintf("client-%s", clientID),
		TokenPrefix:    "mcp_cli_fix",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		ProfilePin:     pin,
		TokenKind:      auth.KindClient,
		ClientID:       clientID,
		ProfileMode:    mode,
	})
}

// enforcementMatrixProfiles returns the three contracts/enforcement-matrix.md
// fixture profiles, in fixture order (work-readonly, work-full, legacy).
func enforcementMatrixProfiles() []config.ProfileConfig {
	falseVal := false
	switchTo := []string{"work-full"}
	return []config.ProfileConfig{
		{
			Name:    "work-readonly",
			Title:   "Work · Read-only",
			Servers: []string{"github", "notion"},
			MaxTier: "read",
			Tools: &config.ProfileToolRules{
				Allow: []string{"notion:update_page", "github:get_secret_scanning_alert", "filesystem:read_text_file"},
				Deny:  []string{"github:*secret*"},
			},
			CodeExecution:   &falseVal,
			ManagementTools: &falseVal,
			SwitchableTo:    &switchTo,
		},
		{
			Name:          "work-full",
			Servers:       []string{"github", "notion", "filesystem"},
			MaxTier:       "destructive",
			Unannotated:   "as_write",
			CodeExecution: boolPtr(true),
		},
		{
			Name:    "legacy",
			Servers: []string{"github"},
		},
	}
}

// newProfilesV3Fixture builds a test proxy+runtime wired with the three
// contracts/enforcement-matrix.md fixture profiles (work-readonly, work-full,
// legacy) and three fake upstreams — github (5 tools spanning every
// annotation shape), notion (1 write tool) and filesystem (1 read tool) —
// exactly as the matrix's fixture table lists them. It enables the FR-009a
// policy gate for the duration of the test (config.EnablePolicyForTest) so a
// v3 profile loads before 108-d ships real enforcement.
func newProfilesV3Fixture(t *testing.T) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()
	config.EnablePolicyForTest(t)

	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{
			{Name: "github", Enabled: true},
			{Name: "notion", Enabled: true},
			{Name: "filesystem", Enabled: true},
		}
		cfg.Profiles = enforcementMatrixProfiles()
	})

	startCountingUpstream(t, proxy, rt, "github",
		readSpec("list_issues"),
		writeSpec("create_issue"),
		destructiveSpec("delete_repo"),
		toolSpec{Name: "search_code", Description: "Search code"},
		readSpec("get_secret_scanning_alert"),
	)
	startCountingUpstream(t, proxy, rt, "notion", writeSpec("update_page"))
	startCountingUpstream(t, proxy, rt, "filesystem", readSpec("read_text_file"))

	return proxy, rt
}

// anonCtx returns a context with no AuthContext at all — the "no credential"
// anonymous branch (Spec 108 FR-008/US5), as distinct from adminCtx's
// explicit administrator identity. Callers that need the
// require_mcp_auth:false back-compat unrecognised-token branch build their
// own context; this is the pure no-credential case every profile resolver
// test starts from.
func anonCtx() context.Context {
	return context.Background()
}
