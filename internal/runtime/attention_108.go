package runtime

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 109-l (FR-093): the Spec 108 client/credential/guard warnings as
// needs-attention items. The source is the SAME ClientsService.Warnings call
// GET /api/v1/clients serves, so the attention list and the Clients banner can
// never disagree.

// attentionGuardSubject is the instance-level subject of the binding-guard
// item (P5): pinning it to a client would change its id whenever the bound set
// changes.
const (
	attentionGuardSettingID   = "require_mcp_auth"
	attentionGuardSettingName = "Anonymous callers"
	// attentionGuardNamesShown is how many bound client names the guard detail
	// lists before collapsing the rest into "+N more".
	attentionGuardNamesShown = 3
)

// AttentionClientWarning is the only Spec 108 warning data Compute needs.
type AttentionClientWarning struct {
	Code         string // a profile.Warning* value
	ClientID     string
	DisplayName  string
	Profile      string     // profile_missing: the missing profile slug
	ExpiresAt    *time.Time // client_credential_expiring
	BindingCount int        // anonymous_denied_by_binding_guard
	BindingNames []string   // display names of the bound clients, for the guard detail
	// Since is when the subscriber first saw this warning. Ignored for
	// client_credential_expiring, whose since is ExpiresAt minus the warning
	// window. Zero means "now".
	Since time.Time
}

func (w AttentionClientWarning) name() string {
	if w.DisplayName != "" {
		return w.DisplayName
	}
	return w.ClientID
}

// attentionWarningKey is the stable identity of a warning across recomputes
// (the item id minus nothing: (code, client id)). The subscriber keys its
// firstSeen map on it.
func attentionWarningKey(w AttentionClientWarning) string {
	return w.Code + "\x00" + w.ClientID
}

func computeClientWarningItems(warnings []AttentionClientWarning, now time.Time) []contracts.AttentionItem {
	var out []contracts.AttentionItem
	for _, w := range warnings {
		if item, ok := computeClientWarningItem(w, now); ok {
			out = append(out, item)
		}
	}
	return out
}

func computeClientWarningItem(w AttentionClientWarning, now time.Time) (contracts.AttentionItem, bool) {
	since := w.Since
	if since.IsZero() {
		since = now
	}
	name := w.name()
	clientSubject := contracts.AttentionSubject{Type: "client", ID: w.ClientID, Name: name}
	focus := "/clients?focus=" + url.QueryEscape(w.ClientID)
	id := func(kind string) string { return fmt.Sprintf("%s:client:%s", kind, w.ClientID) }

	switch w.Code {
	case AttentionKindAnonymousDeniedByBindingGuard:
		detail := "Turn on authentication, or set anonymous callers to a profile no wider than the bindings."
		if len(w.BindingNames) > 0 {
			shown := w.BindingNames
			more := 0
			if len(shown) > attentionGuardNamesShown {
				more = len(shown) - attentionGuardNamesShown
				shown = shown[:attentionGuardNamesShown]
			}
			bound := "Bound: " + strings.Join(shown, ", ")
			if more > 0 {
				bound += fmt.Sprintf(" (+%d more)", more)
			}
			detail = bound + ". " + detail
		}
		return contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:setting:%s", AttentionKindAnonymousDeniedByBindingGuard, attentionGuardSettingID),
			Kind:    AttentionKindAnonymousDeniedByBindingGuard,
			Rank:    AttentionRankAnonymousDenied,
			Subject: contracts.AttentionSubject{Type: "setting", ID: attentionGuardSettingID, Name: attentionGuardSettingName},
			Summary: "Anonymous callers are denied: client bindings could be bypassed without authentication",
			Detail:  detail,
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixChangeSetting,
				Label:  "Require authentication…",
				Target: "/settings?tab=security&focus=" + attentionGuardSettingID,
			},
			Since: since,
		}, true
	case AttentionKindClientHoldsAdminKey:
		return contracts.AttentionItem{
			ID: id(AttentionKindClientHoldsAdminKey), Kind: AttentionKindClientHoldsAdminKey,
			Rank: AttentionRankClientHoldsAdminKey, Subject: clientSubject,
			Summary: name + ": holds the admin API key",
			Detail:  "Upgrade it to a client credential, then rotate the admin API key.",
			Fix:     contracts.AttentionFix{Verb: AttentionFixUpgradeAdminKeyHolders, Label: "Upgrade…", Target: focus},
			Since:   since,
		}, true
	case AttentionKindClientTokenNameConflict:
		return contracts.AttentionItem{
			ID: id(AttentionKindClientTokenNameConflict), Kind: AttentionKindClientTokenNameConflict,
			Rank: AttentionRankClientTokenNameConflict, Subject: clientSubject,
			Summary: fmt.Sprintf("%s: token name %s is held by an agent token", name, auth.ClientTokenName(w.ClientID)),
			Detail:  fmt.Sprintf("Revoke or delete that token, then connect %s again.", name),
			Fix: contracts.AttentionFix{
				Verb: AttentionFixEditToken, Label: "Open token",
				Target: "/clients?tab=tokens&token=" + url.QueryEscape(auth.ClientTokenName(w.ClientID)),
			},
			Since: since,
		}, true
	case AttentionKindProfileMissing:
		return contracts.AttentionItem{
			ID: id(AttentionKindProfileMissing), Kind: AttentionKindProfileMissing,
			Rank: AttentionRankProfileMissing, Subject: clientSubject,
			Summary: fmt.Sprintf("%s: bound to missing profile %s — denied everything", name, w.Profile),
			Detail:  "Move it to an existing profile.",
			Fix:     contracts.AttentionFix{Verb: AttentionFixMoveClient, Label: "Move client…", Target: focus},
			Since:   since,
		}, true
	case AttentionKindClientRotationPending:
		return contracts.AttentionItem{
			ID: id(AttentionKindClientRotationPending), Kind: AttentionKindClientRotationPending,
			Rank: AttentionRankClientRotationPending, Subject: clientSubject,
			Summary: name + ": credential rotation not finished",
			Detail:  fmt.Sprintf("Reconnect %s or finalize the rotation.", name),
			Fix:     contracts.AttentionFix{Verb: AttentionFixReconnectClient, Label: "Reconnect…", Target: focus},
			Since:   since,
		}, true
	case AttentionKindClientCredentialExpiring:
		summary := name + ": client credential expires soon"
		if w.ExpiresAt != nil {
			summary = fmt.Sprintf("%s: client credential expires %s", name, w.ExpiresAt.UTC().Format("2006-01-02"))
			since = w.ExpiresAt.Add(-clientCredentialExpiringWindow)
		}
		return contracts.AttentionItem{
			ID: id(AttentionKindClientCredentialExpiring), Kind: AttentionKindClientCredentialExpiring,
			Rank: AttentionRankClientCredentialExpiring, Subject: clientSubject,
			Summary: summary,
			Detail:  "Reconnect to issue a new credential.",
			Fix:     contracts.AttentionFix{Verb: AttentionFixReconnectClient, Label: "Reconnect…", Target: focus},
			Since:   since,
		}, true
	}
	return contracts.AttentionItem{}, false
}

// AttentionClientWarnings derives the Spec 108 warnings for the attention
// list. It calls the same ClientsService.Warnings GET /clients uses, with the
// admin-key states from the PERSISTED last observations (108-f F11), and never
// reads a client config file (Spec 075: no macOS App-Data prompt). The server
// edition has no clients service, so the result is empty.
func (r *Runtime) AttentionClientWarnings() []AttentionClientWarning {
	warnings, _ := r.attentionClientState()
	return warnings
}

// attentionClientSource is what the subscriber reads each recompute: the
// current warnings plus the credential expiries that have not yet entered the
// warning window (so the threshold timer can wake exactly when one does).
type attentionClientSource func() (warnings []AttentionClientWarning, upcomingExpiries []time.Time)

func (r *Runtime) attentionClientState() ([]AttentionClientWarning, []time.Time) {
	svc := r.clientsService
	if svc == nil || r.storageManager == nil {
		return nil, nil
	}
	var observed map[string]storage.ClientCredentialObservation
	if state, err := r.GetOnboardingState(); err == nil && state != nil {
		observed = state.ClientCredentialObserved
	}
	states, err := svc.ObservedCredentialStates(observed)
	if err != nil {
		return nil, nil
	}
	warnings := svc.Warnings(states)
	records, _ := svc.Records()
	var upcoming []time.Time
	now := svc.Now()
	for i := range records {
		t := &records[i]
		if t.Kind == auth.KindClient && svc.StateOf(t) == profile.CredentialStateClient && t.ExpiresAt.Sub(now) > clientCredentialExpiringWindow {
			upcoming = append(upcoming, t.ExpiresAt)
		}
	}
	if len(warnings) == 0 {
		return nil, upcoming
	}
	recByClient := map[string]*auth.AgentToken{}
	for i := range records {
		if records[i].Kind == auth.KindClient {
			recByClient[records[i].ClientID] = &records[i]
		}
	}
	display := func(clientID string) string {
		if def := connect.FindClient(clientID); def != nil && def.Name != "" {
			return def.Name
		}
		if rec := recByClient[clientID]; rec != nil && rec.DisplayName != "" {
			return rec.DisplayName
		}
		return clientID
	}

	out := make([]AttentionClientWarning, 0, len(warnings))
	for _, w := range warnings {
		aw := AttentionClientWarning{Code: string(w.Code), ClientID: w.ClientID}
		if w.ClientID != "" {
			aw.DisplayName = display(w.ClientID)
		}
		switch w.Code {
		case profile.WarningAnonymousDeniedByBindingGuard:
			aw.BindingCount = len(w.Bindings)
			for _, b := range w.Bindings {
				aw.BindingNames = append(aw.BindingNames, display(b.ClientID))
			}
		case profile.WarningProfileMissing:
			if rec := recByClient[w.ClientID]; rec != nil {
				aw.Profile = rec.ProfilePin
			}
		case profile.WarningClientCredentialExpiring:
			if rec := recByClient[w.ClientID]; rec != nil {
				exp := rec.ExpiresAt
				aw.ExpiresAt = &exp
			}
		}
		out = append(out, aw)
	}
	return out, upcoming
}
