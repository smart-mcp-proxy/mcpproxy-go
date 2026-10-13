package httpapi

import (
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
)

// CodeScriptsResponse is the payload of GET /api/v1/code/scripts: every
// token-valid stored script found next to the active config file, plus the
// directory they were read from so a caller can see WHERE the daemon looked.
type CodeScriptsResponse struct {
	Scripts []codescripts.Entry `json:"scripts"`
	Dir     string              `json:"dir"`
}

// scriptsListingDenialMessage is the body an agent token receives from the
// stored-script listing. It names nothing about the directory.
const scriptsListingDenialMessage = "Agent tokens cannot list stored scripts (the stored-script listing is available to administrators only)"

// handleListScripts godoc
// @Summary List stored code-execution scripts
// @Description List the stored scripts available to the code_execution tool. Scripts are `<name>.js` / `<name>.ts` files in the `scripts/` directory next to the active configuration file. Entries are advisory: `ok` scripts are invocable, `ambiguous` names have both extensions, and `invalid` ones report why (empty, oversized, unreadable, non-regular). Read-only — there is no write surface for stored scripts. Administrator-only (Spec 105 FR-012): an agent token, whatever its server scope, is refused with 403 — the listing is the enumeration the missing-script error withholds from a scoped caller.
// @Tags code
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Stored scripts and the directory they were read from"
// @Failure 403 {object} contracts.ErrorResponse "Agent tokens cannot list stored scripts"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/code/scripts [get]
func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	// Spec 105 FR-012: the listing — names, count, paths AND the directory —
	// is exactly what the missing-script refusal withholds from a scoped
	// caller, so it is administrator-only here too. requireAdminRead keys on
	// the same predicate (!IsAdmin) the MCP refusal uses, so the two doors
	// share one definition of "administrator": the admin API key, the tray
	// over the socket and an absent context pass; every agent token is
	// refused before the directory is read.
	if !s.requireAdminRead(w, r, scriptsListingDenialMessage) {
		return
	}

	// The scripts directory follows the ACTIVE config file, the same authority
	// the code_execution handler resolves against — a listing that disagreed
	// with what executes would be worse than no listing at all.
	dir := codescripts.DirFor(s.controller.GetConfigPath())

	entries, err := codescripts.List(dir)
	if err != nil {
		s.getRequestLogger(r).Errorw("Failed to list stored scripts", "dir", dir, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, CodeScriptsResponse{Scripts: entries, Dir: dir})
}
