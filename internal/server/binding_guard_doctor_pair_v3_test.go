package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// TestBindingGuardActiveBindings_DoctorPair pins Spec 108 T083 / US5-4 at the
// one evaluator `mcpproxy doctor` (profiles.binding_bypass) and the GET /clients
// anonymous_denied_by_binding_guard warning both read: with require_mcp_auth off
// and Cursor locked to work-readonly,
//
//	(a) anonymous_profile = a copy of work-readonly with switchable_to unset
//	    reaches nothing work-readonly does not: no active binding, no warning;
//	(b) anonymous_profile = work-readonly itself is wider than the locked
//	    binding, because its switchable_to [work-full] lets an anonymous caller
//	    switch where the locked client cannot (FR-008a reachability): Cursor is
//	    an active binding and the warning names it.
//
// doctor derives its finding from that warning, so it cannot disagree with the
// API refusal.
func TestBindingGuardActiveBindings_DoctorPair(t *testing.T) {
	cases := []struct {
		name      string
		anonymous string
		active    bool
	}{
		{name: "a: a copy of work-readonly without switchable_to", anonymous: "work-readonly-copy", active: false},
		{name: "b: work-readonly itself (switchable_to [work-full])", anonymous: "work-readonly", active: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy, rt := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
				cfg.RequireMCPAuth = false
				cfg.AnonymousProfile = tc.anonymous
				var copied config.ProfileConfig
				for _, p := range cfg.Profiles {
					if p.Name == "work-readonly" {
						copied = p
					}
				}
				copied.Name = "work-readonly-copy"
				copied.SwitchableTo = nil
				cfg.Profiles = append(cfg.Profiles, copied)
			})
			rt.SetBindingGuard(proxy)
			_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_doctor_pair_test", []byte("doctor-pair-test-key"),
				auth.ProfileModeLocked, "work-readonly", time.Now().Add(time.Hour))
			require.NoError(t, err)

			active := proxy.BindingGuardActiveBindings()
			warnings := rt.ClientsService().Warnings(nil)
			var guardWarning bool
			for _, w := range warnings {
				if w.Code == profile.WarningAnonymousDeniedByBindingGuard {
					guardWarning = true
					require.NotEmpty(t, w.Bindings, "the warning names the binding doctor prints")
					require.Equal(t, "cursor", w.Bindings[0].ClientID)
				}
			}
			if tc.active {
				require.Len(t, active, 1)
				require.Equal(t, "cursor", active[0].ClientID)
				require.Equal(t, "work-readonly", active[0].Profile)
			} else {
				require.Empty(t, active)
			}
			require.Equal(t, tc.active, guardWarning, "the GET /clients warning and the evaluator agree")
		})
	}
}
