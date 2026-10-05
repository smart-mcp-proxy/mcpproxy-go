package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

const (
	// clientRotationCustomOverlap is how long a staged rotation of a custom
	// client (one with no readable config) keeps both secrets before it is
	// finalized automatically (FR-021a: 24-hour overlap).
	clientRotationCustomOverlap = 24 * time.Hour
	// clientCredentialExpiringWindow is when client_credential_expiring
	// starts (plan D26): two weeks, matching a reconnect window.
	clientCredentialExpiringWindow = 14 * 24 * time.Hour
)

// reconcilerActor attributes records the reconciler writes.
var reconcilerActor = Actor{Kind: "system", Surface: profile.SurfaceAPI}

// Reconcile resolves every staged rotation it can (FR-021a), at startup. Only
// records mid-rotation are examined, so it reads at most the configs of
// clients whose rotation was interrupted.
func (s *ClientsService) Reconcile(ctx context.Context) error {
	all, err := s.records()
	if err != nil {
		return err
	}
	var ids []string
	for i := range all {
		if all[i].Kind == auth.KindClient && all[i].PendingHash != "" {
			ids = append(ids, all[i].ClientID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := s.ReconcileClient(ctx, id); err != nil {
			s.logger.Warn("client rotation reconcile failed", zap.String("client_id", id), zap.Error(err))
		}
	}
	return nil
}

// ReconcileClient resolves one client's staged rotation, if any:
//
//   - supported client whose config holds the new secret -> finalize;
//   - holds the old secret -> roll back (drop the pending one);
//   - unreadable, no entry, or neither -> keep both (client_rotation_pending);
//   - custom client (no readable config) -> finalize after the 24 h overlap.
//
// A crash or write failure at any point therefore never leaves a client
// holding an invalidated secret.
func (s *ClientsService) ReconcileClient(ctx context.Context, clientID string) error {
	if !auth.ValidClientID(clientID) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.records()
	if err != nil {
		return err
	}
	rec := clientRecord(all, clientID)
	if rec == nil || rec.Kind != auth.KindClient || rec.PendingHash == "" || rec.Revoked {
		return nil
	}
	if s.connectInFlight(clientID) {
		return nil // a connect is mid-write: the file legitimately still holds the old secret
	}
	if def := connect.FindClient(clientID); def == nil || !def.Supported {
		if rec.RotationStartedAt != nil && s.now().Sub(*rec.RotationStartedAt) >= clientRotationCustomOverlap {
			_, err := s.finalizeLocked(ctx, reconcilerActor, clientID)
			return err
		}
		return nil
	}
	if s.reader == nil {
		return nil
	}
	secret, found, err := s.reader.ClientSecret(clientID)
	if err != nil || !found || secret == "" {
		return nil // unreadable / no entry: keep both
	}
	key, err := s.hmacKey()
	if err != nil {
		return err
	}
	h := auth.HashToken(secret, key)
	switch {
	case auth.ConstantTimeEqual(h, rec.PendingHash):
		_, err := s.finalizeLocked(ctx, reconcilerActor, clientID)
		return err
	case auth.ConstantTimeEqual(h, rec.TokenHash):
		return s.rollbackLocked(ctx, reconcilerActor, clientID)
	default:
		return nil
	}
}

// WarningAction is what a warning's "fix it" control does (data-model §7):
// kind is a profile.FixAction spelling (change_setting, reconnect_client,
// move_client, edit_token) or profile.WarningActionUpgradeAdminKeyHolders;
// target names what to act on (a setting, a client id or a token name).
type WarningAction struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
}

// Warning is one Clients-surface warning (data-model §7 ClientView.warnings):
// the ONE shape every surface renders. Severity is info for a rotation that is
// merely in progress and warn for everything else.
type Warning struct {
	Code     profile.WarningCode     `json:"code"`
	Severity profile.WarningSeverity `json:"severity"`
	ClientID string                  `json:"client_id,omitempty"`
	Message  string                  `json:"message"`
	Action   *WarningAction          `json:"action,omitempty"`
	Bindings []BindingRef            `json:"bindings,omitempty"`
	Fixes    []GuardFix              `json:"fixes,omitempty"`
}

// Warnings derives the credential and guard warnings. states maps client id
// to its classified credential_state where the caller has it (an on-demand
// classification or the persisted last observation); rows not in the map
// contribute no admin-key warning.
func (s *ClientsService) Warnings(states map[string]profile.CredentialState) []Warning {
	all, err := s.records()
	if err != nil {
		return nil
	}
	var out []Warning
	cfg := s.cfg()
	g := s.guard()
	if active := g.BindingGuardActiveBindings(); len(active) > 0 {
		names := make([]string, 0, len(active))
		for _, b := range active {
			names = append(names, b.ClientID)
		}
		out = append(out, Warning{
			Code:     profile.WarningAnonymousDeniedByBindingGuard,
			Severity: profile.WarningSeverityWarn,
			Message:  fmt.Sprintf("anonymous callers are denied while %s could be bypassed without auth", strings.Join(names, ", ")),
			Action:   &WarningAction{Kind: string(profile.FixChangeSetting), Target: "require_mcp_auth"},
			Bindings: active,
			Fixes:    g.BindingGuardFixes(GuardState{Config: cfg, Tokens: clientOnly(all)}, active),
		})
	}
	now := s.now()
	for i := range all {
		t := &all[i]
		switch {
		case t.Kind == auth.KindClient && s.stateOf(t) == profile.CredentialStateClient:
			if t.ExpiresAt.Sub(now) <= clientCredentialExpiringWindow {
				out = append(out, Warning{Code: profile.WarningClientCredentialExpiring, Severity: profile.WarningSeverityWarn, ClientID: t.ClientID,
					Message: fmt.Sprintf("the credential of %s expires soon; reconnect it", t.ClientID),
					Action:  &WarningAction{Kind: string(profile.FixReconnectClient), Target: t.ClientID}})
			}
			if t.PendingHash != "" {
				out = append(out, Warning{Code: profile.WarningClientRotationPending, Severity: profile.WarningSeverityInfo, ClientID: t.ClientID,
					Message: fmt.Sprintf("a credential rotation of %s has not finished; reconnect it", t.ClientID),
					Action:  &WarningAction{Kind: string(profile.FixReconnectClient), Target: t.ClientID}})
			}
			if t.ProfilePin != "" && !s.profileExists(cfg, t.ProfilePin) {
				out = append(out, Warning{Code: profile.WarningProfileMissing, Severity: profile.WarningSeverityWarn, ClientID: t.ClientID,
					Message: fmt.Sprintf("the profile %s bound to %s no longer exists; the client is denied everything", t.ProfilePin, t.ClientID),
					Action:  &WarningAction{Kind: string(profile.FixMoveClient), Target: t.ClientID}})
			}
		case t.Kind != auth.KindClient && strings.HasPrefix(t.Name, "client-") && t.UserID == "":
			id := strings.TrimPrefix(t.Name, "client-")
			out = append(out, Warning{Code: profile.WarningClientTokenNameConflict, Severity: profile.WarningSeverityWarn, ClientID: id,
				Message: fmt.Sprintf("token name %s is held by a regular agent token; revoke or delete token %s, then connect again", t.Name, t.Name),
				Action:  &WarningAction{Kind: string(profile.FixEditToken), Target: t.Name}})
		}
	}
	ids := make([]string, 0, len(states))
	for id, st := range states {
		if st == profile.CredentialStateAdminKey {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, Warning{Code: profile.WarningClientHoldsAdminKey, Severity: profile.WarningSeverityWarn, ClientID: id,
			Message: fmt.Sprintf("%s holds the admin API key; upgrade it to a client credential", id),
			Action:  &WarningAction{Kind: profile.WarningActionUpgradeAdminKeyHolders}})
	}
	return out
}

// ReconcileTimeOnly is the half of the FR-021a reconciler that needs no config
// read: a custom client's staged rotation finalizes once the 24 h overlap has
// passed. The clients LIST runs only this half (Spec 075 keeps a list free of
// content reads); the detail read runs the full ReconcileClient.
func (s *ClientsService) ReconcileTimeOnly(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.records()
	if err != nil {
		return err
	}
	for i := range all {
		rec := &all[i]
		if rec.Kind != auth.KindClient || rec.PendingHash == "" || rec.Revoked || rec.RotationStartedAt == nil {
			continue
		}
		if s.connectInFlight(rec.ClientID) {
			continue
		}
		if def := connect.FindClient(rec.ClientID); def != nil && def.Supported {
			continue // a supported client's rotation resolves from its config
		}
		if s.now().Sub(*rec.RotationStartedAt) >= clientRotationCustomOverlap {
			if _, err := s.finalizeLocked(ctx, reconcilerActor, rec.ClientID); err != nil {
				s.logger.Warn("custom client rotation finalize failed", zap.String("client_id", rec.ClientID), zap.Error(err))
			}
		}
	}
	return nil
}
