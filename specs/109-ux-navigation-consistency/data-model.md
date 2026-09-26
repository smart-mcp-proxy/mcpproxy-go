# Data model: Spec 109

Only additive fields. No config-file changes, no new bbolt bucket, no migration. Legacy records decode unchanged (`omitempty`).

## 1. `contracts.HealthStatus` (extended) — `internal/contracts/types.go:1257`

| Field | Type | New | Notes |
|---|---|---|---|
| `level` | string | | unchanged (`healthy|degraded|unhealthy`); badge severity only |
| `admin_state` | string | | unchanged |
| `summary`, `detail` | string | | unchanged |
| `action` | string | | `== actions[0]` or `""`; unchanged except quarantined + sign-in → `login` (was `approve`, FR-010) |
| `status` | string | ✓ | `ready|connecting|sign_in_required|needs_review|needs_secret|needs_config|error|disabled` |
| `usable` | bool | ✓ | `status == ready` |
| `actions` | []string | ✓ | ordered per FR-012, `omitempty` |

Constants: `internal/health/constants.go` `Status*`, exported by `cmd/generate-types` to `contracts.ts` (`HealthStatusValue` union + `HEALTH_STATUS_LABELS` map, the label table from contracts/health-vocabulary.md).

## 2. `storage.ToolApprovalRecord` (extended) — `internal/storage/models.go:261`

| Field | Type | Notes |
|---|---|---|
| `current_annotations` | `*config.ToolAnnotations` (JSON) | written with `current_description` in `checkToolApprovals`; `omitempty`. A captured tool with no hints is stored as a non-nil empty object `{}`, so nil always means "not captured" (→ `unknown`) and `{}` means "captured, unannotated" |
| `previous_annotations` | `*config.ToolAnnotations` (JSON) | moved from `current_annotations` when a change is recorded; `omitempty` |

The approval hash is unchanged (annotations stay excluded, `tool_quarantine.go:26`). Records without these fields → review `tier: unknown`.

## 3. `contracts.AnnotationTier` (new pure function) — `internal/contracts/tier.go`

```go
type Tier string // "read" | "write" | "destructive" | "unannotated"  (+ "unknown", used only by the review composer for records captured before this spec)
const (TierRead Tier = "read"; TierWrite Tier = "write"; TierDestructive Tier = "destructive"; TierUnannotated Tier = "unannotated"; TierUnknown Tier = "unknown")
func AnnotationTier(a *config.ToolAnnotations) Tier // never returns TierUnknown
```

`destructiveHint==true` → destructive; `readOnlyHint==false` (explicit) → write; `readOnlyHint==true` → read; otherwise unannotated. It does not replace `DeriveCallWith` (Spec 018 call-variant hint, which keeps unannotated → read for call routing). Spec 108 `IntrinsicTier` wraps it through an exhaustive `contracts.Tier` → `profile.Tier` (int) adapter in which any value other than the four it returns fails closed to destructive (Spec 108 data-model §2, its joint test T005a); the exported constants above are what that adapter switches on, so Spec 108-a merges after 109-a (research D28).

## 4. Attention (derived, not persisted) — `internal/runtime/attention.go`

```go
type AttentionItem struct {
    ID      string            `json:"id"`      // kind:type:subject
    Kind    string            `json:"kind"`
    Rank    int               `json:"rank"`
    Subject AttentionSubject  `json:"subject"` // {type: server|tool|client, id, name}
    Summary string            `json:"summary"`
    Detail  string            `json:"detail,omitempty"`
    Fix     AttentionFix      `json:"fix"`     // {verb, label, target}
    Since   time.Time         `json:"since"`
}
type AttentionInput struct {
    Servers        []AttentionServer     // minimal subset, defined here (109-d)
    Clients        []AttentionClient     // minimal subset, defined here (109-d)
    Now            time.Time
}
// AttentionServer is the only server data Compute needs. 109-d defines it and
// the builder that fills it (T060); T053 tests Compute over it.
type AttentionServer struct {
    Name        string                  // config name = subject.id and subject.name
    Enabled     bool                    // false → never an item
    Quarantined bool                    // admin_state == quarantined → server_review
    Health      contracts.HealthStatus  // status, actions, summary, detail (109-c fields)
    Pending     int                     // tools with approval_status pending (Compute ignores Pending/Changed on a quarantined server: server_review covers it)
    Changed     int                     // tools with approval_status changed
    StateSince  time.Time               // when health.status last changed (subscriber memory)
    Detail      string                  // item detail line built from the row: transport + URL host, e.g. "OAuth · api.githubcopilot.com"; never a secret, URL path/query or header
}
// AttentionClient is the only client data Compute needs. 109-d defines it so
// attention.go compiles and T053 tests client_never_seen without 109-h.
type AttentionClient struct {
    ID, DisplayName string
    ConnectedAt     *time.Time // last successful connect write
    LastSeen        *time.Time // last MCP session mapped to this client
}
func Compute(in AttentionInput) []AttentionItem // pure; sorted by rank, subject.name
```

Server input wiring (109-d, T060): the subscriber builds `[]AttentionServer` from the same `[]contracts.Server` rows the `servers.changed` payload is built from (`internal/runtime/event_bus.go`): `Name`, `Enabled`, `Quarantined`, `Health` from the row; `Pending`/`Changed` from `Quarantine.PendingCount/ChangedCount`, which `enrichServersWithQuarantineStats` already fills from `ListToolApprovals` (one bbolt read per server per debounced recompute, never per request); `StateSince` from the subscriber's in-memory `map[name]{status, since}`, stamped when `Health.Status` differs from the stored value. No new store and no activity-log read.

Client input wiring: 109-d's subscriber passes `Clients: nil` (so live instances show no `client_never_seen` item before 109-h). 109-h adds `ClientPresence.AttentionClient()` (§6), which the subscriber calls on connect and session events. `ClientPresence` is a superset, so no field is renamed. Compute's rule, the kind, its rank and its fix are complete in 109-d. The fix target `/clients?focus=<id>` exists from 109-h, the same PR that makes the item appear.

The `sign_in_required`, `missing_secret` and `config_error` conditions key on `health.status`, never on `actions` membership (a `ready` server can carry `actions: ["login"]` as a proactive nudge; contracts/rest-api.md#attention).

SSE: the runtime `attention.changed` event carries `items: [{id, subject_type, subject_id}]`; `internal/httpapi` renders `{count, ids}` per subscriber (FR-006).

`stateSince` (per server, the time `health.status` last changed) is tracked by the runtime subscriber in memory (it resets on restart, which only delays an item by ≤ 60 s). **Recompute triggers**: the debounced events above **and** a threshold timer — after each recompute the subscriber computes `next = min(StateSince+60s over connecting/error servers, ConnectedAt+5min over connected-never-seen clients)` and arms one timer for `min(next, now+30s)`, so a time-based item appears at its threshold even when no event fires (FR-002). Kinds, ranks and fixes are enums in `internal/runtime/attention_contract.go`, exported to `contracts.ts`.

## 5. Review payloads (derived) — `internal/runtime/review.go`

`ReviewQueue{Count, Servers []ReviewQueueRow}` and `ServerReview{Server ReviewServer, Tools []ReviewTool}` exactly as in contracts/rest-api.md#review. Composed from `ListToolApprovals(server)`, server config (command/url/transport/trust mode — the summary is built from a `contracts.Server` copy passed through `oauth.RedactServerSecretFields` before any field is read, so no raw secret enters the payload, FR-021), and the latest scan summary and per-tool findings (`security/scanner` service). Diff: unified diff computed server-side in `internal/runtime/review_diff.go` (same sections as today's `frontend/src/utils/toolDiff.ts` `computeToolDiffSections`: description, input schema, output schema, plus annotations), so macOS and the CLI get the same text. The Web UI renders the server diff and drops its local computation. The server summary also carries the existing `source_registry_id` / `source_registry_provenance` (MCP-866 origin, `contracts.Server` fields, `omitempty`), so a reviewer sees which catalog the server came from; both are listed in contracts/rest-api.md#review and FR-021.

## 6. Client presence (derived) — `internal/runtime/clients_presence.go`

```go
type ClientPresence struct {
    ID, DisplayName, Kind, Icon, State string   // JSON id, display_name, kind (supported|other; 108 adds custom), icon, state: connected_seen|connected_never_seen|installed|not_installed|other
    Installed, Connected  bool
    ConnectionUnverified bool      // JSON connection_unverified: installed, no connect write and no session seen; the list cannot know without a content read (FR-030)
    ConfigPath, DisplayPath string
    LastSeen *time.Time
    ActiveSessions, Calls24h int
    ReloadHint string
    ConnectedAt *time.Time         // last successful connect write (for client_never_seen)
    Sessions []SessionRef          // only on the detail call
}
func (p ClientPresence) AttentionClient() AttentionClient // 109-h; feeds §4 (same package)
```

Sources: `connect.GetAllStatus()` (installed/config path only — it performs no content read and always reports `Connected=false`, Spec 075 FR-001), `SessionStore` (active sessions by `client_name`), the telemetry activation store's `mcp_clients_seen_ever` (unchanged `[]string` on the wire and in the bucket; used only as an "ever seen" hint) plus a new `client_last_seen` map (client-info name → timestamp, capped at 32 entries) stored in the onboarding record beside `client_connected_at` and written on `initialize` independent of telemetry (§7), `calls_24h` from a **per-client rolling counter added to the in-memory usage aggregate** (109-h; the aggregate at `638fa805a` keys `ToolUsage` by server:tool and keeps fleet-wide hourly buckets only, with no client or session dimension, so it cannot answer this without the extension): `UsageAggregate` gains `ClientCalls map[string]*[24]HourCount` keyed by client id — resolved when a record is applied from the record's `client_id` once Spec 108-e stamps it, otherwise from `rec.SessionID` → the session's `client_name` (live `SessionStore`, or the persisted session record during a replay) → the `ClientInfoNames` alias map, an unrecognised name keyed by its sanitised form; records with no resolvable client are not counted per client. Hourly slots older than 24 h are zeroed on apply and ignored on read; keys are capped by the same rule as `client_last_seen` (supported ids never evicted, at most 32 others, LRU); `usageAdmissionVersion` is bumped so a persisted snapshot without the field is rebuilt once from the activity scan the aggregate already performs on a cold start, never per request. And the connect write time (new field in the onboarding state record, set by the connect service on success, cleared by its disconnect, which also stamps `client_disconnected_at`, §7). **`Connected`** on the list = a recorded connect write **or** a `client_last_seen` entry for the client **newer than its `client_disconnected_at`** (a session seen before MCPProxy's last disconnect of that client only feeds `last_seen`, codex round 4); `GET /clients/{id}` (explicit, per client) additionally calls `connect.GetStatus()` (the Spec 075 FR-002 content read) and ORs its `Connected`. `State`: `connected_seen` (connected and seen), `connected_never_seen` (connect write, never seen since), `installed` (installed, no evidence — `ConnectionUnverified=true` on the list), `not_installed`, `other`.

`connect.ClientDef` gains `ClientInfoNames []string` (explicit aliases), `ReloadHint string`, `ConfigPathDisplay` computed at request time (`~` substitution for `os.UserHomeDir()`). `connect.ClientStatus` gains `DisplayPath`/`ReloadHint` here (109-b) and `CredentialState` from Spec 108-c; the later of the two parallel PRs keeps all three. From 108-c every REST response carrying `ClientStatus` is administrator-only (Spec 108 FR-025a); the in-process `GetAllStatus()` call above is unaffected.

**Owned here** (ownership rule): Spec 108 extends the JSON row additively with `credential_state`, `token_name`, `profile`, `profile_title`, `profile_mode`, `profile_source`, `profile_missing`, `expires_at`, `rotation_pending`, `blocked_24h`, the `kind` value `custom`, and response-level `warnings[]` (its data-model §7); it renames and drops nothing.

## 7. Onboarding state (extended)

Two structs: the persisted record `storage.OnboardingState` (`internal/storage/models.go:96`, JSON in the onboarding bucket) and the response DTO `OnboardingStateResponse` (`internal/httpapi/onboarding.go:17`, which embeds the record as `state`). Computed fields live on the DTO; persisted maps live on the record. Every writer of the record (`handleMarkOnboardingState`, the connect success path, the `initialize` hook) goes through `storage.UpdateOnboardingState(func(*OnboardingState) error)`, one bbolt update transaction per mutation (109-b, T035), so no writer drops another's field. `GET /onboarding/state` is administrator-only (`requireAdminRead`), so the maps appearing additively under `state` expose nothing to scoped callers.

| Field | New | Notes |
|---|---|---|
| `has_configured_server` | | DTO; kept for compatibility |
| `has_usable_server` | ✓ | DTO. ≥ 1 enabled, non-quarantined, usable server with ≥ 1 approved tool. 109-b and 109-c are parallel PRs with no edge: whichever merges second applies the final `health.usable` definition (if 109-b merges first it uses "connected" as the interim test; if 109-c merges first, 109-b uses `health.usable` directly; tasks.md T051) |
| `usable_servers` | ✓ | DTO; names, for Verify prompts |
| persisted `client_connected_at` map | ✓ | `storage.OnboardingState`; client id → last connect write time; keys come only from the fixed `connect` client registry (bounded by it); written by the connect success path (109-b, T035) |
| persisted `client_disconnected_at` map | ✓ | `storage.OnboardingState`; client id → time of the last disconnect MCPProxy performed for it; keys come only from the fixed `connect` client registry (bounded by it); written by the disconnect path in the same `UpdateOnboardingState` transaction that clears `client_connected_at` (109-h, T129), and removed again by the next successful connect write. Presence ignores `client_last_seen` evidence older than it (§6) |
| persisted `client_last_seen` map | ✓ | `clientInfo.name` → last session start; written from the MCP `initialize` handler (`internal/server/mcp.go`, beside the existing `SetSession` and `RecordMCPClientForActivation` calls) through a runtime method that works with telemetry disabled (the seen-ever list lives in the telemetry activation bucket, `internal/telemetry/activation.go`, and is not a reliable source when `MCPPROXY_TELEMETRY=false`); the key is the sanitised `clientInfo.name` (untrusted wire input, research D19). **Bounded**: at most 32 entries (twice the telemetry list's `MaxMCPClientsSeen = 16`, `internal/telemetry/activation.go:34`, because this map also serves presence); names that map to a supported client's `ClientInfoNames` are never evicted, and on overflow the least recently seen other entry is evicted (LRU by timestamp), so a caller cycling random `clientInfo.name` values cannot grow the record. **Write-throttled**: an entry is rewritten only when it is new or its stored timestamp is ≥ 60 s old, so reconnect storms do not rewrite the record per `initialize`. `mcp_clients_seen_ever` stays an unchanged `[]string` (109-h, T129; test T124) |

## 8. Import preview (extended) — `internal/configimport`

`DetectionResult.Format` gains `url` and `command`. `ProposedServer` gains `Summary string`, `Tags []string`, and per env/header `SecretLike`, `EmptyOrPlaceholder`. Secret-like name regex in research D13.

## 9. Catalog (derived) — `internal/registries/catalog.go`

```go
// Internal ranked hit (Go only, never marshalled): keeps the registry entry untouched.
type CatalogHit struct {
    Entry      ServerEntry  // existing Spec 070 type; its JSON (url, installCmd, registry, required_inputs[].secret) is NOT changed
    Source, Title, Publisher string
    Verified, Official       bool
    Popularity *Popularity   // {stars?, installs?}
}

// REST response DTO of GET /catalog/search — a distinct type, built by toCatalogResult(hit, added).
type CatalogResult struct {
    Source         string          `json:"source"`
    ID             string          `json:"id"`
    Title          string          `json:"title"`
    Publisher      string          `json:"publisher"`
    Verified       bool            `json:"verified"`
    Official       bool            `json:"official"`
    Popularity     *Popularity     `json:"popularity,omitempty"`
    Description    string          `json:"description"`
    Transport      string          `json:"transport"`              // "http" when Entry.URL/ConnectURL is set, else "stdio"
    Install        CatalogInstall  `json:"install"`                // {url} or {command, args} (Entry.InstallCmd split with the shared shell-words parser)
    RequiredInputs []CatalogInput  `json:"required_inputs,omitempty"` // {name, description?, secret_like} ← RequiredInput{Name, Description, Secret}; secret_like = Secret OR the research D13 secret-like-name rule (the one function the import preview's SecretLike uses), so a registry that omits or falsifies isSecret on GITHUB_TOKEN still defaults to Secret (FR-065)
    SourceCodeURL  string          `json:"source_code_url,omitempty"`
    Added          bool            `json:"added"`
}
type SearchOptions struct{ SourceTimeout time.Duration } // default 5 s; only tests set another value (T109a)
func SearchAll(ctx, q, tag string, limit int, opts SearchOptions) (results []CatalogHit, sections *CatalogSections, unavailable []SourceError)
func Rank(a, b CatalogHit, q string) bool // pure, deterministic
func toCatalogResult(h CatalogHit, added bool) CatalogResult // REST only; golden-tested against the contracts/rest-api.md#catalog example
```

`Added` is computed per caller: only configured servers the caller can enumerate (`visibleServers(ctx, …)`, FR-007) take part in the join, so a scoped caller never learns from `added` that an out-of-scope server is configured. `Added` is true when such a configured server has `source_registry_id == Source` (existing field, `config.go:756`) **and** the same install URL or command (the config does not record the registry's server id, so the install target is the join key). Manually added servers match on install URL or command alone.

## 10. Token metrics (extended) — `contracts.ServerTokenMetrics`

`estimated bool` (new). See contracts/rest-api.md#token-metrics.

## 11. Frontend state

- `useScopeQuery` registry (contracts/url-filter-contract.md), incl. `profile`/`client`/`token` with `requires: "scope_filters"`; the availability list is read once from `GET /api/v1/status` `features` and refreshed on reconnect.
- `stores/attention.ts`: `items`, `count`, refreshed by SSE `attention.changed`.
- `stores/clients.ts`: presence rows (Spec 108 extends it with binding actions).
- `components/ClientConnectList.vue`, `components/ImportServers.vue`, `components/ReviewScreen.vue`, `components/CommandPalette.vue`, `components/AttentionList.vue`, `components/StatusPill.vue`, `components/AddMenu.vue`, `components/ServerCard.vue` (rewritten layout).

## 12. macOS state

- `AppState.attention: [AttentionItem]`, `attentionCount` (from `GET /attention` + SSE). This replaces the `serversNeedingAttention` computation.
- `AppState.scopeFilter: ScopeFilter?`, which replaces `pendingActivitySessionFilter`; it has `profile`, `client` and `token` fields from 109-k, hidden (not sent, no control) until `features.scope_filters` lists them.
- Models: `HealthStatus.status/usable/actions`, `ReviewQueue`, `ServerReview`, `ClientPresence`, `CatalogResult`, `AttentionItem`, decoded from the shared golden fixtures (`internal/*/testdata/*109*.json`).
