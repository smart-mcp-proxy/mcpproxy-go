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

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

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
	if hit == nil {
		s.writeError(w, r, http.StatusNotFound, "Server not found: "+serverID)
		return
	}

	resp := map[string]interface{}{
		"server":    serverID,
		"connected": hit["connected"],
		"status":    hit["status"],
		"health":    hit["health"],
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
			resp["diagnostic"] = normalized
		} else {
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
