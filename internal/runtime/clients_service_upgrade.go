package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// The FR-025 bulk upgrade (Spec 108-f F22): every supported client whose config
// still holds the instance admin API key is moved onto a per-client `mcp_cli_`
// credential in one operation. Preview classifies and shows what each write
// would change (nothing is written, nothing minted); apply runs the writes
// behind a precondition token and the FR-008a guard over the WHOLE request.
// Each mint writes its own `assign` record through the ordinary connect minter.

// AdminKeyUpgradePort is the slice of connect.Service the upgrade uses
// (*connect.Service implements it).
type AdminKeyUpgradePort interface {
	GetStatus(clientID string) (connect.ClientStatus, error)
	PreviewWithIntent(clientID, serverName string, intent connect.CredentialIntent) (*connect.ConnectPreview, error)
	ConnectWithOptions(clientID, serverName string, opts connect.ConnectOptions) (*connect.ConnectResult, error)
}

// SetUpgradePort installs the connect port behind the admin-key upgrade.
func (s *ClientsService) SetUpgradePort(p AdminKeyUpgradePort) { s.upgrade = p }

// SetObserver installs the sink for on-demand credential classifications
// (Spec 108-f F11): REST persists the last observation per client so the
// stat-only list can still report who holds the admin key across restarts.
func (s *ClientsService) SetObserver(fn func(clientID string, state profile.CredentialState)) {
	s.observe = fn
}

// UpgradeRequest names the binding every upgraded client is given. A nil
// Profile means none: the clients get the default (All servers, switchable), so
// the FR-008a guard never has a named binding to refuse.
type UpgradeRequest struct {
	Profile *string
	Mode    *string
	// PreconditionToken, on apply, is the combined token a preview returned.
	PreconditionToken string
}

// UpgradeRow is one client the upgrade would move, as a preview shows it.
type UpgradeRow struct {
	ClientID    string                 `json:"client_id"`
	DisplayName string                 `json:"display_name"`
	DisplayPath string                 `json:"display_path,omitempty"`
	Diff        map[string]interface{} `json:"diff"`
	// Credential is the masked value the write would embed (mcp_cli_••••).
	Credential        string `json:"credential"`
	Profile           string `json:"profile"`
	Mode              string `json:"mode"`
	PreconditionToken string `json:"precondition_token"`
	Error             string `json:"error,omitempty"`
}

// UpgradeGuard reports that the request as a whole would be refused by the
// FR-008a guard.
type UpgradeGuard struct {
	Code     string       `json:"code"`
	Bindings []BindingRef `json:"bindings"`
	Fixes    []GuardFix   `json:"fixes"`
}

// UpgradePreview is the answer of POST /clients/upgrade-admin-key-holders
// without `apply`.
type UpgradePreview struct {
	Preview           []UpgradeRow  `json:"preview"`
	PreconditionToken string        `json:"precondition_token"`
	Guard             *UpgradeGuard `json:"guard,omitempty"`
	// NextStep is rotate_admin_api_key once nothing holds the admin key and no
	// client is unresolved (an unresolved client may still hold it).
	NextStep string `json:"next_step,omitempty"`
}

// UpgradeFailure is a client whose write did not go through.
type UpgradeFailure struct {
	ClientID string `json:"client_id"`
	Error    string `json:"error"`
}

// UpgradeResult is the answer of an apply.
type UpgradeResult struct {
	Upgraded []string         `json:"upgraded"`
	Failed   []UpgradeFailure `json:"failed"`
	NextStep string           `json:"next_step,omitempty"`
}

// PreconditionFailedError is the 409 precondition_failed of an apply whose
// token no longer matches what a fresh preview shows.
type PreconditionFailedError struct{}

func (e *PreconditionFailedError) Error() string {
	return "the admin-key holders changed since the preview; preview again"
}

// Code is the wire `code`.
func (e *PreconditionFailedError) Code() string { return profile.ErrorCodePreconditionFailed }

// NextStepRotateAdminKey is the follow-up once no client holds the admin key.
const NextStepRotateAdminKey = "rotate_admin_api_key"

// holderScan is the result of classifying every supported client: the clients
// known to hold the admin key, and the clients whose credential state could not
// be resolved (config read denied or malformed, or the status call failed).
// An unresolved client may still hold the admin key, so it is never treated as
// "not a holder" (fail closed, F3.1).
type holderScan struct {
	holders    []string
	unresolved map[string]string // client id -> why it could not be classified
}

func (s *ClientsService) classifyHolders() (holderScan, error) {
	scan := holderScan{unresolved: map[string]string{}}
	if s.upgrade == nil {
		return scan, connect.ErrNoCredentialMinter
	}
	for _, def := range connect.GetAllClients() {
		if !def.Supported {
			continue
		}
		status, err := s.upgrade.GetStatus(def.ID)
		if err != nil {
			scan.unresolved[def.ID] = "could not read the client's config: " + err.Error()
			continue
		}
		switch status.AccessState {
		case "denied":
			scan.unresolved[def.ID] = "the client's config could not be read (access denied); its credential is unknown"
			continue
		case "malformed":
			scan.unresolved[def.ID] = "the client's config could not be parsed; its credential is unknown"
			continue
		}
		if !status.Connected {
			continue
		}
		state := profile.CredentialState(status.CredentialState)
		if s.observe != nil && state != "" && state != profile.CredentialStateUnknown {
			s.observe(def.ID, state)
		}
		if state == profile.CredentialStateAdminKey {
			scan.holders = append(scan.holders, def.ID)
		}
	}
	sort.Strings(scan.holders)
	return scan, nil
}

// ids returns the holders and unresolved clients in one sorted list.
func (h holderScan) ids() []string {
	out := append([]string{}, h.holders...)
	for id := range h.unresolved {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s *ClientsService) upgradeIntent(a Actor, req UpgradeRequest) connect.CredentialIntent {
	return connect.CredentialIntent{
		Profile: req.Profile, Mode: req.Mode,
		ActorKind: a.Kind, ActorName: a.Name, Surface: string(a.Surface),
	}
}

func (s *ClientsService) previewUpgrade(a Actor, req UpgradeRequest) (*UpgradePreview, error) {
	if req.Profile != nil && *req.Profile != "" && !s.profileExists(s.cfg(), *req.Profile) {
		return nil, &ValidationError{Field: "profile", Message: fmt.Sprintf("unknown profile %q", *req.Profile)}
	}
	if req.Profile != nil {
		if _, err := resolveMode(*req.Profile, req.Mode, ""); err != nil {
			return nil, err
		}
	}
	scan, err := s.classifyHolders()
	if err != nil {
		return nil, err
	}
	out := &UpgradePreview{Preview: []UpgradeRow{}}
	for _, id := range scan.ids() {
		def := connect.FindClient(id)
		if why, bad := scan.unresolved[id]; bad {
			out.Preview = append(out.Preview, UpgradeRow{
				ClientID: id, DisplayName: def.Name, Diff: map[string]interface{}{}, Error: why,
			})
			continue
		}
		p, err := s.upgrade.PreviewWithIntent(id, "", s.upgradeIntent(a, req))
		row := UpgradeRow{ClientID: id, DisplayName: def.Name, Diff: map[string]interface{}{}}
		if err != nil {
			row.Error = err.Error()
			out.Preview = append(out.Preview, row)
			continue
		}
		row.DisplayPath = p.DisplayPath
		row.Credential = p.Credential
		row.Profile, row.Mode = p.Profile, p.Mode
		row.PreconditionToken = p.PreconditionToken
		row.Diff = map[string]interface{}{"entry_exists": p.EntryExists, "entry_text": p.EntryText}
		if p.ExistingEntrySummary != nil {
			row.Diff["existing_entry_summary"] = p.ExistingEntrySummary
		}
		out.Preview = append(out.Preview, row)
	}
	out.PreconditionToken = combinedUpgradeToken(out.Preview)
	if len(out.Preview) == 0 {
		out.NextStep = NextStepRotateAdminKey
	}
	return out, nil
}

// combinedUpgradeToken digests the sorted (client, row token) pairs, so the
// apply can tell that ANY holder, or any holder's pending write, changed.
func combinedUpgradeToken(rows []UpgradeRow) string {
	pairs := make([]string, 0, len(rows))
	for _, r := range rows {
		pairs = append(pairs, r.ClientID+"\x00"+r.PreconditionToken)
	}
	sort.Strings(pairs)
	h := sha256.New()
	for _, p := range pairs {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// upgradeGuard runs the FR-008a guard over the whole request: the client
// credentials as they would be with one new binding per row (only when a named
// profile is given - an All-servers binding cannot be bypassed). Rows carrying
// an error (an unresolved client, a failed preview) are never written, so they
// create no binding and are left out: the guard judges what the apply would
// actually mint.
func (s *ClientsService) upgradeGuard(rows []UpgradeRow, req UpgradeRequest) error {
	writable := make([]UpgradeRow, 0, len(rows))
	for _, r := range rows {
		if r.Error == "" {
			writable = append(writable, r)
		}
	}
	rows = writable
	if req.Profile == nil || *req.Profile == "" || len(rows) == 0 {
		return nil
	}
	all, err := s.records()
	if err != nil {
		return err
	}
	mode, err := resolveMode(*req.Profile, req.Mode, "")
	if err != nil {
		return err
	}
	cfg := s.cfg()
	current := GuardState{Config: cfg, Tokens: guardedOnly(all)}
	tokens := guardedOnly(all)
	now := s.now().UTC()
	for _, r := range rows {
		next := auth.AgentToken{
			Name: auth.ClientTokenName(r.ClientID), Kind: auth.KindClient, ClientID: r.ClientID,
			ProfilePin: *req.Profile, ProfileMode: mode, CreatedAt: now, ExpiresAt: now.Add(clientCredentialTTL),
		}
		tokens = candidateTokens(tokens, r.ClientID, &next)
	}
	return CheckBindingGuard(s.guard(), current, GuardState{Config: cfg, Tokens: tokens})
}

// PreviewAdminKeyUpgrade classifies every supported client that holds the admin
// key and shows what upgrading each would change. It writes nothing and mints
// nothing; when a named profile would make the request's bindings bypassable,
// the refusal the apply would return is reported as `guard`.
func (s *ClientsService) PreviewAdminKeyUpgrade(ctx context.Context, a Actor, req UpgradeRequest) (*UpgradePreview, error) {
	_ = ctx
	out, err := s.previewUpgrade(a, req)
	if err != nil {
		return nil, err
	}
	if gerr := s.upgradeGuard(out.Preview, req); gerr != nil {
		var refusal *BindingGuardError
		if errors.As(gerr, &refusal) {
			out.Guard = &UpgradeGuard{Code: refusal.Code(), Bindings: refusal.Bindings, Fixes: refusal.Fixes}
		}
	}
	return out, nil
}

// ApplyAdminKeyUpgrade upgrades every holder. The precondition token, when
// sent, must match a fresh preview (*PreconditionFailedError otherwise); the
// FR-008a guard runs over the whole request first (*BindingGuardError: nothing
// minted, no file written); then each client is connected with force, bound to
// its own row token, so one client's drift fails that client only.
func (s *ClientsService) ApplyAdminKeyUpgrade(ctx context.Context, a Actor, req UpgradeRequest) (*UpgradeResult, error) {
	_ = ctx
	prev, err := s.previewUpgrade(a, req)
	if err != nil {
		return nil, err
	}
	if req.PreconditionToken != "" && req.PreconditionToken != prev.PreconditionToken {
		return nil, &PreconditionFailedError{}
	}
	if gerr := s.upgradeGuard(prev.Preview, req); gerr != nil {
		return nil, gerr
	}
	res := &UpgradeResult{Upgraded: []string{}, Failed: []UpgradeFailure{}}
	for _, row := range prev.Preview {
		if row.Error != "" {
			res.Failed = append(res.Failed, UpgradeFailure{ClientID: row.ClientID, Error: row.Error})
			continue
		}
		out, err := s.upgrade.ConnectWithOptions(row.ClientID, "", connect.ConnectOptions{
			Force: true, PreconditionToken: row.PreconditionToken, Intent: s.upgradeIntent(a, req),
		})
		switch {
		case err != nil:
			res.Failed = append(res.Failed, UpgradeFailure{ClientID: row.ClientID, Error: err.Error()})
		case out == nil || !out.Success:
			msg := "the write did not go through"
			if out != nil && out.Message != "" {
				msg = out.Message
			}
			res.Failed = append(res.Failed, UpgradeFailure{ClientID: row.ClientID, Error: msg})
		default:
			res.Upgraded = append(res.Upgraded, row.ClientID)
		}
	}
	remaining, err := s.classifyHolders()
	if err == nil && len(remaining.holders) == 0 && len(remaining.unresolved) == 0 {
		res.NextStep = NextStepRotateAdminKey
	}
	return res, nil
}
