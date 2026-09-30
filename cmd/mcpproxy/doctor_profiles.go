package main

// Doctor checks for profiles and clients (Spec 108-g, FR-025, US5-4).
//
// Both checks are derived ONLY from the warnings of GET /api/v1/clients. The
// server computes them with the one BindingBypassable evaluator and the
// persisted credential observation, so doctor, the REST warning and the API
// refusal cannot disagree, and doctor never reads a client config (no macOS
// App-Data prompt).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// profileCheck is one entry of the additive `profile_checks` JSON key.
type profileCheck struct {
	ID       string               `json:"id"`
	Status   string               `json:"status"` // ok | warn | info | skipped
	Message  string               `json:"message"`
	Bindings []runtime.BindingRef `json:"bindings,omitempty"`
	Fixes    []runtime.GuardFix   `json:"fixes,omitempty"`
}

const (
	checkBindingBypass = "profiles.binding_bypass"
	checkAdminKey      = "connect.admin_key_in_client_config"
	checkAdminKeyInfo  = "connect.admin_key_unchecked"
)

// Fix kinds of the admin-key remediation (additive to the guard fix kinds).
const (
	fixUpgradeAdminKeyHolders = "upgrade_admin_key_holders"
	fixRotateAdminAPIKey      = "rotate_admin_api_key"
)

// collectProfileChecks reads GET /clients once and derives the checks. An older
// daemon or the server edition (no /clients) yields skipped entries with the
// reason.
func collectProfileChecks(client *cliclient.Client) []profileCheck {
	sess := &restSession{client: client}
	data, ref, err := sess.call(http.MethodGet, "/api/v1/clients", nil)
	if err != nil || ref != nil {
		reason := "GET /api/v1/clients failed"
		switch {
		case ref != nil && ref.Status == http.StatusNotFound:
			reason = "not available on this edition or daemon (GET /api/v1/clients answered 404)"
		case ref != nil:
			reason = ref.Error
		case err != nil:
			reason = err.Error()
		}
		return []profileCheck{
			{ID: checkBindingBypass, Status: "skipped", Message: reason},
			{ID: checkAdminKey, Status: "skipped", Message: reason},
		}
	}
	var list struct {
		Clients []struct {
			ID              string `json:"id"`
			Kind            string `json:"kind"`
			Connected       bool   `json:"connected"`
			CredentialState string `json:"credential_state"`
		} `json:"clients"`
		Warnings []runtime.Warning `json:"warnings"`
	}
	if json.Unmarshal(data, &list) != nil {
		return []profileCheck{
			{ID: checkBindingBypass, Status: "skipped", Message: "unexpected GET /api/v1/clients response"},
			{ID: checkAdminKey, Status: "skipped", Message: "unexpected GET /api/v1/clients response"},
		}
	}

	bypass := profileCheck{ID: checkBindingBypass, Status: "ok", Message: "no client binding can be bypassed without auth"}
	var holders []string
	for _, w := range list.Warnings {
		switch w.Code {
		case profile.WarningAnonymousDeniedByBindingGuard:
			bypass = profileCheck{ID: checkBindingBypass, Status: "warn", Message: w.Message, Bindings: w.Bindings, Fixes: w.Fixes}
		case profile.WarningClientHoldsAdminKey:
			holders = append(holders, w.ClientID)
		}
	}
	adminKey := profileCheck{ID: checkAdminKey, Status: "ok", Message: "no client is known to hold the admin API key"}
	if len(holders) > 0 {
		adminKey = profileCheck{
			ID: checkAdminKey, Status: "warn",
			Message: fmt.Sprintf("%s hold the admin API key in their config: %s", countNoun(len(holders), "client"), strings.Join(holders, ", ")),
			Fixes:   []runtime.GuardFix{{Kind: fixUpgradeAdminKeyHolders}, {Kind: fixRotateAdminAPIKey}},
		}
	}
	checks := []profileCheck{bypass, adminKey}

	unchecked := 0
	for _, c := range list.Clients {
		if c.Connected && c.Kind == "supported" && c.CredentialState == string(profile.CredentialStateUnknown) {
			unchecked++
		}
	}
	if unchecked > 0 {
		checks = append(checks, profileCheck{
			ID: checkAdminKeyInfo, Status: "info",
			Message: fmt.Sprintf("%d connected %s never checked for the admin key; run mcpproxy client upgrade-admin-key-holders (preview only) to check",
				unchecked, plural2(unchecked, "client was", "clients were")),
		})
	}
	return checks
}

func countNoun(n int, noun string) string {
	return fmt.Sprintf("%d %s%s", n, noun, plural(n))
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func profileChecksWarn(checks []profileCheck) bool {
	for _, c := range checks {
		if c.Status == "warn" {
			return true
		}
	}
	return false
}

// printProfileChecksSection prints the `Profiles & clients` section after the
// attention list. Nothing is printed when the checks were not collected.
func printProfileChecksSection(checks []profileCheck) {
	if len(checks) == 0 {
		return
	}
	fmt.Println("👥 Profiles & clients")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	for _, c := range checks {
		mark := map[string]string{"ok": "✓", "warn": "⚠", "info": "ℹ", "skipped": "–"}[c.Status]
		fmt.Printf("%s %s: %s\n", mark, c.ID, c.Message)
		if len(c.Bindings) > 0 {
			fmt.Println("  Bindings:")
			for _, b := range c.Bindings {
				fmt.Printf("    - %s → %s (%s)\n", b.ClientID, profileOrAll(b.Profile), b.Mode)
			}
		}
		name := ""
		if len(c.Bindings) > 0 {
			name = c.Bindings[0].Profile
		}
		for i, f := range c.Fixes {
			fmt.Printf("  Fix: %s\n", doctorFixLine(f, name, i+1))
		}
	}
	fmt.Println()
}

// doctorFixLine renders one fix; the admin-key remediation is two ordered steps.
func doctorFixLine(f runtime.GuardFix, bindingProfile string, step int) string {
	switch f.Kind {
	case fixUpgradeAdminKeyHolders:
		return fmt.Sprintf("%d. mcpproxy client upgrade-admin-key-holders", step)
	case fixRotateAdminAPIKey:
		return fmt.Sprintf("%d. rotate the admin API key (set a new api_key and restart): %s", step, adminKeyDocsURL)
	}
	return guardFixLine(f, bindingProfile)
}
