package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Per-prompt rug-pull baseline (spec 100) — the prompt analogue of the tool
// quarantine machinery (Spec 032). It baselines ADVERTISED LIST METADATA ONLY
// (name + description + arguments); get-time prompts/get message content is out
// of scope and not baselineable here. Enforcement is by WITHHOLDING: a
// pending/changed prompt is simply not registered, which is simultaneously
// list-hide and native get-fail, so there is no runtime get-time gate.

const (
	promptStatusApproved    = storage.ToolApprovalStatusApproved
	promptStatusPending     = storage.ToolApprovalStatusPending
	promptStatusChanged     = storage.ToolApprovalStatusChanged
	promptHashSchemaVersion = 1
)

// promptTransitionReason gates promotions TO approved in enforcePromptInvariant.
type promptTransitionReason string

const (
	reasonPromptHashMatch          promptTransitionReason = "hash_match"
	reasonPromptUserApprove        promptTransitionReason = "user_approve"
	reasonPromptAutoApprove        promptTransitionReason = "auto_approve"
	reasonPromptBaselineTrust      promptTransitionReason = "baseline_trust"
	reasonPromptAutoApproveChanges promptTransitionReason = "auto_approve_changes"
)

// promptArgsJSON canonically serialises a prompt's arguments (sorted by name,
// only the identity-bearing fields) so ordering/whitespace noise and volatile
// fields (Title, Meta) never trip change detection. This is the arguments
// component of the approval hash.
func promptArgsJSON(args []mcp.PromptArgument) string {
	if len(args) == 0 {
		return ""
	}
	type normArg struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Required    bool   `json:"required,omitempty"`
	}
	norm := make([]normArg, 0, len(args))
	for _, a := range args {
		norm = append(norm, normArg{Name: a.Name, Description: a.Description, Required: a.Required})
	}
	sort.Slice(norm, func(i, j int) bool { return norm[i].Name < norm[j].Name })
	b, err := json.Marshal(norm)
	if err != nil {
		return ""
	}
	return string(b)
}

// calculatePromptApprovalHash = sha256(name | description | normalizeJSON(args)).
// Metadata only — no Meta/Title, no message content (spec 100 Non-Goals).
func calculatePromptApprovalHash(promptName, description, argsJSON string) string {
	h := sha256.New()
	h.Write([]byte(promptName))
	h.Write([]byte("|"))
	h.Write([]byte(description))
	h.Write([]byte("|"))
	h.Write([]byte(hash.NormalizeJSON(argsJSON)))
	return hex.EncodeToString(h.Sum(nil))
}

// enforcePromptInvariant is the fail-closed transition spine (spec 100 FR-3).
// Transitions AWAY from approved (→pending, →changed) are always allowed — they
// are the safe direction (withholding). A promotion TO approved must carry a
// legal reason for the current state, else it is rejected and the caller must
// NOT promote. This mirrors runtime.enforceInvariant for tools.
func enforcePromptInvariant(from, to string, reason promptTransitionReason) error {
	if to != promptStatusApproved {
		return nil
	}
	switch from {
	case promptStatusApproved, "":
		// Re-affirming approved, or first-seen→approved.
		return nil
	case promptStatusChanged:
		switch reason {
		case reasonPromptHashMatch, reasonPromptUserApprove, reasonPromptAutoApproveChanges:
			return nil
		}
	case promptStatusPending:
		switch reason {
		case reasonPromptUserApprove, reasonPromptAutoApprove, reasonPromptBaselineTrust, reasonPromptAutoApproveChanges:
			return nil
		}
	}
	return fmt.Errorf("illegal prompt approval transition %s->%s with reason %q", from, to, reason)
}

// promptApprovalResult reports which aggregated (colon-qualified) prompt names
// must be withheld from registration, plus counts for logging/inspection.
type promptApprovalResult struct {
	blocked map[string]struct{}
	pending int
	changed int
}

// checkPromptApprovals is the prompt analogue of runtime.checkToolApprovals
// (spec 100 FR-4). For each aggregated, colon-qualified prompt it computes the
// metadata hash, compares against the stored baseline, updates the record, and
// returns the set of qualified names to withhold (pending or changed). The
// prompts reaching here have already survived the TPA scan-and-drop, so this
// decides visibility based on CHANGE, not poison. It reads live config for the
// quarantine kill-switch and per-server trust.
func (p *MCPProxyServer) checkPromptApprovals(prompts []mcp.Prompt) promptApprovalResult {
	res := promptApprovalResult{blocked: map[string]struct{}{}}
	if len(prompts) == 0 || p.storage == nil {
		return res
	}

	cfg := p.currentConfig()
	quarantineEnabled := cfg != nil && cfg.IsQuarantineEnabled()

	serverCfg := map[string]*config.ServerConfig{}
	if cfg != nil {
		for _, sc := range cfg.Servers {
			if sc != nil {
				serverCfg[sc.Name] = sc
			}
		}
	}

	// hasBaseline[server] = server already has an approved/changed record (so a
	// first-seen prompt is NOT auto-approved as baseline).
	hasBaseline := map[string]bool{}
	baselineKnown := map[string]bool{}

	for _, pr := range prompts {
		serverName, promptName, ok := strings.Cut(pr.Name, ":")
		if !ok {
			continue // malformed; buildAggregatedServerPrompts drops it anyway
		}

		if !baselineKnown[serverName] {
			recs, _ := p.storage.ListPromptApprovals(serverName)
			has := false
			for _, r := range recs {
				if r.Status == promptStatusApproved || r.Status == promptStatusChanged {
					has = true
					break
				}
			}
			hasBaseline[serverName] = has
			baselineKnown[serverName] = true
		}

		argsJSON := promptArgsJSON(pr.Arguments)
		curHash := calculatePromptApprovalHash(promptName, pr.Description, argsJSON)

		existing, err := p.storage.GetPromptApproval(serverName, promptName)
		if err != nil && !errors.Is(err, storage.ErrPromptApprovalNotFound) {
			// Real read failure — fail closed (withhold) rather than register an unverified prompt.
			p.logger.Error("prompt approval read failed; withholding prompt",
				zap.String("server", serverName), zap.String("prompt", promptName), zap.Error(err))
			res.blocked[pr.Name] = struct{}{}
			continue
		}

		sc := serverCfg[serverName]
		trustAuto := sc != nil && (sc.EffectiveTrustMode() == config.TrustModeAuto || sc.IsAutoApproveToolChanges())
		// Baseline pass: a trusted (non-manual) server with no prior record treats
		// its current prompt set as the approved baseline (mirrors tool isBaselinePass).
		baselinePass := sc != nil && sc.EffectiveTrustMode() != config.TrustModeManual && !hasBaseline[serverName]

		if existing == nil {
			rec := &storage.PromptApprovalRecord{
				ServerName:         serverName,
				PromptName:         promptName,
				CurrentHash:        curHash,
				HashSchemaVersion:  promptHashSchemaVersion,
				CurrentDescription: pr.Description,
				CurrentArguments:   argsJSON,
			}
			if !quarantineEnabled || trustAuto || baselinePass {
				rec.ApprovedHash = curHash
				rec.Status = promptStatusApproved
				rec.ApprovedAt = time.Now()
				rec.ApprovedBy = "auto"
			} else {
				rec.Status = promptStatusPending
				res.blocked[pr.Name] = struct{}{}
				res.pending++
			}
			if saveErr := p.storage.SavePromptApproval(rec); saveErr != nil {
				p.logger.Error("failed to save prompt approval", zap.String("server", serverName), zap.String("prompt", promptName), zap.Error(saveErr))
			}
			hasBaseline[serverName] = true
			continue
		}

		// Capture the pre-update (last-approved-or-seen) metadata for review diffs.
		prevDesc, prevArgs := existing.CurrentDescription, existing.CurrentArguments
		existing.CurrentHash = curHash
		existing.CurrentDescription = pr.Description
		existing.CurrentArguments = argsJSON

		switch {
		case existing.ApprovedHash == curHash:
			// Matches the approved baseline (including a revert back to it) → approved.
			if existing.Status != promptStatusApproved {
				if invErr := enforcePromptInvariant(existing.Status, promptStatusApproved, reasonPromptHashMatch); invErr == nil {
					existing.Status = promptStatusApproved
					existing.PreviousDescription = ""
					existing.PreviousArguments = ""
				}
			}
		case trustAuto || !quarantineEnabled:
			// Trusted server (or quarantine off): auto re-baseline the change.
			if invErr := enforcePromptInvariant(existing.Status, promptStatusApproved, reasonPromptAutoApproveChanges); invErr == nil {
				existing.ApprovedHash = curHash
				existing.Status = promptStatusApproved
				existing.ApprovedAt = time.Now()
				existing.ApprovedBy = "auto_approve_changes"
			}
		default:
			// Genuine change on a manual-trust server → hold as changed, withhold.
			if existing.Status == promptStatusApproved {
				existing.PreviousDescription = prevDesc
				existing.PreviousArguments = prevArgs
			}
			existing.Status = promptStatusChanged
			res.blocked[pr.Name] = struct{}{}
			res.changed++
		}

		if saveErr := p.storage.SavePromptApproval(existing); saveErr != nil {
			p.logger.Error("failed to save prompt approval", zap.String("server", serverName), zap.String("prompt", promptName), zap.Error(saveErr))
		}
	}

	if res.pending > 0 || res.changed > 0 {
		p.logger.Info("prompt rug-pull baseline withheld prompts",
			zap.Int("pending", res.pending), zap.Int("changed", res.changed))
	}
	return res
}

// filterBlockedPrompts drops the withheld (pending/changed) prompts before
// registration. A withheld prompt never reaches SetPrompts → absent from
// prompts/list → prompts/get on it fails natively (spec 100 FR-5).
func filterBlockedPrompts(prompts []mcp.Prompt, blocked map[string]struct{}) []mcp.Prompt {
	if len(blocked) == 0 {
		return prompts
	}
	kept := make([]mcp.Prompt, 0, len(prompts))
	for _, pr := range prompts {
		if _, isBlocked := blocked[pr.Name]; isBlocked {
			continue
		}
		kept = append(kept, pr)
	}
	return kept
}

// ApprovePrompt approves a single held prompt: it re-baselines ApprovedHash to
// the current metadata hash and clears the withhold, then a RefreshPrompts
// re-registers it. Fails closed via enforcePromptInvariant. Reason is
// user_approve (an operator/agent explicitly approved).
func (p *MCPProxyServer) ApprovePrompt(serverName, promptName, approvedBy string) error {
	if p.storage == nil {
		return fmt.Errorf("storage unavailable")
	}
	rec, err := p.storage.GetPromptApproval(serverName, promptName)
	if err != nil {
		return err
	}
	if invErr := enforcePromptInvariant(rec.Status, promptStatusApproved, reasonPromptUserApprove); invErr != nil {
		return invErr
	}
	rec.ApprovedHash = rec.CurrentHash
	rec.Status = promptStatusApproved
	rec.ApprovedAt = time.Now()
	rec.ApprovedBy = approvedBy
	rec.PreviousDescription = ""
	rec.PreviousArguments = ""
	if err := p.storage.SavePromptApproval(rec); err != nil {
		return err
	}
	p.RefreshPrompts()
	return nil
}

// ApproveAllPrompts approves every pending/changed prompt for a server (or all
// servers when serverName is empty), then refreshes once. Returns the count
// approved. Each promotion is guarded by enforcePromptInvariant.
func (p *MCPProxyServer) ApproveAllPrompts(serverName, approvedBy string) (int, error) {
	if p.storage == nil {
		return 0, fmt.Errorf("storage unavailable")
	}
	recs, err := p.storage.ListPromptApprovals(serverName)
	if err != nil {
		return 0, err
	}
	approved := 0
	for _, rec := range recs {
		if rec.Status == promptStatusApproved {
			continue
		}
		if invErr := enforcePromptInvariant(rec.Status, promptStatusApproved, reasonPromptUserApprove); invErr != nil {
			p.logger.Warn("skipping illegal prompt approval promotion",
				zap.String("server", rec.ServerName), zap.String("prompt", rec.PromptName), zap.Error(invErr))
			continue
		}
		rec.ApprovedHash = rec.CurrentHash
		rec.Status = promptStatusApproved
		rec.ApprovedAt = time.Now()
		rec.ApprovedBy = approvedBy
		rec.PreviousDescription = ""
		rec.PreviousArguments = ""
		if err := p.storage.SavePromptApproval(rec); err != nil {
			p.logger.Error("failed to save prompt approval", zap.String("server", rec.ServerName), zap.String("prompt", rec.PromptName), zap.Error(err))
			continue
		}
		approved++
	}
	if approved > 0 {
		p.RefreshPrompts()
	}
	return approved, nil
}

// --- MCP quarantine_security prompt operations (spec 100 FR-7) ---

// handleInspectPromptApprovals returns the prompt approval records for a server
// (all servers when 'name' is omitted), with pending/changed counts so an agent
// can see which prompts the rug-pull baseline is withholding.
func (p *MCPProxyServer) handleInspectPromptApprovals(request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if p.storage == nil {
		return mcp.NewToolResultError("storage unavailable"), nil
	}
	serverName := request.GetString("name", "")
	recs, err := p.storage.ListPromptApprovals(serverName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list prompt approvals: %v", err)), nil
	}
	type promptView struct {
		Server      string `json:"server"`
		Prompt      string `json:"prompt"`
		Status      string `json:"status"`
		ChangedFrom string `json:"changed_from,omitempty"`
	}
	var pending, changed int
	views := make([]promptView, 0, len(recs))
	for _, r := range recs {
		switch r.Status {
		case promptStatusPending:
			pending++
		case promptStatusChanged:
			changed++
		}
		v := promptView{Server: r.ServerName, Prompt: r.PromptName, Status: r.Status}
		if r.Status == promptStatusChanged && r.PreviousDescription != "" {
			v.ChangedFrom = r.PreviousDescription
		}
		views = append(views, v)
	}
	payload := map[string]interface{}{
		"prompts":         views,
		"pending_count":   pending,
		"changed_count":   changed,
		"action_required": pending + changed,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to encode: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// handleApprovePromptByName approves one held prompt (re-baselines it).
func (p *MCPProxyServer) handleApprovePromptByName(request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serverName := request.GetString("name", "")
	if serverName == "" {
		return mcp.NewToolResultError("Missing required parameter 'name' (server name)"), nil
	}
	promptName := request.GetString("prompt_name", "")
	if promptName == "" {
		return mcp.NewToolResultError("Missing required parameter 'prompt_name'"), nil
	}
	if err := p.ApprovePrompt(serverName, promptName, "mcp"); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to approve prompt '%s': %v", promptName, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Prompt '%s' on server '%s' has been approved.", promptName, serverName)), nil
}

// handleApproveAllPromptsByServer approves every held prompt for a server.
func (p *MCPProxyServer) handleApproveAllPromptsByServer(request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serverName := request.GetString("name", "")
	if serverName == "" {
		return mcp.NewToolResultError("Missing required parameter 'name' (server name)"), nil
	}
	n, err := p.ApproveAllPrompts(serverName, "mcp")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to approve prompts for '%s': %v", serverName, err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Approved %d prompt(s) on server '%s'.", n, serverName)), nil
}
