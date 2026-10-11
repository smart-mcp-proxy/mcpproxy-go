# Data Model: Config Commit Consistency

**Spec**: [spec.md](spec.md) | **Research**: [research.md](research.md)

All types are internal unless they are marked **wire**. The only persisted
changes are additive JSON fields. Nothing needs migrating: a record without the
new field reads as generation 0, and the first commit that touches it stamps
it.

## 1. Commit (PR1), `internal/runtime/commit.go`

```go
// Commit runs fn against a working copy of the live config while holding
// configCommitMu, then persists and publishes the result (plan.md §Commit pipeline).
func (r *Runtime) Commit(reason string, fn func(tx *CommitTx) error) (*CommitResult, error)

type CommitTx struct {
    base     *configsvc.Snapshot          // the live snapshot read under the lock
    next     *config.Config               // deep copy, mutated by the ops below
    removed  map[string]*config.ServerConfig
    created  map[string]bool
    expect   map[string]configsvc.ServerGen // Expect* preconditions
    reason   string
    // unexported bookkeeping: touched names, quarantine transitions, explicit bits
}

func (tx *CommitTx) Server(name string) (*config.ServerConfig, bool)     // deep copy of the working entry
func (tx *CommitTx) Generation(name string) (configsvc.ServerGen, bool)  // base generation
func (tx *CommitTx) CreateServer(sc *config.ServerConfig) error          // *ServerExistsError
func (tx *CommitTx) UpdateServer(name string, fn func(cur *config.ServerConfig) error) error // ErrServerNotFound
func (tx *CommitTx) SetEnabled(name string, enabled bool) error
func (tx *CommitTx) SetQuarantined(name string, q bool) error            // stamps the explicit bit (#937)
func (tx *CommitTx) RemoveServer(name string) error                      // ErrServerNotFound
func (tx *CommitTx) ReplaceConfig(cfg *config.Config)                    // authoritative only: caller-submitted or file read under the lock (D14)
func (tx *CommitTx) ReconcileLive()                                      // projection repair of the base; no argument
func (tx *CommitTx) ExpectGeneration(name string, gen int64) error       // ErrServerReplaced at commit time
func (tx *CommitTx) ExpectIncarnation(name string, inc int64) error      // ErrServerReplaced at commit time
func (tx *CommitTx) Bootstrap()   // boot only: seed generations from matching stored rows; schedule all enabled servers (D13)
func (tx *CommitTx) ForceDisk()   // SaveConfiguration: always write the file (D13)
func (tx *CommitTx) MutateGlobal(fn func(cfg *config.Config) error) error // listen, registries, …
// UnderApprovalLock registers fn to run in pipeline step 4 while the commit
// holds name's approval lock (Spec 117 review-bound server approval). fn may
// only write fail-safe state (research §6). An error aborts the commit.
func (tx *CommitTx) UnderApprovalLock(name string, fn func() error) error

type CommitResult struct {
    Generation int64
    Snapshot   *configsvc.Snapshot
    Changes    map[string]ServerChange
    Purged     map[string]storage.PurgeReport // PR2
}

type ServerChange struct {
    Kind      ChangeKind // Created | Updated | Removed
    OldGen    int64      // 0 when Created
    NewGen    int64      // 0 when Removed
    Reconnect bool       // connection fields, Enabled or Quarantined changed (config.ConnectionEquivalent + flags)
    QuarantineRaised bool
}
```

Rules:
- `fn` runs with `configCommitMu` held. It MUST NOT call `Commit`,
  `SaveConfiguration`, `ApplyConfig`, `QuarantineServer`, `EnableServer` or
  take an approval lock. A test hook asserts re-entry (`commitDepth`).
- An error from `fn` aborts the commit: nothing is written.
- A create plus a remove of one name in a single transaction is
  `Removed(old)` + `Created(new)` (an ApplyConfig rename).
- `CommitTx` replaces `createMu`. `createServer` becomes
  `Commit("create", tx.CreateServer)`, and storage's
  `CreateUpstreamServer` stays the atomic existence check for the row.

## 2. Two-phase publish and generations (PR1), `internal/runtime/configsvc`

```go
type Snapshot struct {
    Config            *config.Config
    Path              string
    Version           int64            // == commit generation G
    Timestamp         time.Time
    ServerGenerations map[string]ServerGen // immutable
}
type ServerGen struct {
    Gen         int64 // g(name): last create or change
    Incarnation int64 // inc(name): last create; kept through updates
}
func (s *Snapshot) ServerGeneration(name string) (ServerGen, bool)

type PreparedSnapshot struct {
    base   int64            // Version this was prepared against
    snap   *Snapshot        // pre-publish hook applied; observers not yet run
}

func (s *Service) Prepare(cfg *config.Config, source string) (*PreparedSnapshot, error)
func (s *Service) Publish(p *PreparedSnapshot) (*Snapshot, error) // ErrConcurrentPublish if Version moved
func (s *Service) SeedVersion(v int64)                            // boot: meta.config_commit_generation
```

Generation rule, computed in `Prepare`:

| Entry in next vs base | `ServerGenerations[name]` |
|---|---|
| absent in base (created) | `{Gen: G, Incarnation: G}` |
| present, `config.ServerEntryEqual(base, next)` | base value (kept) |
| present, not equal | `{Gen: G, Incarnation: base.Incarnation}` |
| removed and re-created in one commit | `{Gen: G, Incarnation: G}` |
| absent in next | key dropped |

`config.ServerEntryEqual` compares every serialized field, including the
explicit-quarantine bit, `Shared` and `AuthBroker`, and ignores
`Created`/`Updated`.

The pre-publish hook (`SetPrePublishHook`) is removed (review R1.7). Admission
runs in commit step 2. Observers stay, but they must be leaves (today only the
profile-index warm).

`Update` and `UpdateIfCurrent` are kept only as thin wrappers that call
`Prepare`+`Publish`. After PR1 they are called only from `commit.go`.
`ReloadFromFile` is replaced by the commit's `ReplaceConfig` path, so the raw
file is never published unpinned.

## 3. Storage (PR1 and PR2), `internal/storage`

### UpstreamRecord (bbolt `upstreams`, **persisted**)

```go
type UpstreamRecord struct {
    // … existing fields unchanged …
    Generation  int64 `json:"generation,omitempty"`  // g(name) of the commit that last wrote this row
    Incarnation int64 `json:"incarnation,omitempty"` // inc(name); approval writers check this
    OAuthEpoch  int64 `json:"oauth_epoch,omitempty"` // PR2: G of the commit that created the row or last cleared its OAuth state (review R5.3)
}
```

`OAuthEpoch` is set to `G` on create and by every commit that clears the
server's OAuth state because `config.OAuthConfigChanged` is true. That clear
(`lifecycle.go:1488-1489`, `:1543-1551` on `main`) moves into the commit
transaction in PR1, and PR2 stamps the epoch in the same transaction. Any
other update keeps the epoch. A row without the field reads as 0.

### Meta key (**persisted**)

`meta["config_commit_generation"] = G` (decimal string). It is written in the
same transaction as the rows. At startup, `SeedVersion(max(meta, max row
generation))`.

### ServerCommitBatch (PR1)

```go
type ServerCommitBatch struct {
    Generation int64
    Saves      []*config.ServerConfig // each carries its new generation
    Removes    []string               // PR1: row + call history; PR2: full PurgeSet
}
func (m *Manager) ApplyServerCommit(b *ServerCommitBatch) (map[string]PurgeReport, error) // ONE bbolt Update tx
```

The quarantine-keeping invariant of `SaveUpstreamKeepingQuarantine` still
holds per row. The commit's gate step (plan step 2) has already put the
resulting value into `next`, so the invariant never refuses a commit write. A
refusal now indicates a bug and returns an error that aborts the commit.

### PurgeSet (PR2)

Deleted in the same transaction as the row:

| Bucket / state | Key match | Current helper reused |
|---|---|---|
| `upstreams` | `name` | `DeleteUpstream` |
| tool-call history (`server_identities` + per-identity buckets) | every identity whose name is `name` | `deleteToolCallHistoryForName` |
| `tool_approvals` | server `name` | `DeleteServerToolApprovals` |
| `prompt_approvals` | server `name` | `DeleteServerPromptApprovals` (`bbolt.go:790`) |
| `oauth_tokens` (tokens + DCR client credentials) | **exact** `name` or `name_` + 16 lowercase hex (B2 fix) | `ClearOAuthState`, rewritten |
| `oauth_completion` | decoded record `ServerName == name` (keys are `name_<unix>`, review R3.5) | new |
| `integrity_baselines` | `name` | `DeleteIntegrityBaseline` |
| `security_reports`, `security_scan_jobs`, `security_scan_job_index` | server `name` | `DeleteServerScanReports`, `DeleteServerScanJobs` |
| `toolhash` | `name:` prefix with exact separator (A11: verified in T046) | `DeleteToolHash` |
| Spec 117 tool-baseline marker (when it has landed) | `name` | `MarkToolBaselineDecided`'s bucket; the purge is its only clearer |

### Incarnation-conditional writes (PR2, D12)

```go
func (m *Manager) SaveScanJobIfIncarnation(job *ScanJob, inc int64) (written bool, err error)
func (m *Manager) SaveScanReportIfIncarnation(r *ScanReport, inc int64) (bool, error)
// OAuth writes bind to the credential binding, not to the incarnation alone (review R5.3).
type CredentialBinding struct{ Incarnation, OAuthEpoch int64 }
func (m *Manager) SaveOAuthTokenIfBound(rec *OAuthTokenRecord, server string, b CredentialBinding) (bool, error)
func (m *Manager) SaveOAuthClientIfBound(..., b CredentialBinding) (bool, error)
func (m *Manager) SaveOAuthCompletionIfBound(..., b CredentialBinding) (bool, error)
func (m *Manager) RegisterServerIdentityIfIncarnation(sc *config.ServerConfig, path string, inc int64) (*ServerIdentity, bool, error)
func (m *Manager) RecordToolCallIfIncarnation(rec *ToolCallRecord, inc int64) (bool, error) // code_execution exempt
```

Each one runs a single bbolt `Update` that reads `upstreams[server]`, compares
its `Incarnation`, and writes only on a match. `ScanJob` and `ScanReport` gain
`Incarnation int64` (`omitempty`).

The three OAuth writers compare **both** `Incarnation` and `OAuthEpoch` with
the row, in the same transaction. An OAuth config change that keeps the name
and URL keeps the incarnation, and `GenerateServerKey` returns the same key, so
an incarnation check alone would let an old instance's parked token or DCR
write restore credentials that the commit had just cleared (review R5.3). The
retirement barrier cannot prevent this, because the write is local and its
network response has already completed. Each instance captures its
`CredentialBinding` from the row when the post-commit worker constructs it.
The commit that clears OAuth state raises the epoch in the same bbolt
transaction as the clear. A parked write therefore either committed first, and
the clear deletes it, or sees the new epoch and is dropped. Reads that
**reuse** state apply the same binding: the token store `Load`, the DCR client
lookup and `RefreshManager` scheduling. A record is ignored unless it was
written under the current binding, and refresh schedules are keyed by
`(name, Incarnation, OAuthEpoch)`. The token, client and completion records
gain `OAuthEpoch int64` (`omitempty`). A record without it is accepted only
while the row's epoch is also 0.

Not in the set: `activity_records` (audit history), `agent_tokens` and
client credentials (A5), `toolstats` (aggregate counters, not trust).

```go
type PurgeReport struct {
    Server string
    Deleted map[string]int // bucket -> keys removed
}
```

The in-memory purge runs in the same commit, after the transaction:
search-index `DeleteServerTools` + `reindexAffectedProfiles`,
`RefreshManager.OnTokenCleared`, `Supervisor.RevokeInspectionExemption`, the
toolsig cache drop, `discoveryGeneration` reset, and `lastGoodTools`/`lastCaptureAt` deletion (review R2.5). Call stats and the limiter
are already dropped by manager unregistration.

## 4. Manager generations (PR3), `internal/upstream`

```go
type desiredGen struct {
    gen     int64
    present bool // false after removal (a tombstone in the manager only, never in storage or config)
}

type Manager struct {
    // … existing …
    desired map[string]desiredGen // guarded by mu; written only by AdvanceGenerations (under captureMu W + mu)
}

type GenerationChange struct {
    Name      string
    Gen       int64 // new generation, 0 when removed
    Present   bool
    Reconnect bool  // from ServerChange.Reconnect
    Config    *config.ServerConfig
}

// Called only from Commit step 7. Unregisters instances that must not survive,
// restamps the rest, records desired. Returns the unregistered instances for
// asynchronous Disconnect. PR4 also retires them before returning.
func (m *Manager) AdvanceGenerations(changes []GenerationChange) []*managed.Client

func (m *Manager) AddServerConfigAt(name string, cfg *config.ServerConfig, gen int64) error // ErrStaleGeneration
func (m *Manager) RemoveServerAt(name string, gen int64) error                            // ErrStaleGeneration
func (m *Manager) ConnectServerAt(ctx context.Context, name string, gen int64) error        // ErrStaleGeneration
func (m *Manager) DisconnectServerAt(name string, gen int64, inst managed.InstanceID) error // exact instance only (review R5.2)
func (m *Manager) ReplaceInstanceAt(name string, gen int64, expected *managed.Client) error  // ForceReconnectAll / Docker recovery / RestartServer; retires `expected` (review R5.4)

// unregisterLocked is the ONLY code that deletes from m.clients or overwrites
// an entry. It runs under captureMu W + mu, calls old.Retire() (PR4; a no-op
// before PR4) before returning, and returns old for asynchronous Disconnect.
func (m *Manager) unregisterLocked(name string) (old *managed.Client)
```

**Instance identity (review R5.2).** `managed.Client` gains
`InstanceID() managed.InstanceID`, a process-wide monotonic counter assigned
when the instance is constructed and never reused. A generation identifies a
*config*. `ReplaceInstanceAt` installs a new instance at the **same**
generation (restart, `ForceReconnectAll`, Docker recovery), so the generation
alone cannot tell the old instance from its replacement. Every action that
targets an existing instance rather than the desired config binds to
`(gen, InstanceID)`: `DisconnectServerAt`, the disconnect leg of
`ActionReconnect`, and `ReplaceInstanceAt` (through `expected`). The manager
checks, under `captureMu` + `mu`, that `desired[name].gen == gen` **and** that
the registered instance's `InstanceID()` equals the captured one. Otherwise it
returns `ErrStaleGeneration` and does nothing. Actions that target the desired
config (`ConnectServerAt`, `AddServerConfigAt`) stay keyed by generation,
because connecting the current replacement is the intended effect.
`ActorPoolSimple.DisconnectServer` (`actor_pool.go:146-154`) has the same
name-resolution hazard today and is converted in the same task.

**Every unregistration retires (review R5.4).** `AdvanceGenerations`,
`RemoveServerAt` and `ReplaceInstanceAt` all go through `unregisterLocked`.
An instance can therefore never leave `m.clients` without being retired,
whether or not the config generation moved. `ReplaceInstanceAt` retires the
old instance before it installs the replacement. The CI write-site script
(T076) forbids `delete(m.clients` and `m.clients[...] =` in
`internal/upstream/manager.go` outside `unregisterLocked` and the install
helper.

`managed.Client` gains `Generation() int64`. It is set when installed and
restamped on generation-only changes. **This lands in PR1** (review R4.3),
because D15 needs it. PR3 adds the manager-side checks. The legacy `AddServerConfig`/`AddServer`/`RemoveServer`
stay for tests and CLI-only code paths. In the daemon they are replaced by the
`*At` forms, and a CI grep (`scripts/check-upstream-write-sites.sh`, extended)
forbids the legacy forms in `internal/runtime/**`.

The `present=false` entries are bounded by the number of names ever removed
in the process. They are dropped when a later commit creates the name again,
because `present=true` overwrites them.

### Supervisor

`DiscoveryCapture` and the planned action carry `gen` taken from
`configSnapshot.ServerGeneration(name)`. An action that targets an existing
instance (`ActionDisconnect`, the first leg of `ActionReconnect`) also carries
the `InstanceID` of the instance it observed when it was planned. `executeAction`
calls the `*At` methods. `ErrStaleGeneration` produces a debug log and
`ActionNone`.

## 5. Dial gate (PR4), `internal/upstream/dialgate`

```go
type Gate struct {
    mu      sync.RWMutex       // R: admitted sections and conn writes; W: Retire barrier
    retired atomic.Bool
    ctx     context.Context    // cancelled by Retire
    cancel  context.CancelFunc
    connsMu sync.Mutex
    conns   map[*gatedConn]struct{}
}

func New(parent context.Context) *Gate
func (g *Gate) Admit(ctx context.Context) (release func(), err error)         // ErrRetired
func (g *Gate) DialContext(next func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error)
func (g *Gate) Transport(base *http.Transport) *http.Transport                // Clone + gated DialContext (+ DialTLSContext if set)
func (g *Gate) Context() context.Context
func (g *Gate) Retire()                                                       // idempotent, bounded
func (g *Gate) TrackProcess(p ProcessHandle)                                  // called inside the admitted launch section (pgid / Job handle)
func WrapTransport(g *Gate, t transport.Interface) transport.Interface         // gates Start/SendRequest/SendNotification
func (g *Gate) Retired() bool
func (g *Gate) RetireWaiting() bool // test probe: Retire is blocked on an admitted section

type gatedConn struct{ net.Conn; g *Gate }
func (c *gatedConn) Write(b []byte) (int, error) // RLock; if retired → ErrRetired; Conn.Write; RUnlock

// track registers c. It runs INSIDE the admitted dial section, before the
// read lock is released and before the conn is returned to the caller.
// Under connsMu: if retired → close c and return ErrRetired; else add to conns.
func (g *Gate) track(c *gatedConn) error
```

**Connection registration is retirement-aware (review R5.1).** The gated
`DialContext` dials **and** registers the connection in one admitted section,
so a conn is never handed to `net/http` (and so never written) before it is
tracked. `retired` is set while holding `connsMu`, and `track` checks it under
the same mutex. A conn registered after the sweep is therefore closed by
`track` itself, and one registered before the latch is closed by the sweep.
`Retire` also sweeps `conns` a second time after its barrier, so the design
does not rely on a single ordering argument. A `Write` that begins after the
latch returns `ErrRetired` without touching the socket. A `Write` that began
before the latch is on a conn that was already tracked, and the first sweep
closes it, which unblocks it. Without these rules a conn registered after the
sweep could be left open, and a blocked write on it would hold the read lock
against the barrier indefinitely.

The read lock is held only around **one leaf operation** (a dial syscall, one
`Write`, or `cmd.Start` + process registration + `TrackProcess`) and is never
nested. `WrapTransport` takes no lock (review R2.3).

The order inside `Retire`: `connsMu.Lock(); retired.Store(true); connsMu.Unlock()` → `cancel()` → close every
tracked conn → kill every tracked process → `mu.Lock(); mu.Unlock()` → close every
conn and kill every process tracked during the drain → poll ≤1 s for exit (reviews R2.4, R5.1). Any section admitted before
`retired` was set has finished by the time the barrier returns, because its
conn is closed or its `cmd.Start` has returned. Any later `RLock` observes
`retired`.

Wiring: `managed.NewClient` creates the gate. `core.Client` receives it and
passes `gate.Transport(...)` to every `transport.CreateHTTPClient` branch
(`transport/http.go:245-262`, `:444`, `:470`). `oauth.CreateOAuthConfig*`
gets `WithBaseTransport(gate.Transport(...))` (`oauth/config.go:1255-1265`).
The stdio, Docker and launcher `Start` calls run under `gate.Admit`.

## 6. Errors

| Error | Package | Meaning | Surface mapping |
|---|---|---|---|
| `ErrServerNotFound` | runtime | name absent from the live config | REST 404, MCP `isError` "Server 'x' not found", CLI exit 1 |
| `*ServerExistsError` | runtime (moved from server) | create of a present name | REST 409 (unchanged text "already exists") |
| `ErrServerReplaced` | runtime | captured incarnation (or, for `ExpectGeneration`, generation) no longer current | REST 409 `{"error":"server_replaced"}`, MCP `isError`, CLI exit 1 |
| `ErrStaleGeneration` | upstream | worker or action bound to an old generation | internal; debug log |
| `ErrRetired` | dialgate | operation on a retired instance | internal; tool calls on a retired instance return the existing "server unavailable" error |
| `ErrConcurrentPublish` | configsvc | `Publish` saw an unexpected version | internal invariant violation; logged as an error and the commit is aborted |

## 7. Wire additions (**wire**)

- `GET /api/v1/servers`, `GET /api/v1/servers/{id}` and the review payload gain
  `"generation": <int64>` and `"incarnation": <int64>`. This is additive.
- Approve, block and unquarantine endpoints accept an optional
  `"expected_incarnation": <int64>`. A mismatch returns 409 `server_replaced`.
  The payloads also expose `"incarnation"`.
- `oas/swagger.yaml` and `docs/api/rest-api.md` are updated in PR2.

## 8. State transitions of a server name

```
(absent) --Create@G1--> present(g=G1,inc=G1) --Update(eq)--> present(g=G1,inc=G1)
                         |  --Update(ne)@G2--> present(g=G2,inc=G1)  [instance restamped or retired+replaced; approvals unaffected]
                         |  --Update(oauth)@G2'-> present(g=G2',inc=G1,oauthEpoch=G2')  [OAuth state cleared; old-epoch credential writes dropped]
                         |  --Restart (same g)--> instance replaced (new InstanceID), old one retired
                         |  --Remove@G3-----> absent                 [purge set deleted, instance retired]
absent  --Create@G4--> present(g=G4,inc=G4)  with G4 > G3 > G2 > G1, empty purge set, every inc=G1 approval writer rejected
```
