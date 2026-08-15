package preflight

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolannotations"
)

// ---------------------------------------------------------------------------
// Narrow read interfaces
//
// Every input the evaluator needs arrives through one of these four
// interfaces. They are deliberately minimal and read-only: there is no method
// here that can connect, reconnect, index, approve or write anything, which is
// what makes FR-006 (zero upstream I/O, zero runtime mutation) structural
// rather than a promise. Implementations live in the glue layer.
// ---------------------------------------------------------------------------

// IndexedTool is the slice of indexed metadata the evaluator uses: identity and
// annotations. Name may be either "server:tool" or the bare tool name — the
// evaluator normalizes both, exactly like the search paths do.
type IndexedTool struct {
	Name        string
	Annotations *config.ToolAnnotations
}

// IndexReader reads the SHARED search index. Note what is absent: no
// per-profile index accessor. index.Manager.ForProfile lazily CREATES and
// caches a profile index — a mutation — so a preflight must never call it.
// Profile semantics here are "shared-index existence + profile scope filter".
type IndexReader interface {
	// ToolsByServer returns the indexed tools for one server. A server with no
	// indexed tools returns an empty slice and a nil error.
	ToolsByServer(serverName string) ([]IndexedTool, error)
	// IndexedServerNames returns every server present in the shared index.
	IndexedServerNames() ([]string, error)
}

// ApprovalReader reads spec 032 tool-approval records.
type ApprovalReader interface {
	// ToolApproval returns the record for a tool, or (nil, nil) when no record
	// exists (the implicit-approved default). It must return an error ONLY for
	// genuine infrastructure failures — "no record" is not an error.
	ToolApproval(serverName, toolName string) (*ApprovalState, error)
}

// ServerRuntimeState is the evaluator's normalized connection state. It mirrors
// upstream/types.ConnectionState without importing it, so this package stays
// free of anything that can dial a socket.
type ServerRuntimeState string

const (
	// RuntimeStateUnknown means the snapshot has no usable state for the
	// server. The evaluator then makes NO connection-state claim.
	RuntimeStateUnknown ServerRuntimeState = ""
	RuntimeStateReady   ServerRuntimeState = "ready"
	// RuntimeStateConnecting / Discovering / Authenticating are the
	// server-level "initializing" states (research D4). No per-tool indexing
	// progress is ever claimed.
	RuntimeStateConnecting     ServerRuntimeState = "connecting"
	RuntimeStateDiscovering    ServerRuntimeState = "discovering"
	RuntimeStateAuthenticating ServerRuntimeState = "authenticating"
	// RuntimeStatePendingAuth is the deferred-OAuth state (FR-007): mapped to
	// oauth_required explicitly, BEFORE any health fallthrough, because waiting
	// cannot help without a login.
	RuntimeStatePendingAuth  ServerRuntimeState = "pending_auth"
	RuntimeStateDisconnected ServerRuntimeState = "disconnected"
	RuntimeStateError        ServerRuntimeState = "error"
)

// ServerRuntime is the read-only connection view of one server.
type ServerRuntime struct {
	State ServerRuntimeState
	// Detail is an occurrence-specific human note (e.g. the last error, or a
	// spec 044 diagnostic message). Optional.
	Detail string
	// Action optionally overrides the default action for server_unhealthy with
	// a best-effort suggestion from the spec 044 diagnostic classifier
	// (restart / login / view_logs). Empty means "use the default".
	Action string
}

// StateReader reads the connection-state snapshot (stateview). It is a snapshot
// read: lock-free, never blocking, never triggering a connect.
type StateReader interface {
	// ServerRuntime returns the runtime view of a server; found=false when the
	// snapshot has no entry for it.
	ServerRuntime(serverName string) (rt ServerRuntime, found bool)
}

// ConfigPolicy reads configuration-derived policy.
type ConfigPolicy interface {
	// ServerPolicy returns the config view of a server. A missing server is
	// reported as ServerPolicy{Found: false}, NOT as an error.
	ServerPolicy(serverName string) (ServerPolicy, error)
	// ToolConfigDenied reports the enabled_tools / disabled_tools verdict.
	ToolConfigDenied(serverName, toolName string) (bool, error)
	// QuarantineEnabled is the global quarantine switch.
	QuarantineEnabled() bool
}

// ---------------------------------------------------------------------------
// Request / result types
// ---------------------------------------------------------------------------

// ToolRef is one requested id, optionally hash-pinned.
type ToolRef struct {
	ID      string
	PinHash string
}

// EvalContext carries everything one evaluation needs. It is a value: build it
// per request, never share it across requests.
type EvalContext struct {
	Index     IndexReader
	Approvals ApprovalReader
	State     StateReader
	Policy    ConfigPolicy

	// Tier selects the disclosure rules (FR-013).
	Tier Tier
	// Scope is the effective evaluation scope (token scope ∩ token pin ∩
	// requested profile — see ResolveScope). nil means unrestricted.
	Scope *Scope
	// Filters are the caller's annotation policy filters (spec 094 semantics).
	Filters toolannotations.Filters
	// Pins maps a requested id to its pin string, for callers that carry pins
	// separately from the refs. A ToolRef.PinHash always wins over this map.
	Pins map[string]string
}

// Result is one per-tool verdict. It mirrors the wire DTO minus serialization
// concerns; failure fields are empty for a ready result.
type Result struct {
	ID          string
	Status      Status
	Reason      Reason
	Retryable   bool
	Action      string
	Detail      string
	Remediation string
	// Hash is the tool's current pin string ("sha256/v{N}:{hex}") — operator
	// tier, ready results only. Never populated for the agent-token tier.
	Hash string
	// DidYouMean is populated only on not_found, from the caller-visible corpus.
	DidYouMean []string
}

// Canonical wording. The not_found texts are constants because FR-013 requires
// an out-of-scope result at the agent-token tier to be byte-indistinguishable
// from an ordinary not_found — same reason, retryable, action, detail and
// remediation. They are produced by one constructor (notFoundResult) for
// exactly that reason.
const (
	detailNotFound    = "No tool with this id is available."
	detailMalformedID = "Malformed tool id: expected the format <server>:<tool>."
)

// Evaluate answers one verdict per requested ref, in request order.
//
// It returns an error — never a fabricated reason code — when an underlying
// read fails (index, approvals, config policy) or the context is cancelled. The
// served surface answers 503 in that case: a reason code is a statement about
// proxy state, and inventing one from a failed read would poison the taxonomy.
func Evaluate(ctx context.Context, ec EvalContext, refs []ToolRef) ([]Result, error) {
	results := make([]Result, 0, len(refs))
	corpus := &visibleCorpus{ec: &ec}

	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := evaluateOne(&ec, ref, corpus)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

// evaluateOne walks the FR-004 precedence chain for a single id. The order of
// the blocks below IS the normative chain — do not reorder without changing the
// spec.
func evaluateOne(ec *EvalContext, ref ToolRef, corpus *visibleCorpus) (Result, error) {
	id := strings.TrimSpace(ref.ID)

	serverName, toolName, ok := splitToolID(id)
	if !ok {
		// A malformed id is a per-ID verdict, never a request-level error: one
		// bad entry must not mask the verdicts of the rest (spec Edge Cases).
		return unavailable(id, ReasonNotFound, detailMalformedID), nil
	}

	policy, err := ec.Policy.ServerPolicy(serverName)
	if err != nil {
		return Result{}, fmt.Errorf("preflight: read server policy for %q: %w", serverName, err)
	}

	// 1. server_not_configured — operator tier only. At the agent-token tier
	//    this is scope-silenced into the SAME not_found an out-of-scope server
	//    produces: if the two answers differed, a token could probe arbitrary
	//    names and learn which servers exist behind its scope (the exact leak
	//    FR-013 forbids).
	if !policy.Found {
		if ec.Tier == TierAgentToken {
			return corpus.notFoundResult(id)
		}
		return unavailable(id, ReasonServerNotConfigured,
			fmt.Sprintf("No upstream server named %q is configured.", serverName)), nil
	}

	// 2. server_not_in_scope (operator tier) — the agent-token tier gets
	//    scope-silence: the SAME construction an absent tool produces.
	if !ec.Scope.Allows(serverName) {
		if ec.Tier == TierAgentToken {
			return corpus.notFoundResult(id)
		}
		detail := fmt.Sprintf("Server %q is outside the evaluated scope; a session under this scope sees this id as not_found.", serverName)
		if name := ec.Scope.Name(); name != "" {
			detail = fmt.Sprintf("Server %q is outside profile %q; a session pinned to that profile sees this id as not_found.", serverName, name)
		}
		return unavailable(id, ReasonServerNotInScope, detail), nil
	}

	// 3. server_quarantined — a quarantined server's tools are never indexed,
	//    so existence below it is unknowable, which is exactly why it outranks
	//    not_found.
	if policy.Quarantined {
		return unavailable(id, ReasonServerQuarantined,
			fmt.Sprintf("Server %q is quarantined; its tools are withheld pending review.", serverName)), nil
	}

	// 4. server_disabled
	if !policy.Enabled {
		return unavailable(id, ReasonServerDisabled,
			fmt.Sprintf("Server %q is disabled.", serverName)), nil
	}

	// 5. not_found — exact-id existence. The shared index is the primary
	//    source, but it is NOT authoritative on its own: the runtime
	//    de-indexes a tool the moment it becomes blocked, pending, or changed
	//    (spec 032), so a spec-032 approval record is equally valid evidence
	//    the tool exists upstream. When a record exists the chain falls
	//    through to the tool-level gates instead of reporting a misleading
	//    not_found (with an actively harmful did_you_mean). And when the
	//    server is not Ready, existence is unknowable — the connection-state
	//    verdict is returned instead of not_found (FR-005: never claim
	//    per-tool knowledge the runtime does not have).
	indexed, err := lookupIndexed(ec, serverName, toolName)
	if err != nil {
		return Result{}, err
	}
	approval, err := ec.Approvals.ToolApproval(serverName, toolName)
	if err != nil {
		return Result{}, fmt.Errorf("preflight: read tool approval for %q: %w", id, err)
	}
	if indexed == nil && approval == nil {
		if res, notReady := connectionVerdict(ec, id, serverName); notReady {
			return res, nil
		}
		return corpus.notFoundResult(id)
	}

	configDenied, err := ec.Policy.ToolConfigDenied(serverName, toolName)
	if err != nil {
		return Result{}, fmt.Errorf("preflight: read tool config policy for %q: %w", id, err)
	}

	// 6-9. tool_denied_by_config → tool_blocked_by_user → tool_changed →
	//      tool_pending_approval, all from the shared classifier so preflight
	//      and dispatch cannot disagree (FR-002).
	class := ClassifyTool(ClassifyInputs{
		Server:            policy,
		QuarantineEnabled: ec.Policy.QuarantineEnabled(),
		ConfigDenied:      configDenied,
		Approval:          approval,
	})
	if !class.Callable() {
		return unavailable(id, class.Reason(), classDetail(class, serverName, toolName)), nil
	}

	// 10. hash_mismatch — evaluated only now that the tool is known to exist
	//     (FR-004): earlier states win over a pin failure.
	if pin := pinFor(ec, ref, id); pin != "" {
		if res, mismatch := checkPin(id, pin, approval, ec.Tier); mismatch {
			return res, nil
		}
	}

	// 11-13. Connection state: oauth_required → server_unhealthy →
	//        server_initializing. All server-level (FR-005).
	if res, unhealthy := connectionVerdict(ec, id, serverName); unhealthy {
		return res, nil
	}

	// 14. Annotation filters (spec 094 order: read_only_only →
	//     exclude_destructive → exclude_open_world; the first filter that
	//     excludes owns the omission).
	if ec.Filters.Any() {
		// A known-but-de-indexed tool (approval record, no index entry) has no
		// readable annotations; nil is exactly the spec-094 missing-annotation
		// case, so the shared classifier handles it without a special path.
		var annotations *config.ToolAnnotations
		if indexed != nil {
			annotations = indexed.Annotations
		}
		if filterKey, explicit, excluded := toolannotations.ExcludeReasonFor(annotations, ec.Filters); excluded {
			reason := ReasonMissingAnnotation
			detail := fmt.Sprintf("Filter %s omits this tool: the upstream definition does not declare the required annotation.", filterKey)
			if explicit {
				reason = ReasonPolicyFiltered
				detail = fmt.Sprintf("Filter %s omits this tool: it is explicitly annotated as unsafe for this filter.", filterKey)
			}
			return unavailable(id, reason, detail), nil
		}
	}

	// 15. ready
	res := Result{ID: id, Status: StatusReady}
	// Hash disclosure is operator-tier only (FR-013) and only on ready results.
	if ec.Tier != TierAgentToken && approval != nil && approval.CurrentHash != "" {
		res.Hash = FormatPin(approval.HashSchemaVersion, approval.CurrentHash)
	}
	return res, nil
}

// unavailable builds a failure result from the taxonomy defaults.
func unavailable(id string, reason Reason, detail string) Result {
	return Result{
		ID:          id,
		Status:      StatusUnavailable,
		Reason:      reason,
		Retryable:   Retryable(reason),
		Action:      DefaultAction(reason),
		Detail:      detail,
		Remediation: DefaultRemediation(reason),
	}
}

// classDetail renders the occurrence-specific note for a classifier verdict.
func classDetail(class ToolClass, serverName, toolName string) string {
	id := serverName + ":" + toolName
	switch class {
	case ToolClassDeniedByConfig:
		return fmt.Sprintf("Tool %q is denied by the server's enabled_tools/disabled_tools policy.", id)
	case ToolClassBlockedByUser:
		return fmt.Sprintf("Tool %q was disabled in mcpproxy.", id)
	case ToolClassChanged:
		return fmt.Sprintf("Tool %q changed after approval (rug-pull guard); it is locked pending review.", id)
	case ToolClassPendingApproval:
		return fmt.Sprintf("Tool %q is pending security approval.", id)
	case ToolClassServerNotConfigured, ToolClassServerQuarantined, ToolClassServerDisabled, ToolClassReady:
		return ""
	default:
		return ""
	}
}

// connectionVerdict applies the three connection-state gates. found=false in the
// snapshot means the evaluator makes no claim at all: absence of runtime
// information is not evidence of ill health (and the served surface refuses with
// 503 when the runtime is unavailable entirely, FR-006).
func connectionVerdict(ec *EvalContext, id, serverName string) (Result, bool) {
	if ec.State == nil {
		return Result{}, false
	}
	rt, found := ec.State.ServerRuntime(serverName)
	if !found {
		return Result{}, false
	}

	switch rt.State {
	case RuntimeStatePendingAuth:
		// FR-007: explicit map, ahead of any health fallthrough.
		res := unavailable(id, ReasonOAuthRequired,
			fmt.Sprintf("Server %q is waiting for OAuth login.", serverName))
		if rt.Detail != "" {
			res.Detail = rt.Detail
		}
		return res, true

	case RuntimeStateError, RuntimeStateDisconnected:
		detail := fmt.Sprintf("Server %q is not connected.", serverName)
		if rt.Detail != "" {
			detail = rt.Detail
		}
		res := unavailable(id, ReasonServerUnhealthy, detail)
		if action := normalizeHealthAction(rt.Action); action != "" {
			res.Action = action
		}
		return res, true

	case RuntimeStateConnecting, RuntimeStateDiscovering, RuntimeStateAuthenticating:
		// Server-level only — never a per-tool claim about indexing progress.
		detail := fmt.Sprintf("Server %q is still connecting or discovering its tools.", serverName)
		if rt.Detail != "" {
			detail = rt.Detail
		}
		return unavailable(id, ReasonServerInitializing, detail), true

	case RuntimeStateReady, RuntimeStateUnknown:
		return Result{}, false

	default:
		return Result{}, false
	}
}

// normalizeHealthAction accepts only the existing health-action vocabulary for
// the best-effort server_unhealthy override; anything else falls back to the
// taxonomy default.
func normalizeHealthAction(action string) string {
	switch action {
	case health.ActionRestart, health.ActionLogin, health.ActionViewLogs:
		return action
	default:
		return ""
	}
}

// lookupIndexed resolves an exact (server, tool) pair against the shared index.
// Exact match only — no fuzzy resolution, no live ListTools fallback.
func lookupIndexed(ec *EvalContext, serverName, toolName string) (*IndexedTool, error) {
	if ec.Index == nil {
		return nil, fmt.Errorf("preflight: no index reader configured")
	}
	tools, err := ec.Index.ToolsByServer(serverName)
	if err != nil {
		return nil, fmt.Errorf("preflight: read index for server %q: %w", serverName, err)
	}
	full := serverName + ":" + toolName
	for i := range tools {
		if tools[i].Name == full || tools[i].Name == toolName {
			t := tools[i]
			return &t, nil
		}
	}
	return nil, nil
}

// splitToolID splits a canonical "<server>:<tool>" id. Whitespace is never
// significant, so both segments are trimmed; ok=false when either is blank.
func splitToolID(id string) (serverName, toolName string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(id), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	serverName, toolName = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if serverName == "" || toolName == "" {
		return "", "", false
	}
	return serverName, toolName, true
}

// pinFor resolves the pin for a ref: an explicit ToolRef.PinHash wins over the
// EvalContext.Pins map.
func pinFor(ec *EvalContext, ref ToolRef, id string) string {
	if ref.PinHash != "" {
		return ref.PinHash
	}
	if ec.Pins == nil {
		return ""
	}
	return ec.Pins[id]
}

// ---------------------------------------------------------------------------
// Hash pins (FR-011, research D4)
// ---------------------------------------------------------------------------

const pinPrefix = "sha256/v"

// FormatPin renders a stored hash as the wire pin format
// "sha256/v{HashSchemaVersion}:{hex}".
func FormatPin(schemaVersion uint64, hash string) string {
	return fmt.Sprintf("%s%d:%s", pinPrefix, schemaVersion, hash)
}

// ParsePin decodes a pin string. The schema version is part of the format so a
// proxy-side hash-algorithm bump is distinguishable from genuine upstream drift
// (it changes `detail`, not the reason code).
func ParsePin(pin string) (schemaVersion uint64, hash string, err error) {
	rest, found := strings.CutPrefix(strings.TrimSpace(pin), pinPrefix)
	if !found {
		return 0, "", fmt.Errorf("invalid pin %q: expected %s<N>:<hex>", pin, pinPrefix)
	}
	verStr, hexStr, ok := strings.Cut(rest, ":")
	if !ok || verStr == "" || hexStr == "" {
		return 0, "", fmt.Errorf("invalid pin %q: expected %s<N>:<hex>", pin, pinPrefix)
	}
	v, convErr := strconv.ParseUint(verStr, 10, 64)
	if convErr != nil {
		return 0, "", fmt.Errorf("invalid pin %q: schema version is not a number", pin)
	}
	return v, hexStr, nil
}

// checkPin compares a supplied pin against the tool's current stored hash.
// mismatch=false means the pin matches (or the caller supplied none).
//
// Fail-closed cases (all reported as hash_mismatch, distinguished in `detail`):
// an unparseable pin, a schema-version bump, no stored hash to verify against,
// and genuine drift. A pin that cannot be verified must never pass a gate whose
// entire purpose is to detect definition drift.
func checkPin(id, pin string, approval *ApprovalState, tier Tier) (Result, bool) {
	pinVersion, pinHash, err := ParsePin(pin)
	if err != nil {
		return unavailable(id, ReasonHashMismatch,
			fmt.Sprintf("Invalid pin format: expected %s<N>:<hex>.", pinPrefix)), true
	}
	if approval == nil || approval.CurrentHash == "" {
		return unavailable(id, ReasonHashMismatch,
			"No stored hash is available for this tool, so the pin cannot be verified; re-pin from the tool's current definition."), true
	}
	if approval.HashSchemaVersion != pinVersion {
		return unavailable(id, ReasonHashMismatch,
			fmt.Sprintf("Hash schema changed (proxy upgrade): pin uses schema v%d, the proxy now stores v%d. Relock the pin.",
				pinVersion, approval.HashSchemaVersion)), true
	}
	if approval.CurrentHash != pinHash {
		// Hashes are operator-tier disclosure only (FR-013).
		detail := "The pinned hash does not match the tool's current definition."
		if tier != TierAgentToken {
			detail = fmt.Sprintf("Pinned %s but the tool's current hash is %s.",
				FormatPin(pinVersion, pinHash), FormatPin(approval.HashSchemaVersion, approval.CurrentHash))
		}
		return unavailable(id, ReasonHashMismatch, detail), true
	}
	return Result{}, false
}

// ---------------------------------------------------------------------------
// not_found + did_you_mean
// ---------------------------------------------------------------------------

// visibleCorpus lazily builds the caller-visible id list used for did_you_mean
// suggestions: in-scope, configured, non-quarantined servers only. Built at
// most once per Evaluate call, and only when some id actually misses.
type visibleCorpus struct {
	ec     *EvalContext
	ids    []string
	built  bool
	buildE error
}

func (c *visibleCorpus) candidates() ([]string, error) {
	if c.built {
		return c.ids, c.buildE
	}
	c.built = true

	ec := c.ec
	if ec.Index == nil {
		c.buildE = fmt.Errorf("preflight: no index reader configured")
		return nil, c.buildE
	}
	servers, err := ec.Index.IndexedServerNames()
	if err != nil {
		c.buildE = fmt.Errorf("preflight: list indexed servers: %w", err)
		return nil, c.buildE
	}
	for _, server := range servers {
		if !ec.Scope.Allows(server) {
			continue
		}
		policy, perr := ec.Policy.ServerPolicy(server)
		if perr != nil {
			c.buildE = fmt.Errorf("preflight: read server policy for %q: %w", server, perr)
			return nil, c.buildE
		}
		// Never suggest names from a quarantined, unconfigured or disabled
		// server (FR-013): a suggestion must not confirm what the caller may
		// not see.
		if !policy.Found || policy.Quarantined || !policy.Enabled {
			continue
		}
		tools, terr := ec.Index.ToolsByServer(server)
		if terr != nil {
			c.buildE = fmt.Errorf("preflight: read index for server %q: %w", server, terr)
			return nil, c.buildE
		}
		for i := range tools {
			name := tools[i].Name
			if idx := strings.Index(name, ":"); idx >= 0 {
				name = name[idx+1:]
			}
			if name == "" {
				continue
			}
			c.ids = append(c.ids, server+":"+name)
		}
	}
	return c.ids, nil
}

// notFoundResult is the ONE constructor for a not_found verdict. Both an absent
// tool and an out-of-scope id at the agent-token tier go through it, which is
// what makes the two byte-indistinguishable (FR-013) by construction rather
// than by careful copy-editing. Suggestions are drawn from the caller-visible
// corpus only, so they can never cross the scope boundary.
func (c *visibleCorpus) notFoundResult(id string) (Result, error) {
	res := unavailable(id, ReasonNotFound, detailNotFound)
	candidates, err := c.candidates()
	if err != nil {
		return Result{}, err
	}
	if suggestions := Suggest(id, candidates); len(suggestions) > 0 {
		res.DidYouMean = suggestions
	}
	return res, nil
}
