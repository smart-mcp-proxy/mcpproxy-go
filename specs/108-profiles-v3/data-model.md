# Data Model: Profiles v3

All additions are optional/omitempty; every existing record, config file and token loads unchanged. Types below are the Go shapes; JSON names are the wire names used identically by REST, MCP, CLI `-o json`, `contracts.ts` and the Swift models.

## 1. `config.ProfileConfig` (config file, `internal/config/profiles.go`)

```go
type ProfileConfig struct {
    Name            string            `json:"name"`                        // slug (Spec 057 rules unchanged)
    Servers         []string          `json:"servers"`                     // unchanged
    Title           string            `json:"title,omitempty"`             // ≤ 80 chars, display only
    Description     string            `json:"description,omitempty"`       // ≤ 500 chars
    MaxTier         string            `json:"max_tier,omitempty"`          // "" | read | write | destructive
    Unannotated     string            `json:"unannotated,omitempty"`       // "" | deny | as_write | as_read
    Tools           *ProfileToolRules `json:"tools,omitempty"`
    CodeExecution   *bool             `json:"code_execution,omitempty"`    // nil = inherit global, except off under a read/write cap (FR-003a)
    ManagementTools *bool             `json:"management_tools,omitempty"`  // nil = legacy
    SwitchableTo    *[]string         `json:"switchable_to,omitempty"`     // nil = legacy/none (research D6); non-nil empty = explicit "none" (round-trips as [])
}

type ProfileToolRules struct {
    Allow    []string          `json:"allow,omitempty"`    // "server:tool" globs, '*' only
    Deny     []string          `json:"deny,omitempty"`
    Classify map[string]string `json:"classify,omitempty"` // exact "server:tool" -> read|write|destructive
}
```

`SwitchableTo` is a pointer because the config is saved with plain `json.Marshal` (`internal/config/config.go` `MarshalJSON`): with a `[]string` + `omitempty`, an explicitly empty `switchable_to: []` would be dropped on save and silently turn the profile legacy on the next load; `*[]string` pointing at an empty slice marshals as `[]` and survives (test T004). The same rule would apply to any future policy field whose empty value differs from unset.

`IsLegacy()` = none of the six **policy** fields named in spec Definitions is set (`MaxTier`, `Unannotated`, `Tools`, `CodeExecution`, `ManagementTools`, `SwitchableTo`). `Title` and `Description` are display-only and deliberately excluded, so adding a title to a legacy profile keeps it legacy (no `hidden_by_profile`/`profile` fields appear in `retrieve_tools`, SC-003). The same predicate drives `hidden_by_profile` presence (FR-011) and `ProfileView.is_legacy`. `EffectiveUnannotated()` = explicit value, else `deny` if `MaxTier ∈ {read, write}`, else `as_read` (legacy/destructive). `EffectiveCodeExecution()` = explicit value, else `false` if `MaxTier ∈ {read, write}`, else `true` (legacy/destructive: the global flag decides) — the same fail-closed default (FR-003a, zcode review: the global `enable_code_execution` defaults to `true`, so plain inheritance would let a read-capped profile that leaves the field unset run scripts); the global flag is ANDed at the gate, so a profile never turns code execution on past it (FR-006). Both helpers are the only readers of the raw fields: enforcement, `tools/list` visibility, view-as, the explainer and FR-008a (iii) all use the effective values.

Top-level `Config.AnonymousProfile string \`json:"anonymous_profile,omitempty"\``. Gated by FR-009a together with the policy fields: a non-empty value is fatal until `profile.PolicyEnforcementReady` flips in 108-d (the anonymous tier lands in 108-c, its non-admin management view in 108-d).

Validation (`ValidateProfiles`, extended — one function, FR-007):

| Rule | Severity | Message (exact prefix) |
|---|---|---|
| slug format / reserved / duplicate | fatal | unchanged Spec 057 texts |
| `max_tier` / `unannotated` / classify value not in enum | fatal | `profiles[%d]: invalid %s %q: must be one of …` |
| pattern not `server:tool` or contains chars other than `[A-Za-z0-9_.\-/:*]` | fatal | `profiles[%d]: invalid tool pattern %q` |
| `switchable_to` contains own name | fatal | `profiles[%d]: switchable_to cannot include the profile itself` |
| title > 80 / description > 500 | fatal | `profiles[%d]: %s too long` |
| rule/classify names server not in `servers` | warning | `profile %q rule %q names server %q outside the profile; ignored` |
| `switchable_to` names unknown profile | warning | `profile %q switchable_to references unknown profile %q; ignored` |
| `anonymous_profile` unknown | warning | `anonymous_profile %q does not exist; anonymous callers are denied all tools` |
| any v3 policy field set while `profile.PolicyEnforcementReady` is false (FR-009a, builds between 108-a and 108-d) | fatal | `profiles[%d]: %s is not supported by this build (Profiles v3 enforcement incomplete)` |
| non-empty `anonymous_profile` while `profile.PolicyEnforcementReady` is false (FR-009a) | fatal | `anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)` |

## 2. Compiled policy (per published config snapshot, `internal/profile/policy.go`)

```go
type Tier int // TierRead=1, TierWrite=2, TierDestructive=3; TierUnannotated=0

type CompiledPolicy struct {
    Name, Title  string
    Servers      map[string]struct{}
    Cap          Tier            // 0 = no cap (legacy)
    Unannotated  string          // effective value
    allow, deny  []globMatcher   // anchored, '*' only
    classify     map[string]Tier
    CodeExec     bool            // EffectiveCodeExecution() (FR-003a); the global gate is ANDed at the call
    Mgmt         *bool
    SwitchableTo map[string]struct{} // nil = legacy
    Fingerprint  [32]byte        // sha256 of canonical JSON of the policy fields
}

// Published with the (index, snapshot) pair; bumped when any tool's effective
// annotations change or a tool appears/disappears (FR-027). Cache stamps carry it.
type ToolTierGeneration uint64

// Index API extension (FR-011): the pre-limit predicate sees the hit's canonical identity.
// The index stores no annotations and gains none (no ToolDocument/schema change, no reindex).
type Hit struct{ Server, Tool string } // canonical registration identity (Spec 105)
type Admission int // Admit | RejectScope (server out of scope, never counted) | RejectPolicy (counted in hidden_by_profile)
// index.Manager.SearchToolsAdmitted(query, limit, admit func(Hit) Admission) (results, hiddenByPolicy int, err)
// The server builds `admit` per request from the one ProfileResolution; inside it the
// hit's effective annotations come from profile.EffectiveAnnotations(server, tool)
// (T013) = resolveExactToolIdentity, the identity seam every dispatch path already
// uses (Spec 105 FR-009) — an O(1) StateView lookup, never a live upstream call — so
// discovery and execution classify a tool from one source; not found → destructive.

type Reason string // "", server_not_in_profile, denied_by_rule, unannotated_hidden, above_tier_cap

func (p *CompiledPolicy) Decide(server, tool string, intrinsic Tier) (admitted bool, reason Reason, profileTier Tier)
func IntrinsicTier(a *config.ToolAnnotations, found bool) Tier // found=false → destructive
func (t Tier) String() string // "read" | "write" | "destructive" | "unannotated" — the contracts.Tier spelling
```

**One tier mapping across both specs** (codex round 4, research D30). The annotation → tier rule is Spec 109-a's `contracts.AnnotationTier(a) contracts.Tier` (`internal/contracts/tier.go`, a `string` type: `read|write|destructive|unannotated`, plus `unknown`, which only Spec 109's review composer returns for approval records captured before annotations were stored). `profile.Tier` stays an `int` because `Decide` compares it against the cap (`intrinsic > Cap`). `IntrinsicTier` never re-implements the rule; it is exactly:

```go
func IntrinsicTier(a *config.ToolAnnotations, found bool) Tier {
    if !found { return TierDestructive }                 // unresolved identity fails closed (FR-011)
    switch contracts.AnnotationTier(a) {
    case contracts.TierRead:        return TierRead
    case contracts.TierWrite:       return TierWrite
    case contracts.TierDestructive: return TierDestructive
    case contracts.TierUnannotated: return TierUnannotated
    default:                        return TierDestructive // "unknown" or any future value fails closed
    }
}
```

`Tier.String()` returns the `contracts.Tier` spelling, so every surface that prints a profile tier prints Spec 109's word. The joint test T005a pins `IntrinsicTier(a, true).String() == string(contracts.AnnotationTier(a))` for every annotation fixture (nil, `{}`, read, explicit write, destructive, destructive + read, and the enforcement-matrix tools), and that an out-of-range `contracts.Tier` maps to `TierDestructive`. This is why 108-a depends on 109-a (plan.md).

Compiled policies live inside the existing `profileIndex` (Spec 105 D17), built by the pre-publish observer, taken with their snapshot as one pair. No new locks.

## 3. `auth.AgentToken` (`config.db` bucket `agent_tokens`, extended)

| Field | JSON | Notes |
|---|---|---|
| `Kind` | `kind,omitempty` | `""`/`agent` = regular; `client` = client credential; any other value is invalid (fail closed at authentication, FR-021) |
| `ClientID` | `client_id,omitempty` | set iff `Kind=client`; unique among active client credentials; matches `^[a-z0-9][a-z0-9_-]{0,55}$` and, for a custom client, is not a supported connect-registry id (FR-021, `400 {error, field:"id"}`) |
| `ProfilePin` | `profile_pin,omitempty` | existing; empty on a client credential = "All servers" |
| `ProfileMode` | `profile_mode,omitempty` | `kind=client` only: `locked` \| `switchable` (required, never empty). For regular tokens it is empty and **not applicable** — a regular token is pinned iff `ProfilePin` is set, exactly as today; a non-empty value on a regular token is invalid |
| `PendingHash`, `PendingPrefix` | `pending_hash,omitempty`, `pending_prefix,omitempty` | `kind=client` only, during a staged rotation (FR-021a); indexed by bucket `agent_token_pending` (pending hash → record hash) so the pending secret authenticates as the same record |
| `RotationStartedAt` | `rotation_started_at,omitempty` | set with `PendingHash`; drives the 24-hour custom-client overlap and `client_rotation_pending` |
| `ConnectedAt` | `connected_at,omitempty` | when connect/add minted it |
| `ConfigPath` | `-` (never stored) | resolved live from the connect registry |

**Secret format**: regular tokens `mcp_agt_` + 64 hex; client credentials `mcp_cli_` + 64 hex (same HMAC-SHA256 hashing). A pre-108 binary only treats `mcp_agt_` as an agent token, so a client credential can never authenticate there as a wildcard agent token (rollback safety, research D27).

Invariants (all scoped to `Kind=client`; checked at mint **and on every authentication**, violation → `401 malformed credential record`, FR-021): `Kind=client` ⇔ secret prefix `mcp_cli_`; `Kind=client` ⇒ `ClientID` matches the pattern, `Name="client-"+ClientID`, `AllowedServers=["*"]`, `Permissions=[read,write,destructive]`, `ProfileMode ∈ {locked, switchable}`, `ProfileMode=locked ⇒ ProfilePin != ""`. `Kind ∈ {"", agent}` ⇒ `ClientID`, `ProfileMode`, `PendingHash` empty (legacy pinned and unpinned tokens are valid exactly as today). Minting a client credential for an id whose `kind=client` record is **revoked or expired** replaces it in the same bbolt transaction (names stay reserved by soft-revoked records); over an **active** record it is a staged rotation (FR-021a); it never touches a `kind=agent` record, and a grandfathered `kind=agent` token already named `client-<id>` makes the mint fail with `409 {error, conflicting_token}` (FR-021). Profile rename/`reassign_to` rewrites `ProfilePin` on every record (both kinds) naming the old slug in one bbolt transaction (D19).

`auth.AuthContext` gains `TokenKind`, `ClientID`, `ProfileMode` copied at authentication. A **confined anonymous** caller (`Anonymous=true` and `anonymous_profile` set, i.e. `ProfileResolution.Base` comes from `anonymous_profile`) keeps its back-compat `AuthTypeAdmin` context for every non-management path, but the management-tool gates evaluate `AuthorizeServerOp` against a non-admin view of it (`auth.ScopedView(ac, resolution)`), giving it the agent-token op set (`list`/`tail_log`) exactly like a client credential (FR-016). The view is one helper, `auth.ScopedView(ac, resolution) *AuthContext` (non-admin copy for a confined anonymous caller, `ac` itself otherwise), and **every** `IsAdmin()` branch that shapes a scoped caller's view in a handler that caller reaches reads it — not only `AuthorizeServerOp`: the `upstream_servers` `list` filter, `handleTailLog`'s whole-file vs attributed reader and its `last_error` container redaction, and `handleCallToolVariant`'s scoped refusal shape (FR-016; a grep test in T052a fails on a direct `IsAdmin()` call in those handlers). `AnonymousContext()` itself is unchanged, so unconfined anonymous callers keep today's behaviour.

## 4. Resolution result (per request, `internal/server/profile_resolver.go`)

```go
type ProfileResolution struct {
    Name   string              // "" = none
    Source string              // pin | binding | url | session | anonymous | none
    Scope  *profile.ProfileScope
    Policy *profile.CompiledPolicy // nil for none / legacy-with-no-policy still non-nil but IsLegacy
    Base   string              // bound or anonymous base profile used for switchable_to checks
}
```

One call per request returns it with the `(index, snapshot)` pair (Spec 105 pair discipline). When the base for sources `pin`, `binding` or `anonymous` names a profile missing from the snapshot, `Name` keeps the missing name, `Scope` is deny-all (`profile.NewProfileScope(name, nil)`), `Policy` is nil and no URL/session selection is admitted — never a fall-through (FR-020). A dangling resolution counts as non-legacy for FR-011 (`retrieve_tools` carries `hidden_by_profile: 0`; `profile` is omitted because the source is a base); `IsLegacy()` is never consulted for it because there is no `ProfileConfig`. The FR-008a **binding guard** resolves the same way for an anonymous caller: while `BindingGuardActive(snapshot, tokens)` is true (some active client binding is bypassable), source `anonymous`, `Scope` deny-all, `Policy` nil, no URL/session selection admitted. `BindingGuardActive` and the per-binding `BindingBypassable(binding, cfg, pair)` are the one shared condition function (§7); the guard is re-evaluated whenever a new (index, snapshot) pair is published, because condition (ii) depends on the tool set.

## 5. `storage.ActivityRecord` (extended)

| Field | JSON | Filled from |
|---|---|---|
| `Profile` | `profile,omitempty` | `ProfileResolution.Name` at call time |
| `ProfileSource` | `profile_source,omitempty` | `ProfileResolution.Source` |
| `ClientID` | `client_id,omitempty` | `AuthContext.ClientID` |
| `ClientName` | `client_name,omitempty` | session `clientInfo.name` (was `metadata.client_name`) |
| `TokenName` | `token_name,omitempty` | `AuthContext.AgentName` for agent/client tokens |
| `BlockReason` | `block_reason,omitempty` | `profile_tier`, `profile_rule`, `profile_unannotated`, `profile_code_execution`, `profile_management` (new); existing block causes keep their current metadata and are not renamed |

New `ActivityType`: `profile_change`, metadata: `{actor_kind, actor_name, surface, change, profile, previous_profile, client_id, token_name, diff}` with `change ∈ create|update|delete|rename|classify|assign|lock|unlock|forget|rotate|anonymous`. The clients service writes `assign`/`lock`/`unlock` (binding changes; a mint via connect or `client add` is `assign` with empty `previous_profile`), `rotate` (diff carries old and new `token_name`, never a secret) and `forget` (revoked `token_name`, `disconnected`); the profiles service writes the rest (FR-030).

`ActivityFilter` gains `Profile`, `ClientID`, `ClientName` (advisory), `TokenName` with the `-` = empty sentinel; `Matches` checks struct fields and the legacy metadata keys (`profile`, `agent_name`).

## 6. `storage.SessionRecord` / `server.SessionInfo` (extended)

`TokenName`, `ClientID` (set at initialize from the auth context), `Profile`, `ProfileSource` (latest effective, updated on each call). `SessionStore` adds `SessionsForToken(name) []string` over its existing map. The session's **base** (pin, bound profile or `anonymous_profile`) is deliberately **not** stored: it changes on reassignment and rename, so every consumer (FR-027 notification fan-out, T072a) derives it at use time from `TokenName` → the token's current `profile_pin` in the token store, or, for a session with no `TokenName` (anonymous), from the current snapshot's `anonymous_profile`.

## 7. Derived views (no storage)

**ClientView** (`GET /clients` row) **extends Spec 109's client presence row additively** — Spec 109 data-model §6 owns and defines `id, display_name, kind, icon, state, installed, connected, config_path, display_path, last_seen, active_sessions, calls_24h, reload_hint` (and 109-h owns the route and the CLI `client list|show` presence columns, incl. `CONFIG PATH`). This spec adds only: `kind` value `custom` (a user-added client from `POST /clients`, which has a credential; Spec 109's `supported | other` are unchanged — `other` stays an unrecognised `clientInfo.name` with no credential), `credential_state (client|admin_key|none|revoked|expired)`, `token_name, profile, profile_title, profile_mode, profile_source, profile_missing, expires_at, rotation_pending, blocked_24h`. Deep links are Spec 109's link map (not a row field). Nothing Spec 109 defines is renamed, narrowed or dropped. `profile_source` is the source at which the client's credential resolves when its request names no URL profile and holds no `set_profile` selection — `pin` for `profile_mode=locked`, `binding` for `switchable` (FR-020) — and empty when `credential_state` ≠ `client`; it backs the CLI `SOURCE` column and the chip's source label (spec Terminology). Per-call sources (`url`, `session`) are shown on session and activity rows, not here. Response-level `warnings[]` with codes `anonymous_denied_by_binding_guard` (FR-008a runtime guard active: some client with a non-empty pin, locked or switchable, while `require_mcp_auth` is off and `anonymous_profile` is unset or wider — US5-4; lists the bindings and the fixes), `client_holds_admin_key` (action: the FR-025 bulk upgrade, then admin-key rotation), `client_credential_expiring`, `client_rotation_pending` (FR-021a), `profile_missing`, `client_token_name_conflict` (a `kind=agent` token holds `client-<id>`, FR-021). The **error** code `binding_bypassable_without_auth` (`409`) is returned by every API operation that would create the guard condition (FR-008a). The condition is one function (`BindingBypassable(binding, cfg, pair)`) shared by the API refusal, the runtime guard, `GET /clients`, `mcpproxy doctor` (`profiles.binding_bypass`), the Web UI and macOS banners, so no two surfaces can disagree. Definition (FR-008a): with `require_mcp_auth` off, let A = `{anonymous_profile}` ∪ its `switchable_to` (empty `anonymous_profile` = unconfined, always bypassable) and B = `{P}` for `locked`, `{P}` ∪ P's `switchable_to` for `switchable`; bypassable iff some R ∈ A (i) has a server outside ⋃B's servers, an effective cap above every B member's, or a more permissive effective `unannotated` (`deny` < `as_write` < `as_read`) than every B member; or (ii) admits (`CompiledPolicy.Decide`) some tool of the pair, under its current effective annotations, that no B member admits; or (iii) has effective `code_execution`/`management_tools` on while every B member has it off. Dangling members of A are deny-all (never widen); a dangling P makes the binding deny-all itself, so it is never bypassable. These warning codes are the names Spec 109-l maps to Needs-attention kinds (Spec 109 uses the same names). Binding operations (`PUT /clients/{id}/binding`, lock/unlock, MCP `assign`) on a row whose `credential_state` is not `client` return error code `no_client_credential` (`409`, FR-026).

**`connect.ClientStatus`** (`GET /connect`, `GET /connect/{client}`) gains the same `credential_state` field (108-c). Every response that carries it is **administrator-only** (FR-025a): the three connect reads move behind `requireAdminRead`, so the field — which says which clients still hold the admin API key — is never readable by a scoped agent token or other non-admin caller, matching the administrator-only `GET /clients`. Spec 109-b adds `display_path` and `reload_hint` to the same struct in a parallel PR; the later of the two merges keeps all three fields (tasks.md Dependencies).

**EffectiveTool** (`effective-tools`, `tools?client=`): `server, tool, intrinsic_tier (read|write|destructive|unannotated), profile_tier, access{visible, callable, reason}, classification_stale`. `access` is computed by walking the AccessExplanation chain below in its canonical order (`credential → profile → server_in_scope → tool_rule → tier_cap → token_permission → global_gate → server_state → tool_approval`, FR-032), so `callable` equals a real call's outcome and `reason` names the first failing step. `reason` enum (empty when callable): the FR-010 decision reasons reported by the step that carries them — `server_not_in_profile` (step `server_in_scope`, server outside the profile's `servers`; evaluated before the token half of that step), `denied_by_rule` (step `tool_rule`), `unannotated_hidden` and `above_tier_cap` (step `tier_cap`) — and otherwise the step name itself: `credential`, `profile` (dangling base), `server_in_scope` (server in the profile but outside the subject credential's token servers), `token_permission`, `global_gate`, `server_state`, `tool_approval`. The caller's own authorization scope is applied before the rows are built and never appears as a `reason` (FR-032). For a **non-administrator** caller (`profile=` view-as, `effective-tools`) the response contains only rows with `access.visible=true` plus `counts{visible, hidden}` — excluded rows, their tiers and reasons are administrator-only (FR-032). `classification_stale` is the FR-005 stale-classification report surfaced by every effective-tools consumer.

**AccessExplanation**: `subject{client|token|profile|anonymous}, tool, steps[{step, status (pass|fail|skip), detail}], verdict (allowed|blocked|hidden), first_failure, fixes[{step, action, target, label}]`. `fixes[]` is top-level (FR-035) because one failure can have several fixes, ordered by preference (e.g. for `tier_cap`: `allow_in_profile` → the profile editor focused on the tool, then `move_client` → the client's binding); it is empty when `verdict=allowed`. `action` enum: `allow_in_profile`, `classify_in_profile`, `add_server_to_profile`, `move_client`, `edit_token`, `enable_server`, `approve_tool`, `change_setting`, `reconnect_client`. Steps in enforcement order (identical to the refusal precedence in [contracts/refusals.md](contracts/refusals.md)): `credential, profile, server_in_scope, tool_rule, tier_cap, token_permission, global_gate, server_state, tool_approval`. `server_in_scope` covers profile servers ∩ token servers; `tier_cap` covers the unannotated policy.

**ProfileView** (`GET /profiles` row): config fields + `effective_servers, tool_counts{read,write,destructive,unannotated_hidden}, used_by{clients[], tokens[], anonymous_profile: bool}, calls_24h, blocked_24h, is_legacy`. `used_by` is present for administrator callers only and **omitted** (not emptied) for every non-admin caller — it discloses other credentials' bindings, the FR-032 rule (FR-034, zcode review).

## 8. Events

`profiles.changed {name, change}`, `client.binding_changed {client_id, token_name, profile, previous_profile, mode}` added to `internal/runtime/events.go`; `active_profile.changed` retained during deprecation.

## 9. State transitions — client credential

```
(none) --connect/add--> active(locked|switchable, pin)
active --set-profile/lock/unlock--> active (same token, updated pin/mode; list_changed to its sessions)
active --rotate/reconnect--> rotating (pending secret added; both secrets valid) --config written / reconciler sees new--> active' (finalize: pending promoted, old dropped)
rotating --reconciler sees old secret in config--> active (rollback: pending dropped)
rotating --custom client: finalize call or 24 h overlap--> active'
active --forget/disconnect--> revoked (config entry removed only on disconnect)
active --365d--> expired (401 on MCP; surfaces show "reconnect")
```
