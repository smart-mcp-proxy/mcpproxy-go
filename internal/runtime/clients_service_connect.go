package runtime

import (
	"context"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// clientCredentialTTL is the lifetime of a freshly minted client credential
// (FR-021: default and maximum 365 days). A rotation keeps the record's
// expiry (plan D14).
const clientCredentialTTL = auth.MaxTokenExpiry

// pendingBinding is the state a rotating connect carries from Issue to
// Commit: the binding it asked for, applied only after the config write
// succeeded.
type pendingBinding struct {
	pin  string
	mode string
}

// ConnectMinter adapts the service to connect.CredentialMinter.
func (s *ClientsService) ConnectMinter() connect.CredentialMinter { return connectMinter{s: s} }

type connectMinter struct{ s *ClientsService }

func actorOf(intent connect.CredentialIntent) Actor {
	surface := profile.Surface(intent.Surface)
	if surface == "" {
		surface = profile.SurfaceAPI
	}
	return Actor{Kind: intent.ActorKind, Name: intent.ActorName, Surface: surface}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// issueLocked is Issue's body (s.mu held): resolve the binding, run the guard
// over the candidate state, then mint a fresh credential or stage a rotation.
// allowRotate=false (client add) refuses an already-active credential.
func (s *ClientsService) issueLocked(clientID string, profilePtr, modePtr *string, allowRotate bool) (*connect.IssuedCredential, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	rec := clientRecord(all, clientID)
	if rec != nil && rec.Kind != auth.KindClient {
		return nil, storage.ErrClientCredentialConflict
	}
	active := s.stateOf(rec) == profile.CredentialStateClient
	if active && !allowRotate {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("client %s already has an active credential; rotate it instead", clientID)}
	}

	// A reconnect that names no profile keeps the recorded binding, so it can
	// never silently widen a locked client. That holds for an EXPIRED
	// credential too (it lapsed; the operator's binding did not). A revoked
	// credential was cut off deliberately, so reconnecting it is a fresh
	// grant and starts from the defaults (All servers) unless a profile is
	// given.
	keepBinding := rec != nil && profilePtr == nil &&
		(active || s.stateOf(rec) == profile.CredentialStateExpired)
	var pin, mode string
	if keepBinding {
		pin = rec.ProfilePin
		mode, err = resolveMode(pin, modePtr, rec.ProfileMode)
	} else {
		pin = deref(profilePtr)
		mode, err = resolveMode(pin, modePtr, "")
	}
	if err != nil {
		return nil, err
	}
	if pin != "" && !s.profileExists(s.cfg(), pin) {
		return nil, &ValidationError{Field: "profile", Message: fmt.Sprintf("unknown profile %q", pin)}
	}

	now := s.now().UTC()
	next := auth.AgentToken{
		Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID,
		ProfilePin: pin, ProfileMode: mode, CreatedAt: now, ExpiresAt: now.Add(clientCredentialTTL),
	}
	if active {
		next = *rec
		next.ProfilePin, next.ProfileMode = pin, mode
	}
	if err := s.checkGuard(all, clientID, &next); err != nil {
		return nil, err
	}

	key, err := s.hmacKey()
	if err != nil {
		return nil, err
	}
	raw, err := auth.GenerateClientToken()
	if err != nil {
		return nil, err
	}
	if active {
		if _, err := s.store.StageClientCredentialRotation(clientID, raw, key); err != nil {
			return nil, err
		}
		return &connect.IssuedCredential{
			Secret: raw, TokenName: rec.Name, Profile: pin, Mode: mode, Rotating: true,
			Pending: pendingBinding{pin: pin, mode: mode},
		}, nil
	}
	tok, err := s.store.MintClientCredential(clientID, raw, key, mode, pin, now.Add(clientCredentialTTL))
	if err != nil {
		return nil, err
	}
	return &connect.IssuedCredential{Secret: raw, TokenName: tok.Name, Profile: pin, Mode: mode}, nil
}

// Issue implements connect.CredentialMinter.
func (m connectMinter) Issue(clientID string, intent connect.CredentialIntent) (*connect.IssuedCredential, error) {
	if !auth.ValidClientID(clientID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	return m.s.issueLocked(clientID, intent.Profile, intent.Mode, true)
}

// Commit implements connect.CredentialMinter.
func (m connectMinter) Commit(clientID string, intent connect.CredentialIntent, issued *connect.IssuedCredential) error {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()
	a := actorOf(intent)
	if !issued.Rotating {
		s.recordMint(ctx, a, clientID, issued)
		return nil
	}
	if _, err := s.finalizeLocked(ctx, a, clientID); err != nil {
		return err
	}
	if pb, ok := issued.Pending.(pendingBinding); ok {
		mode := pb.mode
		if _, err := s.setBindingLocked(ctx, a, clientID, pb.pin, &mode); err != nil {
			return err
		}
	}
	return nil
}

// Abort implements connect.CredentialMinter.
func (m connectMinter) Abort(clientID string, intent connect.CredentialIntent, issued *connect.IssuedCredential) error {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if issued.Rotating {
		return s.rollbackLocked(context.Background(), actorOf(intent), clientID)
	}
	// A fresh mint whose config write failed: revoke it and write no record
	// (from the operator's view nothing happened, plan D13).
	_, err := s.store.ForgetClientCredential(clientID)
	return err
}

// hashMatches reports whether secret is the primary or pending secret of rec.
func (s *ClientsService) hashMatches(secret string, rec *auth.AgentToken) bool {
	key, err := s.hmacKey()
	if err != nil || rec == nil {
		return false
	}
	h := auth.HashToken(secret, key)
	if auth.ConstantTimeEqual(h, rec.TokenHash) {
		return true
	}
	return rec.PendingHash != "" && auth.ConstantTimeEqual(h, rec.PendingHash)
}

// Classify implements connect.CredentialMinter (plan D7).
func (m connectMinter) Classify(clientID, secret string) profile.CredentialState {
	s := m.s
	all, err := s.records()
	if err != nil {
		return profile.CredentialStateNone
	}
	rec := clientRecord(all, clientID)
	if rec != nil && rec.Kind == auth.KindClient && s.hashMatches(secret, rec) {
		return s.stateOf(rec)
	}
	for i := range all {
		if all[i].Kind == auth.KindClient && all[i].ClientID != clientID && s.hashMatches(secret, &all[i]) {
			return profile.CredentialStateNone // another client's credential is not this client's
		}
	}
	// A mcp_cli_ secret with no record: rotated away or deleted.
	return profile.CredentialStateRevoked
}

// HeldByRecord implements connect.CredentialMinter.
func (m connectMinter) HeldByRecord(clientID, secret string) bool {
	all, err := m.s.records()
	if err != nil {
		return false
	}
	rec := clientRecord(all, clientID)
	return rec != nil && rec.Kind == auth.KindClient && m.s.hashMatches(secret, rec)
}

// ForgetUnheld implements connect.CredentialMinter (plan D16).
func (m connectMinter) ForgetUnheld(clientID, restoredSecret string, intent connect.CredentialIntent) (string, error) {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.records()
	if err != nil {
		return "", err
	}
	rec := clientRecord(all, clientID)
	if s.stateOf(rec) != profile.CredentialStateClient {
		return "", nil
	}
	if restoredSecret != "" && s.hashMatches(restoredSecret, rec) {
		return "", nil
	}
	view, err := s.forgetLocked(context.Background(), actorOf(intent), clientID, map[string]interface{}{"reason": "undo"})
	if err != nil {
		return "", err
	}
	return view.TokenName, nil
}
