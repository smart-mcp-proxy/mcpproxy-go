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

// handleListScripts godoc
// @Summary List stored code-execution scripts
// @Description List the stored scripts available to the code_execution tool. Scripts are `<name>.js` / `<name>.ts` files in the `scripts/` directory next to the active configuration file. Entries are advisory: `ok` scripts are invocable, `ambiguous` names have both extensions, and `invalid` ones report why (empty, oversized, unreadable, non-regular). Read-only — there is no write surface for stored scripts.
// @Tags code
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Stored scripts and the directory they were read from"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/code/scripts [get]
func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
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
