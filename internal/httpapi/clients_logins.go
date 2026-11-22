//go:build !server

package httpapi

import (
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// ClientCredential is one credential of a client in the Clients payloads
// (Spec 132, contracts/rest-admin.md): its API key or one browser login. A
// secret, a hash or a token never appears here, only the display prefix.
type ClientCredential struct {
	// Kind is api_key or browser_login.
	Kind   string `json:"kind"`
	Prefix string `json:"prefix,omitempty"`
	// GrantID names a browser login.
	GrantID string `json:"grant_id,omitempty"`
	// State is active|expired|revoked for an API key and active|lapsed|revoked
	// for a login.
	State        string     `json:"state"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	ReportedName string     `json:"reported_name,omitempty"`
	RedirectHost string     `json:"redirect_host,omitempty"`
	// ObservedName is the name the client sent at initialize (advisory).
	ObservedName string `json:"observed_name,omitempty"`
	NameMismatch *bool  `json:"name_mismatch,omitempty"`
}

func clientCredentials(in []internalRuntime.ClientCredentialEntry) []ClientCredential {
	if in == nil {
		return nil
	}
	out := make([]ClientCredential, 0, len(in))
	for i := range in {
		e := &in[i]
		out = append(out, ClientCredential{
			Kind: e.Kind, Prefix: e.Prefix, GrantID: e.GrantID, State: e.State, ExpiresAt: e.ExpiresAt,
			ApprovedAt: e.ApprovedAt, LastUsedAt: e.LastUsedAt, ReportedName: e.ReportedName,
			RedirectHost: e.RedirectHost, ObservedName: e.ObservedName, NameMismatch: e.NameMismatch,
		})
	}
	return out
}

// decorateBrowserLogins adds the Spec 132 credentials list and
// browser_login_disabled to the client rows, and one row for every client that
// has browser logins but neither a registry entry nor an API-key record. It is
// a no-op until browser login was used on this install (the service returns
// views without credentials then), so a never-used install keeps exactly its
// previous payload.
func (s *Server) decorateBrowserLogins(rows []clientPresence, cfg *config.Config) ([]clientPresence, error) {
	if s.clientsService == nil {
		return rows, nil
	}
	views, err := s.clientsService.List()
	if err != nil {
		return nil, err
	}
	used := false
	for i := range views {
		if views[i].Credentials != nil {
			used = true
			break
		}
	}
	if !used {
		return rows, nil
	}
	titleOf := func(name string) string {
		if cfg != nil {
			for i := range cfg.Profiles {
				if cfg.Profiles[i].Name == name {
					return cfg.Profiles[i].Title
				}
			}
		}
		return ""
	}
	profileMissing := func(name string) bool {
		if name == "" || cfg == nil {
			return false
		}
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == name {
				return false
			}
		}
		return true
	}
	source := func(mode string) string {
		if mode == auth.ProfileModeLocked {
			return string(profile.SourcePin)
		}
		return string(profile.SourceBinding)
	}

	seen := map[string]bool{}
	for i := range rows {
		seen[rows[i].ID] = true
	}
	ids := make([]string, 0, len(views))
	byID := map[string]int{}
	for i := range views {
		if views[i].Credentials == nil {
			continue
		}
		byID[views[i].ID] = i
		ids = append(ids, views[i].ID)
	}
	sort.Strings(ids)

	for i := range rows {
		vi, ok := byID[rows[i].ID]
		if !ok {
			continue
		}
		v := &views[vi]
		rows[i].Credentials = clientCredentials(v.Credentials)
		rows[i].BrowserLoginDisabled = v.BrowserLoginDisabled
		if rows[i].TokenName == "" {
			// No API-key record: the binding the row shows is the browser login's.
			rows[i].Profile, rows[i].ProfileMode = v.Profile, v.Mode
			rows[i].ProfileTitle = titleOf(v.Profile)
			rows[i].ProfileMissing = profileMissing(v.Profile)
			rows[i].ProfileSource = source(v.Mode)
		}
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		v := &views[byID[id]]
		name := v.DisplayName
		if name == "" {
			name = id
		}
		rows = append(rows, clientPresence{
			ID: id, DisplayName: name, Kind: "custom", State: contracts.ClientPresenceOther,
			CredentialState: profile.CredentialStateNone,
			Profile:         v.Profile, ProfileMode: v.Mode, ProfileTitle: titleOf(v.Profile),
			ProfileMissing: profileMissing(v.Profile), ProfileSource: source(v.Mode),
			Credentials: clientCredentials(v.Credentials), BrowserLoginDisabled: v.BrowserLoginDisabled,
		})
	}
	return rows, nil
}
