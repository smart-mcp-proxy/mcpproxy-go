package runtime

import (
	"fmt"
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 132 R5: a client that holds browser logins but no API key has no token
// record, so the binding guard (which reads auth.AgentToken only) would not see
// its binding. GuardState therefore carries the OAuth-side bindings next to the
// token records, and every guard treats a live named one as an active binding.

// OAuthBinding is one browser-login binding entry as the guard sees it. Live is
// true while the client has at least one grant that is not revoked and whose
// refresh token has not expired.
type OAuthBinding struct {
	ClientID string `json:"client_id"`
	Pin      string `json:"pin"`
	Mode     string `json:"mode"`
	Live     bool   `json:"live"`
}

// OAuthBindingSource is the browser-login storage surface the guard producers
// read; *storage.Manager implements it. A store that does not (a test fake, an
// older store) contributes no OAuth bindings.
type OAuthBindingSource interface {
	ListOAuthClientBindings() (map[string]storage.OAuthClientBinding, error)
	ListOAuthGrants() ([]storage.OAuthGrant, error)
}

// LoadOAuthBindings reads every browser-login binding with its liveness, sorted
// by client id. src is any value; one that does not implement
// OAuthBindingSource yields nil. A read error is returned and the caller MUST
// refuse the change (fail closed): it must never be read as "no bindings".
// On a database that never used browser login this is two read transactions
// over absent buckets and no write.
func LoadOAuthBindings(src any, now time.Time) ([]OAuthBinding, error) {
	s, ok := src.(OAuthBindingSource)
	if !ok || s == nil {
		return nil, nil
	}
	bindings, err := s.ListOAuthClientBindings()
	if err != nil {
		return nil, fmt.Errorf("cannot read browser-login bindings: %w", err)
	}
	if len(bindings) == 0 {
		return nil, nil
	}
	grants, err := s.ListOAuthGrants()
	if err != nil {
		return nil, fmt.Errorf("cannot read browser-login grants: %w", err)
	}
	live := map[string]bool{}
	for i := range grants {
		g := &grants[i]
		if !g.Revoked && g.RefreshExpiresAt.After(now) {
			live[g.Client] = true
		}
	}
	out := make([]OAuthBinding, 0, len(bindings))
	for id, b := range bindings {
		out = append(out, OAuthBinding{ClientID: id, Pin: b.ProfilePin, Mode: b.ProfileMode, Live: live[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out, nil
}

// ActiveOAuthBinding reports whether b counts as an active named binding: a
// live entry with a named pin and a binding mode.
func ActiveOAuthBinding(b OAuthBinding) bool {
	return b.Live && b.Pin != "" &&
		(b.Mode == auth.ProfileModeLocked || b.Mode == auth.ProfileModeSwitchable)
}

// EffectiveTokens returns the guarded records the evaluators walk: Tokens, plus
// one synthetic client record for every live named OAuth binding whose client
// has no ACTIVE guarded token (a client with both credentials counts once, as
// its API-key record). The binding of a client that has an API-key record, in
// any state, is that record's: it is the truth for the request path
// (storage.CurrentClientBinding), so a stale entry never decides. With no OAuth
// bindings the result is Tokens itself, unchanged.
func (s GuardState) EffectiveTokens(now time.Time) []auth.AgentToken {
	if len(s.OAuthBindings) == 0 {
		return s.Tokens
	}
	byName := make(map[string]*auth.AgentToken, len(s.Tokens))
	for i := range s.Tokens {
		byName[s.Tokens[i].Name] = &s.Tokens[i]
	}
	var extra []auth.AgentToken
	for _, b := range s.OAuthBindings {
		if !b.Live {
			continue
		}
		name := auth.ClientTokenName(b.ClientID)
		pin, mode := b.Pin, b.Mode
		if rec := byName[name]; rec != nil && rec.Kind == auth.KindClient {
			if ActiveGuardedBinding(rec, now) {
				continue // counted once, as the API-key record
			}
			pin, mode = rec.ProfilePin, rec.ProfileMode
		}
		if !ActiveOAuthBinding(OAuthBinding{ClientID: b.ClientID, Pin: pin, Mode: mode, Live: true}) {
			continue
		}
		extra = append(extra, auth.AgentToken{
			Name: name, Kind: auth.KindClient, ClientID: b.ClientID,
			ProfilePin: pin, ProfileMode: mode, ExpiresAt: now.AddDate(100, 0, 0),
		})
	}
	if len(extra) == 0 {
		return s.Tokens
	}
	out := make([]auth.AgentToken, 0, len(s.Tokens)+len(extra))
	out = append(out, s.Tokens...)
	return append(out, extra...)
}

// withOAuthClient returns bindings with client's entry added or replaced by e
// (a first approval's candidate entry, Live: true).
func withOAuthClient(bindings []OAuthBinding, e OAuthBinding) []OAuthBinding {
	out := make([]OAuthBinding, 0, len(bindings)+1)
	replaced := false
	for _, b := range bindings {
		if b.ClientID == e.ClientID {
			out = append(out, e)
			replaced = true
			continue
		}
		out = append(out, b)
	}
	if !replaced {
		out = append(out, e)
	}
	return out
}

// repinOAuthBindings returns bindings with every pin naming from moved to to
// (a profile rename or reassign rewrites both copies).
func repinOAuthBindings(bindings []OAuthBinding, from, to string) []OAuthBinding {
	if len(bindings) == 0 {
		return bindings
	}
	out := make([]OAuthBinding, len(bindings))
	copy(out, bindings)
	for i := range out {
		if out[i].Pin == from {
			out[i].Pin = to
		}
	}
	return out
}
