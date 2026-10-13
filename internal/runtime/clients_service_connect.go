package runtime

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// clientCredentialTTL is the lifetime of a freshly minted client credential
// (FR-021: default and maximum 365 days). A rotation keeps the record's
// expiry (plan D14).
const clientCredentialTTL = auth.MaxTokenExpiry

// issueOptions are the parts of an issue a connect never sets: a custom
// client's expiry and display name (Spec 108-f F21), and the Spec 115
// issuance fields (issuer, purpose).
type issueOptions struct {
	expiresAt   time.Time
	displayName string
	issuer      *auth.CredentialIssuer
	purpose     string
}

// pendingBinding is the state a rotating connect carries from Issue to
// Commit: the binding it asked for, applied only after the config write
// succeeded.
type pendingBinding struct {
	pin  string
	mode string
}

// connectClaim is one in-flight connect: when it started and the token that
// proves ownership to Release, Commit and Abort.
type connectClaim struct {
	started time.Time
	token   string
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

// connectInFlightTTL is a safety valve for the in-flight connect claim: a
// config write takes milliseconds, so a claim older than this was abandoned
// (a panic between Issue and settle) and is ignored.
const connectInFlightTTL = 2 * time.Minute

// connectInFlight reports whether a connect of clientID holds a live claim
// (s.mu held).
func (s *ClientsService) connectInFlight(clientID string) bool {
	claim, ok := s.inflight[clientID]
	return ok && s.now().Sub(claim.started) < connectInFlightTTL
}

// claimConnect takes the in-flight claim for clientID and returns the
// per-connect token that identifies it. Release, Commit and Abort act only
// while the claim still carries their token, so a connect whose claim expired
// (connectInFlightTTL) and was taken over by another connect can never release
// or roll back the newer connect's state.
func (s *ClientsService) claimConnect(clientID string) string {
	if s.inflight == nil {
		s.inflight = map[string]connectClaim{}
	}
	s.claimSeq++
	token := fmt.Sprintf("%s-%d", clientID, s.claimSeq)
	s.inflight[clientID] = connectClaim{started: s.now(), token: token}
	return token
}

// ownsClaim reports whether token is the token of clientID's current claim
// (s.mu held).
func (s *ClientsService) ownsClaim(clientID, token string) bool {
	claim, ok := s.inflight[clientID]
	return ok && token != "" && claim.token == token
}

// releaseConnect drops the claim only when token still owns it (s.mu held).
func (s *ClientsService) releaseConnect(clientID, token string) {
	if s.ownsClaim(clientID, token) {
		delete(s.inflight, clientID)
	}
}

// resolveBindingLocked is THE rule for the binding a connect applies, shared by
// the write (issueLocked) and the reconnect preview (PreviewBinding) so the two
// cannot diverge (s.mu held).
//
// A reconnect that names no profile keeps the recorded binding, so it can
// never silently widen a locked client. That holds for an EXPIRED credential
// too (it lapsed; the operator's binding did not). A revoked credential was
// cut off deliberately (disconnect), but its tombstone preserves the prior
// profile pin and mode, so a profileless reconnect re-mints with that binding
// instead of silently widening a profile-locked client to All servers. A
// tombstone with no binding (or no record) starts from the defaults, and an
// explicit profile always wins.
func (s *ClientsService) resolveBindingLocked(rec *auth.AgentToken, profilePtr, modePtr *string) (pin, mode string, err error) {
	keepBinding := rec != nil && profilePtr == nil &&
		(s.stateOf(rec) == profile.CredentialStateClient || s.stateOf(rec) == profile.CredentialStateExpired ||
			s.stateOf(rec) == profile.CredentialStateRevoked)
	if keepBinding {
		pin = rec.ProfilePin
		mode, err = resolveMode(pin, modePtr, rec.ProfileMode)
	} else {
		pin = deref(profilePtr)
		mode, err = resolveMode(pin, modePtr, "")
	}
	if err != nil {
		return "", "", err
	}
	if pin != "" && !s.profileExists(s.cfg(), pin) {
		return "", "", &ValidationError{Field: "profile", Message: fmt.Sprintf("unknown profile %q", pin)}
	}
	return pin, mode, nil
}

// issueLocked is Issue's body (s.mu held): resolve the binding, run the guard
// over the candidate state, then mint a fresh credential or stage a rotation.
// allowRotate=false (client add) refuses an already-active credential.
func (s *ClientsService) issueLocked(clientID string, profilePtr, modePtr *string, allowRotate bool, opts issueOptions) (*connect.IssuedCredential, error) {
	issued, _, err := s.issueLockedRecord(clientID, profilePtr, modePtr, allowRotate, opts)
	return issued, err
}

// issueLockedRecord is issueLocked that also returns the COMMITTED record of a
// fresh mint (nil for a staged rotation), so the issue path projects its view
// from it with no further store read (Spec 115 FR-007, research D6).
func (s *ClientsService) issueLockedRecord(clientID string, profilePtr, modePtr *string, allowRotate bool, opts issueOptions) (*connect.IssuedCredential, *auth.AgentToken, error) {
	all, err := s.records()
	if err != nil {
		return nil, nil, err
	}
	rec := clientRecord(all, clientID)
	if rec != nil && rec.Kind != auth.KindClient {
		return nil, nil, storage.ErrClientCredentialConflict
	}
	active := s.stateOf(rec) == profile.CredentialStateClient
	if active && !allowRotate {
		return nil, nil, &ValidationError{Field: "id", Message: fmt.Sprintf("client %s already has an active credential; rotate it instead", clientID)}
	}

	pin, mode, err := s.resolveBindingLocked(rec, profilePtr, modePtr)
	if err != nil {
		return nil, nil, err
	}

	now := s.now().UTC()
	expiresAt := now.Add(clientCredentialTTL)
	if !opts.expiresAt.IsZero() {
		expiresAt = opts.expiresAt
	}
	next := auth.AgentToken{
		Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID,
		ProfilePin: pin, ProfileMode: mode, CreatedAt: now, ExpiresAt: expiresAt, DisplayName: opts.displayName,
	}
	if active {
		next = *rec
		next.ProfilePin, next.ProfileMode = pin, mode
	}
	if err := s.checkGuard(all, clientID, &next); err != nil {
		return nil, nil, err
	}

	key, err := s.hmacKey()
	if err != nil {
		return nil, nil, err
	}
	raw, err := auth.GenerateClientToken()
	if err != nil {
		return nil, nil, err
	}
	if active {
		if _, err := s.store.StageClientCredentialRotation(clientID, raw, key); err != nil {
			return nil, nil, err
		}
		return &connect.IssuedCredential{
			Secret: raw, TokenName: rec.Name, Profile: pin, Mode: mode, Rotating: true,
			Pending: pendingBinding{pin: pin, mode: mode},
		}, nil, nil
	}
	tok, err := s.store.MintClientCredentialWith(clientID, raw, key, storage.ClientMintOptions{
		Mode: mode, Pin: pin, ExpiresAt: expiresAt, DisplayName: opts.displayName,
		Issuer: opts.issuer, Purpose: opts.purpose,
	})
	if err != nil {
		return nil, nil, err
	}
	return &connect.IssuedCredential{Secret: raw, TokenName: tok.Name, Profile: pin, Mode: mode}, tok, nil
}

// Issue implements connect.CredentialMinter.
func (m connectMinter) Issue(clientID string, intent connect.CredentialIntent) (*connect.IssuedCredential, error) {
	if !auth.ValidClientID(clientID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	if m.s.connectInFlight(clientID) {
		return nil, &ConnectInProgressError{ClientID: clientID}
	}
	issued, err := m.s.issueLocked(clientID, intent.Profile, intent.Mode, true, issueOptions{})
	if err != nil {
		return nil, err
	}
	issued.ClaimToken = m.s.claimConnect(clientID)
	return issued, nil
}

// CheckIdle implements connect.CredentialMinter: it refuses while another
// connect of clientID holds the in-flight claim, without taking one itself (a
// keyless connect mints nothing, so it has no credential to protect).
func (m connectMinter) CheckIdle(clientID string) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	if m.s.connectInFlight(clientID) {
		return &ConnectInProgressError{ClientID: clientID}
	}
	return nil
}

// PreviewBinding implements connect.CredentialMinter: the binding Issue would
// apply for this intent, minting and staging nothing.
func (m connectMinter) PreviewBinding(clientID string, intent connect.CredentialIntent) (string, string, error) {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.records()
	if err != nil {
		return "", "", err
	}
	rec := clientRecord(all, clientID)
	if rec != nil && rec.Kind != auth.KindClient {
		return "", "", storage.ErrClientCredentialConflict
	}
	return s.resolveBindingLocked(rec, intent.Profile, intent.Mode)
}

// Release implements connect.CredentialMinter: it ends the in-flight claim
// without committing or aborting, leaving an ambiguous write to the reconciler.
func (m connectMinter) Release(clientID string, issued *connect.IssuedCredential) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	if issued != nil {
		m.s.releaseConnect(clientID, issued.ClaimToken)
	}
}

// verifyIssuedLocked is Commit's fail-closed check (s.mu held): the secret it
// is about to finalize must still be the record's pending secret (rotation) or
// primary secret (fresh mint). alreadyFinal reports a rotation that was
// already finalized for this same secret.
func (s *ClientsService) verifyIssuedLocked(clientID string, issued *connect.IssuedCredential) (alreadyFinal bool, err error) {
	superseded := &CredentialSupersededError{ClientID: clientID}
	all, err := s.records()
	if err != nil {
		return false, err
	}
	rec := clientRecord(all, clientID)
	if rec == nil || rec.Kind != auth.KindClient || rec.Revoked {
		return false, superseded
	}
	key, err := s.hmacKey()
	if err != nil {
		return false, err
	}
	h := auth.HashToken(issued.Secret, key)
	switch {
	case issued.Rotating && rec.PendingHash != "" && auth.ConstantTimeEqual(h, rec.PendingHash):
		return false, nil
	case auth.ConstantTimeEqual(h, rec.TokenHash):
		return issued.Rotating, nil
	}
	return false, superseded
}

// Commit implements connect.CredentialMinter.
//
// For a rotation the requested binding is applied BEFORE the new secret is
// finalized (plan D12): a failure or crash between the two steps then leaves
// the old secret live under the old binding, never the new secret under a
// stale, possibly wider one. If finalizing fails after the binding moved, the
// previous binding is restored.
func (m connectMinter) Commit(clientID string, intent connect.CredentialIntent, issued *connect.IssuedCredential) error {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.releaseConnect(clientID, issued.ClaimToken)
	ctx := context.Background()
	a := actorOf(intent)
	alreadyFinal, err := s.verifyIssuedLocked(clientID, issued)
	if err != nil {
		return err
	}
	if !issued.Rotating {
		s.recordMint(ctx, a, clientID, issued)
		return nil
	}
	var prevPin, prevMode string
	restore := false
	if pb, ok := issued.Pending.(pendingBinding); ok {
		if all, err := s.records(); err == nil {
			if rec := clientRecord(all, clientID); rec != nil {
				prevPin, prevMode = rec.ProfilePin, rec.ProfileMode
			}
		}
		mode := pb.mode
		if _, err := s.setBindingLocked(ctx, a, clientID, pb.pin, &mode); err != nil {
			return err
		}
		restore = !alreadyFinal && (prevPin != pb.pin || prevMode != pb.mode)
	}
	if !alreadyFinal {
		if _, err := s.finalizeLocked(ctx, a, clientID); err != nil {
			if restore {
				s.restoreBindingLocked(ctx, a, clientID, prevPin, prevMode)
			}
			return err
		}
	}
	return nil
}

// restoreBindingLocked puts the previous binding back after a rotating
// connect moved it and then failed to finalize, so the old secret is not left
// under the new, possibly wider, binding. It is the mirror of setBindingLocked:
// it writes straight to the store and deliberately skips the guard and the
// profile-exists checks, because it restores a state that already held and a
// refusal must not leave the wider binding in place. Like the forward move it
// then writes a compensating profile_change record and announces the change,
// so the audit trail and the live sessions do not keep showing the new binding.
// A restore failure is logged, not returned: Commit reports the finalize error.
func (s *ClientsService) restoreBindingLocked(ctx context.Context, a Actor, clientID, prevPin, prevMode string) {
	before, after, err := s.store.UpdateClientCredentialBinding(clientID, prevPin, prevMode)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("could not restore the previous client binding after a failed rotation",
				zap.String("client_id", clientID), zap.Error(err))
		}
		return
	}
	change, diff := bindingChange(before, after)
	if diff == nil {
		diff = map[string]interface{}{}
	}
	diff["restored"] = true
	diff["reason"] = "finalize_failed"
	s.writeChange(ctx, a, changeRecord{
		change: change, profile: after.ProfilePin, previousProfile: before.ProfilePin,
		clientID: clientID, tokenName: after.Name, diff: diff,
	})
	s.announce(before, after)
}

// Abort implements connect.CredentialMinter. It rolls back only while the
// claim still belongs to this connect AND the pending (or fresh) secret is
// still the one this connect issued; otherwise another connect has taken over
// and its credential must survive (CredentialSupersededError).
func (m connectMinter) Abort(clientID string, intent connect.CredentialIntent, issued *connect.IssuedCredential) error {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ownsClaim(clientID, issued.ClaimToken) {
		return &CredentialSupersededError{ClientID: clientID}
	}
	defer s.releaseConnect(clientID, issued.ClaimToken)
	if alreadyFinal, err := s.verifyIssuedLocked(clientID, issued); err != nil {
		return err
	} else if alreadyFinal {
		return &CredentialSupersededError{ClientID: clientID}
	}
	if issued.Rotating {
		return s.rollbackLocked(context.Background(), actorOf(intent), clientID)
	}
	// A fresh mint whose config write failed: revoke it and write no record
	// (from the operator's view nothing happened, plan D13).
	_, err := s.store.ForgetClientCredentialRestoringPrior(clientID)
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
	view, err := s.forgetLockedOpt(context.Background(), actorOf(intent), clientID, map[string]interface{}{"reason": "undo"}, true)
	if err != nil {
		return "", err
	}
	return view.TokenName, nil
}
