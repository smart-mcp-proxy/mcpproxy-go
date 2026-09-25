package runtime

import (
	"fmt"
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// AttentionItem, AttentionSubject and AttentionFix (the wire shapes) live in
// internal/contracts — mirroring contracts.HealthStatus beside
// internal/health.CalculateHealth — so internal/httpapi and cmd/generate-types
// can reference them without importing internal/runtime.

// AttentionServer is the only server data Compute needs (data-model.md §4).
// The subscriber builds it from the same []contracts.Server rows the
// servers.changed payload is built from.
type AttentionServer struct {
	Name        string
	Enabled     bool
	Quarantined bool
	Health      contracts.HealthStatus
	Pending     int
	Changed     int
	StateSince  time.Time
	Detail      string // e.g. "OAuth · api.githubcopilot.com" — never a secret, path, query or header
}

// AttentionClient is the only client data Compute needs. 109-d defines it so
// attention.go compiles and is testable without 109-h's client presence
// service; 109-h feeds it from ClientPresence.
type AttentionClient struct {
	ID          string
	DisplayName string
	ConnectedAt *time.Time // last successful connect write
	LastSeen    *time.Time // last MCP session mapped to this client
}

// AttentionInput is the pure snapshot Compute derives the list from.
type AttentionInput struct {
	Servers []AttentionServer
	Clients []AttentionClient
	Now     time.Time

	// ServerErrorThreshold and ClientNeverSeenThreshold override the package
	// defaults (AttentionServerErrorThreshold, AttentionClientNeverSeenThreshold)
	// when non-zero. Production callers leave these zero; tests use them to
	// exercise the threshold-crossing behaviour without waiting real minutes.
	ServerErrorThreshold     time.Duration
	ClientNeverSeenThreshold time.Duration
}

func (in AttentionInput) serverErrorThreshold() time.Duration {
	if in.ServerErrorThreshold > 0 {
		return in.ServerErrorThreshold
	}
	return AttentionServerErrorThreshold
}

func (in AttentionInput) clientNeverSeenThreshold() time.Duration {
	if in.ClientNeverSeenThreshold > 0 {
		return in.ClientNeverSeenThreshold
	}
	return AttentionClientNeverSeenThreshold
}

// Compute derives the needs-attention list from an in-memory snapshot. Pure:
// no I/O, no clock reads beyond in.Now (defaulted to time.Now() so production
// callers may omit it). Sorted by rank ascending, then subject.name
// (contracts/rest-api.md#attention).
func Compute(in AttentionInput) []contracts.AttentionItem {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	items := make([]contracts.AttentionItem, 0, len(in.Servers)+len(in.Clients))

	for _, s := range in.Servers {
		if !s.Enabled {
			continue
		}
		items = append(items, computeServerItems(s, now, in.serverErrorThreshold())...)
	}
	for _, c := range in.Clients {
		if item, ok := computeClientNeverSeen(c, now, in.clientNeverSeenThreshold()); ok {
			items = append(items, item)
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Rank != items[j].Rank {
			return items[i].Rank < items[j].Rank
		}
		return items[i].Subject.Name < items[j].Subject.Name
	})

	return items
}

func computeServerItems(s AttentionServer, now time.Time, serverErrorThreshold time.Duration) []contracts.AttentionItem {
	var out []contracts.AttentionItem
	status := s.Health.Status
	subject := contracts.AttentionSubject{Type: "server", ID: s.Name, Name: s.Name}

	switch status {
	case health.StatusSignInRequired:
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s", AttentionKindSignInRequired, s.Name),
			Kind:    AttentionKindSignInRequired,
			Rank:    AttentionRankSignInRequired,
			Subject: subject,
			Summary: fmt.Sprintf("%s: sign in required", s.Name),
			Detail:  s.Detail,
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixLogin,
				Label:  "Sign in",
				Target: fmt.Sprintf("/servers/%s", s.Name),
			},
			Since: s.StateSince,
		})
	case health.StatusNeedsSecret:
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s", AttentionKindMissingSecret, s.Name),
			Kind:    AttentionKindMissingSecret,
			Rank:    AttentionRankMissingSecret,
			Subject: subject,
			Summary: fmt.Sprintf("%s: missing secret", s.Name),
			Detail:  s.Detail,
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixSetSecret,
				Label:  "Add secret",
				Target: fmt.Sprintf("/servers/%s?tab=config&focus=env", s.Name),
			},
			Since: s.StateSince,
		})
	case health.StatusNeedsConfig:
		verb := health.ActionConfigure
		if len(s.Health.Actions) > 0 {
			verb = s.Health.Actions[0]
		}
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s", AttentionKindConfigError, s.Name),
			Kind:    AttentionKindConfigError,
			Rank:    AttentionRankConfigError,
			Subject: subject,
			Summary: fmt.Sprintf("%s: configuration error", s.Name),
			Detail:  s.Detail,
			Fix: contracts.AttentionFix{
				Verb:   verb,
				Label:  "Fix config",
				Target: fmt.Sprintf("/servers/%s?tab=config", s.Name),
			},
			Since: s.StateSince,
		})
	}

	if s.Quarantined {
		detail := s.Detail
		if status == health.StatusSignInRequired {
			detail = "Tools can be reviewed once sign-in finishes"
		}
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s", AttentionKindServerReview, s.Name),
			Kind:    AttentionKindServerReview,
			Rank:    AttentionRankServerReview,
			Subject: subject,
			Summary: fmt.Sprintf("%s: waiting for review", s.Name),
			Detail:  detail,
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixReview,
				Label:  "Review",
				Target: fmt.Sprintf("/review/%s", s.Name),
			},
			Since: s.StateSince,
		})
		// A quarantined server is never a server_error item (restarting it
		// before approval fixes nothing) and its tool_review counts are
		// covered by the server_review row above.
		return out
	}

	if (status == health.StatusError || status == health.StatusConnecting) &&
		now.Sub(s.StateSince) >= serverErrorThreshold {
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s", AttentionKindServerError, s.Name),
			Kind:    AttentionKindServerError,
			Rank:    AttentionRankServerError,
			Subject: subject,
			Summary: fmt.Sprintf("%s: connection error", s.Name),
			Detail:  s.Detail,
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixRestart,
				Label:  "Restart",
				Target: fmt.Sprintf("/servers/%s", s.Name),
			},
			Since: s.StateSince,
		})
	}

	if s.Changed > 0 {
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s:changed", AttentionKindToolReview, s.Name),
			Kind:    AttentionKindToolReview,
			Rank:    AttentionRankToolReviewChanged,
			Subject: subject,
			Summary: fmt.Sprintf("%s: %d tool(s) changed, needs review", s.Name, s.Changed),
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixReview,
				Label:  "Review",
				Target: fmt.Sprintf("/review/%s?change=changed", s.Name),
			},
			Since: s.StateSince,
		})
	}
	if s.Pending > 0 {
		out = append(out, contracts.AttentionItem{
			ID:      fmt.Sprintf("%s:server:%s:pending", AttentionKindToolReview, s.Name),
			Kind:    AttentionKindToolReview,
			Rank:    AttentionRankToolReviewPending,
			Subject: subject,
			Summary: fmt.Sprintf("%s: %d new tool(s), needs review", s.Name, s.Pending),
			Fix: contracts.AttentionFix{
				Verb:   AttentionFixReview,
				Label:  "Review",
				Target: fmt.Sprintf("/review/%s?change=pending", s.Name),
			},
			Since: s.StateSince,
		})
	}

	return out
}

func computeClientNeverSeen(c AttentionClient, now time.Time, clientNeverSeenThreshold time.Duration) (contracts.AttentionItem, bool) {
	if c.ConnectedAt == nil {
		return contracts.AttentionItem{}, false
	}
	if now.Sub(*c.ConnectedAt) < clientNeverSeenThreshold {
		return contracts.AttentionItem{}, false
	}
	if c.LastSeen != nil && !c.LastSeen.Before(*c.ConnectedAt) {
		// A session since the connect write proves the client loaded MCPProxy.
		return contracts.AttentionItem{}, false
	}
	name := c.DisplayName
	if name == "" {
		name = c.ID
	}
	return contracts.AttentionItem{
		ID:      fmt.Sprintf("%s:client:%s", AttentionKindClientNeverSeen, c.ID),
		Kind:    AttentionKindClientNeverSeen,
		Rank:    AttentionRankClientNeverSeen,
		Subject: contracts.AttentionSubject{Type: "client", ID: c.ID, Name: name},
		Summary: fmt.Sprintf("%s: connected, never seen", name),
		Detail:  fmt.Sprintf("Restart %s to load MCPProxy", name),
		Fix: contracts.AttentionFix{
			Verb:   AttentionFixReloadHint,
			Label:  "How to restart",
			Target: fmt.Sprintf("/clients?focus=%s", c.ID),
		},
		Since: *c.ConnectedAt,
	}, true
}
