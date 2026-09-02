// Package httpapi — per-server diagnostics endpoint (spec 044).
//
// GET /api/v1/servers/{id}/diagnostics returns the per-server health status
// plus, when an active failure is present, a structured diagnostic object
// with a stable error code, user-facing message, ordered fix steps, and a
// documentation URL.
//
// Response is designed to be additive — healthy servers return the existing
// fields with an empty `diagnostic`. No fields are renamed or removed.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// redactHealthDetail returns the health value with any URL secrets scrubbed
// from its Detail string (Issue #872). The value from GetAllServers is a
// *contracts.HealthStatus; it is cloned so the shared map is not mutated. When
// reveal is true, or the value is not a health struct, it passes through as-is.
func redactHealthDetail(healthRaw interface{}, reveal bool) interface{} {
	if reveal {
		return healthRaw
	}
	hs, ok := healthRaw.(*contracts.HealthStatus)
	if !ok || hs == nil || hs.Detail == "" {
		return healthRaw
	}
	clone := *hs
	// Round 8 finding 2: oauth.ScrubUpstreamText is the ONE rule for
	// free-form upstream text. RedactSensitiveData alone is its name half, and
	// oauth.RedactServerSecretFields scrubs this very field with both halves on
	// the sibling /api/v1/servers door.
	clone.Detail = oauth.ScrubUpstreamText(clone.Detail)
	return &clone
}

// redactDiagnosticCause scrubs URL secrets from the diagnostic `cause` string
// in place (Issue #872). No-op when reveal is true or no cause is present.
func redactDiagnosticCause(diag map[string]interface{}, reveal bool) {
	if reveal || diag == nil {
		return
	}
	if cause, ok := diag["cause"].(string); ok && cause != "" {
		// Round 8 finding 2: the same one rule the sibling REST door applies.
		diag["cause"] = oauth.ScrubUpstreamText(cause)
	}
}

// handleGetServerDiagnostics returns the per-server diagnostic snapshot.
// See spec 044 / contracts/diagnostics-openapi.yaml.
func (s *Server) handleGetServerDiagnostics(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	// Reuse the already-populated server map path; this guarantees we return
	// the same `diagnostic` structure everywhere.
	allServers, err := s.controller.GetAllServers()
	if err != nil {
		s.logger.Errorw("diagnostics: failed to fetch servers", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to fetch servers")
		return
	}

	var hit map[string]interface{}
	for _, sv := range allServers {
		if name, _ := sv["name"].(string); name == serverID {
			hit = sv
			break
		}
	}
	// #1166: a server the caller may not enumerate takes the SAME exit as one
	// that does not exist — same status, same message — so the response cannot
	// be used to probe for hidden servers. This route already 404s on absent,
	// which is why the parity is reachable here and why the rest of the
	// /servers/{id} read subtree is a follow-up (those handlers return 200 for
	// a name that does not exist, so there is no 404 to be at parity WITH
	// until each grows an existence check).
	if hit == nil || !canSeeServer(r.Context(), serverID) {
		s.writeError(w, r, http.StatusNotFound, "Server not found: "+serverID)
		return
	}

	// Issue #872: health.detail and diagnostic.cause echo the raw connect
	// error, which carries the full upstream URL (query secrets and all).
	// Scrub them in parity with the /api/v1/servers list route unless the
	// operator opted out via reveal_secret_headers AND the caller is an
	// authenticated admin (#1167 — this read the flag alone, with `r` in
	// scope and its AuthContext simply never consulted).
	reveal := s.revealSecrets(r.Context())

	resp := map[string]interface{}{
		"server":    serverID,
		"connected": hit["connected"],
		"status":    hit["status"],
		"health":    redactHealthDetail(hit["health"], reveal),
	}
	// The raw map values for diagnostic fields are typed
	// (diagnostics.Code, diagnostics.Severity, []diagnostics.FixStep) which
	// JSON-marshals correctly but some downstream clients expect a plain
	// `code`/`severity` string. Normalize via a JSON round-trip.
	if diag, ok := hit["diagnostic"]; ok && diag != nil {
		var normalized map[string]interface{}
		if raw, err := json.Marshal(diag); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &normalized)
		}
		if normalized != nil {
			redactDiagnosticCause(normalized, reveal)
			resp["diagnostic"] = normalized
		} else {
			// Normalization failed (rare); still scrub the raw map if that's
			// what we're about to emit so the secret doesn't leak on this path.
			if rawMap, ok2 := diag.(map[string]interface{}); ok2 {
				redactDiagnosticCause(rawMap, reveal)
			}
			resp["diagnostic"] = diag
		}
		if code, ok2 := hit["error_code"]; ok2 {
			resp["error_code"] = fmt.Sprintf("%v", code)
		}
	} else {
		resp["diagnostic"] = nil
		resp["error_code"] = nil
	}
	// Include the catalog entry count for clients that want to sanity-check
	// the registry coverage.
	resp["catalog_size"] = len(diagnostics.All())

	s.writeSuccess(w, resp)
}

// viewString reads one string leaf out of a redacted server view (see
// oauth.RedactedConfigView), falling back to the raw value when the key was
// omitted — every string field of config.ServerConfig is `omitempty`, and an
// empty value carries no secret. It is the httpapi twin of the helper the MCP
// door uses, so the two build their echoes from the view the same way.
func viewString(view map[string]interface{}, key, fallback string) string {
	if v, ok := view[key].(string); ok {
		return v
	}
	return fallback
}

// redactedRegistrySummary renders one registry source for a REST echo with its
// URLs masked by the shared LIVE rule.
//
// Issue #1148, round 8: a CUSTOM registry source is operator-configured, so its
// URL can carry a credential in the query string exactly as an upstream URL
// can — and it was echoed verbatim by add-source / edit-source / remove-source
// and republished by `list_registries` on every surface. The write doors
// (Server.AddRegistrySource / EditRegistrySource) refuse an echoed mask rather
// than persisting it over the credential, which is the same bind-or-refuse
// answer the server write path gives.
func redactedRegistrySummary(entry *config.RegistryEntry) contracts.RegistrySummary {
	// Nil-tolerant on purpose. This renders the SUCCESS payload for three
	// registry handlers, each of which reaches it whenever the controller
	// returned no error — and a controller may legitimately report success
	// without an entry. Dereferencing there panicked inside the handler, which
	// chi's recoverer turned into a bare 500 with an EMPTY body: the caller saw
	// an unexplained server error on a request that had in fact succeeded, and
	// the real cause only appeared as a stack in the log.
	//
	// The guard lives here rather than at the three call sites so a fourth
	// caller cannot reintroduce it.
	if entry == nil {
		return contracts.RegistrySummary{}
	}
	return contracts.RegistrySummary{
		ID:         entry.ID,
		Name:       entry.Name,
		URL:        oauth.LiveRedaction.URLValue(entry.URL),
		ServersURL: oauth.LiveRedaction.URLValue(entry.ServersURL),
		Protocol:   entry.Protocol,
		Provenance: entry.Provenance,
		Trusted:    entry.IsTrusted(),
	}
}
