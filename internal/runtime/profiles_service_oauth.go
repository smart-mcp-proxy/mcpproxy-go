package runtime

import (
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// addOAuthUsers adds the clients that are bound to a profile only through
// browser logins (Spec 132): a client with a live login and a named pin is a
// user of that profile exactly like one with an API key, so a profile delete
// reports it in profile_in_use and a reassign moves it. A client that has an
// active API-key record is already listed through the record and is not
// counted twice. target returns the used-by list for a pin, or nil when the
// pin is not one the caller collects. A read error fails the call (fail
// closed): an unreadable store must not look like "nobody uses it".
func (s *ProfilesService) addOAuthUsers(tokens []auth.AgentToken, target func(pin string) *UsedBy) error {
	sm := s.rt.StorageManager()
	if sm == nil {
		return nil
	}
	now := time.Now()
	bindings, err := LoadOAuthBindings(sm, now)
	if err != nil {
		return fmt.Errorf("cannot read browser-login bindings: %w", err)
	}
	if len(bindings) == 0 {
		return nil
	}
	active := map[string]bool{}
	recs := map[string]*auth.AgentToken{}
	for i := range tokens {
		t := &tokens[i]
		if t.Kind != auth.KindClient || t.UserID != "" {
			continue
		}
		recs[t.ClientID] = t
		if !t.Revoked && t.ExpiresAt.After(now) {
			active[t.ClientID] = true
		}
	}
	for _, b := range bindings {
		if !b.Live || active[b.ClientID] {
			continue
		}
		// The API-key record, in any state, decides the binding (as
		// EffectiveTokens and CurrentClientBinding do); the entry only when there
		// is no record.
		pin, mode := b.Pin, b.Mode
		if rec := recs[b.ClientID]; rec != nil {
			pin, mode = rec.ProfilePin, rec.ProfileMode
		}
		if pin == "" {
			continue
		}
		if u := target(pin); u != nil {
			u.Clients = append(u.Clients, UsedByClient{ID: b.ClientID, Mode: mode})
		}
	}
	return nil
}
