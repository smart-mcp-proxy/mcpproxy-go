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
    replaced map[string]bool // RemoveServer(name) then CreateServer(name) in this tx (review R8.2)
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
    Kind      ChangeKind // Created | Updated | Removed | Replaced (review R8.2)
    Old       commitview.ServerGen // zero when Created
    New       commitview.ServerGen // zero when Removed
    Reconnect bool       // connection fields, Enabled or Quarantined changed (config.ConnectionEquivalent + flags); always true for Replaced
    QuarantineRaised bool
}
```

Rules:
- `fn` runs with `configCommitMu` held. It MUST NOT call `Commit`,
  `SaveConfiguration`, `ApplyConfig`, `QuarantineServer`, `EnableServer` or
  take an approval lock. A test hook asserts re-entry (`commitDepth`).
- An error from `fn` aborts the commit: nothing is written.
- **Same-name replacement (review R8.2).** `RemoveServer(n)` followed by
  `CreateServer(n)` in one transaction sets `tx.replaced[n]`. It is one
  `Replaced` change, whose `Old` is the base `ServerGen` and whose `New` is
  `{G, G, G}`. That holds even when the re-created entry is field-for-field
  equal to the removed one. Every consumer handles `Replaced` exactly as
  `Removed(Old)` followed by `Created(New)`:
  - step 4 takes the name's approval lock;
  - step 6 runs the full purge set for the name, then writes the new row, in
    the same bbolt transaction (`ServerCommitBatch.Replaces`, §3);
  - the in-memory purge runs for it;
  - step 7 unregisters (and, from PR4, retires) the old instance and records
    `desired = {G, present}`;
  - after unlock, a connect worker is spawned for `(n, G)` when the new entry
    is enabled.

  Further ops on `n` in the same transaction keep the `Replaced` change.
  `UpdateServer(n)` edits the new entry. A second `RemoveServer(n)` turns it
  into `Removed(Old)`. `CreateServer(n)` of a name that is absent from the base,
  followed by `RemoveServer(n)`, is no change. A rename (remove `a`, create
  `b`) is two changes: `Removed(a)` and `Created(b)`. `ReplaceConfig` (a
  whole-config apply or a file reload) cannot express a replacement, because a
  config file identifies a server only by its name. An entry that keeps its
  name there is an update, as it is on `main`. No writer on `main` removes and
  re-creates a name in one transaction. The rule exists so that the
  `CommitTx` API cannot express a replacement that the model then collapses
  into "unchanged" or "updated", which would keep the old incarnation and
  binding.
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
// ServerGen is defined in the leaf package internal/commitview (review R7.2)
// and aliased here, so configsvc and storage never import each other.
type ServerGen = commitview.ServerGen
func (s *Snapshot) ServerGeneration(name string) (ServerGen, bool)

type PreparedSnapshot struct {
    base   int64            // Version this was prepared against
    snap   *Snapshot        // pre-publish hook applied; observers not yet run
}

// Prepare takes the transaction's replacement set explicitly (review R8.2):
// the final config alone cannot tell a remove-and-re-create from an unchanged
// or updated entry. Every name in in.Replaced must be present in both base and
// in.Config; otherwise Prepare returns an invariant error and the commit aborts.
type PrepareInput struct {
    Config   *config.Config
    Source   string
    Replaced map[string]bool // from CommitTx.replaced; nil for ReplaceConfig/Bootstrap
}
func (s *Service) Prepare(in PrepareInput) (*PreparedSnapshot, error)
func (s *Service) Publish(p *PreparedSnapshot) (*Snapshot, error) // ErrConcurrentPublish if Version moved
func (s *Service) SeedVersion(v int64)                            // boot: meta.config_commit_generation
```

Generation rule, computed in `Prepare`:

| Entry in next vs base | `ServerGenerations[name]` |
|---|---|
| name in `in.Replaced` (removed and re-created in one commit), checked **first**, equal or not | `{Gen: G, Incarnation: G}` (and `OAuthEpoch: G`) |
| absent in base (created) | `{Gen: G, Incarnation: G}` |
| present, `config.ServerEntryEqual(base, next)` | base value (kept) |
| present, not equal | `{Gen: G, Incarnation: base.Incarnation}` |
| absent in next | key dropped |

`PreparedSnapshot` also carries the `Changes` map computed from the same
inputs, so the snapshot, `CommitResult.Changes` and the rows written in step 6
can never disagree about a replacement.

### Shared value types: `internal/commitview` (PR1 creates it, PR2 extends it; review R7.2)

`configsvc` already imports `config`, and `storage` imports `config`, `auth`
and `security/scanner`. Neither imports the other today. If `configsvc`
returned a `storage` type and `storage` accepted a `configsvc` type, the two
packages would import each other and PR2 would not compile. The value types
that both sides need therefore live in a new leaf package. That package
imports only the standard library:

```go
package commitview // internal/commitview; imports: errors only

type ServerGen struct {
    Gen         int64 // g(name): last create or change
    Incarnation int64 // inc(name): last create; kept through updates
    OAuthEpoch  int64 // PR2: G of the create or of the last commit that cleared OAuth state (review R6.1)
}
type CredentialBinding struct{ Incarnation, OAuthEpoch int64 } // PR2
func (g ServerGen) Binding() CredentialBinding                   // PR2

var ErrStaleGeneration = errors.New("stale config generation")
```

`configsvc.ServerGen`, `storage.CredentialBinding` and
`upstream.ErrStaleGeneration` are aliases (`type X = commitview.X`, `var
ErrStaleGeneration = commitview.ErrStaleGeneration`), so `errors.Is` works
across packages. `oauth`, `upstream/core` and `upstream/managed` take
`commitview.CredentialBinding` directly. PR1 adds `TestCommitviewIsLeaf`, which
runs `go list -deps ./internal/commitview` and fails on any module-internal
import. T055d compiles and tests `./internal/commitview ./internal/runtime/configsvc
./internal/storage ./internal/oauth ./internal/upstream/...` bare and with
`-tags server`.

`OAuthEpoch` (PR2) follows the same table with one difference: on a changed
entry it is `G` when `config.OAuthConfigChanged(base, next)` is true (the
commit clears OAuth state, §3) and the base value otherwise. A created entry
gets `G`. `Prepare` computes it, so the snapshot, the `CommitResult` and the
row the commit writes in step 6 all carry the same
`{Gen, Incarnation, OAuthEpoch}`: one committed view (review R6.1).

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
    Replaces   []string               // review R8.2: delete (PR2: purge) the old row, then save the new one, in this tx
}
```

`ApplyServerCommit` processes `Removes` and `Replaces` deletions before
`Saves`. A name may appear in both the delete side and `Saves` only when it is
listed in `Replaces`. Any other overlap is an invariant error that aborts the
transaction. For a replaced name, the new row carries `Incarnation = G` and
`OAuthEpoch = G`, so a parked old approval writer (bound to the old
incarnation) and a parked old OAuth writer (bound to the old binding) both fail
their conditional writes. The returned `PurgeReport` includes the name.

```go
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
// Construction-time check (review R6.1): one bbolt View, row must equal sg exactly.
func (m *Manager) CheckCommittedView(server string, sg commitview.ServerGen) error // commitview.ErrStaleGeneration (review R7.2: storage never imports configsvc)
// Instance-originated destructive OAuth operations (review R6.2, R7.3): binding
// check, observed-revision check, and the delete in one bbolt Update.
func (m *Manager) DeleteOAuthTokenIfBound(serverKey, server string, b CredentialBinding, observedRev uint64) (bool, error)
func (m *Manager) ClearOAuthClientIfBound(serverKey, server string, b CredentialBinding, observedRev uint64) (bool, error)
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
network response has already completed.

**The binding comes from the committed view, never from the row (review
R6.1).** An instance's `CredentialBinding` is `sg.Binding()` of the
`ServerGen` it was built for, taken from the **same** `CommitResult` or
published `Snapshot` as its `cfg` and `Gen` (§2). A worker never reads the
binding from the `upstreams` row and adopts it. Reading it from the row would
let a stale worker pair an old config with a new binding. For example, a
commit that changes OAuth config writes the new epoch in step 6 and pauses
before step 7. An old-generation worker still passes the desired-generation
check (the manager has not advanced), builds an instance with the old config,
reads the new epoch and saves old-config credentials that pass
`Save*IfBound`. Before the instance is built, and before any OAuth config is
created for it (`createOAuthConfigInternal` can clear DCR state, see below),
the worker calls `CheckCommittedView(name, sg)`. In one bbolt `View`, it
requires the row to exist and to equal `sg` in `Generation`, `Incarnation`
and `OAuthEpoch`. On any mismatch it returns `ErrStaleGeneration` and the
worker builds nothing. That is a debug log, not an error. The rule covers
every constructor: the post-commit workers, supervisor `ActionConnect`, and
`postConfigReload`'s `ConnectAll` (each with the `ServerGen` of its
snapshot), plus `RestartServer`/`ReplaceInstanceAt`, which reuse the binding
of the instance they replace. A check that passes is not a lease. If a commit
lands after the check, the instance holds the older binding, so its
`…IfBound` writes and binding-conditional deletes are dropped, and PR3/PR4
unregister and retire it. A generation-only mismatch with an equal binding is
also rejected. It loses no connection: the newer commit's publish triggers a
supervisor reconcile that connects the server from the newer snapshot, and
after PR3 step 7 restamps an instance that is already installed. This check
lands in **PR2**, because the remove-and-re-add form of the race (the row's
incarnation moved) exists before PR3 does.

The commit that clears OAuth state raises the epoch in the same bbolt
transaction as the clear. A parked write therefore either committed first, and
the clear deletes it, or sees the new epoch and is dropped. Reads that
**reuse** state apply the same binding: the token store `Load`, the DCR client
lookup and `RefreshManager` scheduling. A record is ignored unless it was
written under the current binding, and refresh schedules are keyed by
`(name, Incarnation, OAuthEpoch)`. The token, client and completion records
gain `OAuthEpoch int64` (`omitempty`). A record without it is accepted only
while the row's epoch is also 0.

**Refresh flights are bound too (review R7.1).** On `main` the process-wide
`RefreshCoordinator` (`oauth/refresh_coordinator.go:193-216`) keys flights,
the terminal latch, the transient cooldown and `lastSavedAt` by
`GenerateServerKey` alone. A caller that arrives while a flight runs joins it
and gets that flight's token or error, and its own `Load` never runs. Binding
the reads and the schedules does not cover this. Suppose an old instance
reads its token and pauses before `Do`. An OAuth-changing commit (or a remove
and re-add with the same URL) advances the binding, and the replacement starts
a refresh under the same key. The old instance then resumes, joins the
replacement's flight and receives the replacement's token, which it uses on
its own still-open connection. The reverse also happens: a replacement joins
an obsolete flight and inherits its failure, or its terminal latch.

PR2 therefore keys the coordinator by binding:

```go
type RefreshRequest struct {
    Key     string                       // GenerateServerKey, unchanged
    Binding commitview.CredentialBinding // NEW: the caller's committed binding
    // … ServerName, Trigger, ObservedRefreshToken unchanged …
    Load    func() (*storage.OAuthTokenRecord, error) // the caller's BOUND loader
    // Save goes through SaveOAuthTokenIfBound with Binding
}
type flightKey struct {
    serverKey string
    binding   commitview.CredentialBinding
}
// keys map[flightKey]*refreshKeyState: flight, latch, cooldown and lastSavedAt
func (c *RefreshCoordinator) Forget(serverKey string, b commitview.CredentialBinding) // drops the state for one binding
```

- `Do` joins a flight only when the `flightKey` is equal, so callers with
  different bindings never share a flight, a result, a latch or a cooldown.
- The flight runs the `Load` of the request that started it. Every joiner has
  the same binding, so that loader is also the joiner's bound loader. Under an
  obsolete binding the bound `Load` returns no record, and the flight returns
  `ErrNoRefreshToken` with no network request. A flight whose binding goes
  stale while it is on the network persists through `SaveOAuthTokenIfBound`,
  which drops the write. Its token reaches only its own joiners, which are the
  retired or soon-to-be-retired instance.
- `RefreshOutcome` gains `Binding`. The completion hook and `RefreshManager`
  act on an outcome only when it equals their schedule key
  `(name, Incarnation, OAuthEpoch)`, so a stale flight's terminal failure
  cannot mark the replacement as needing sign-in.
- `RefreshManager.OnTokenCleared`, the in-memory purge (§PurgeSet) and the
  OAuth-config clear call `Forget` for the old binding, so the map stays
  bounded by the live bindings plus in-flight stale ones. A stale flight that
  is still running removes its own entry when it finishes, because `run`
  compares the entry pointer before clearing it.
- Both call sites pass the instance binding: `persistent_token_store.go:437`
  (the store holds it from construction) and `core/client.go:807` /
  `core/oauth_refresh.go:161` (`RefreshOAuthTokenDirect`, from the
  `managed.Client`'s committed view).

The lock order is unchanged: the coordinator's `mu` is a leaf below L2, as
today.

**Destructive OAuth operations are binding-conditional too (review R6.2).**
Saves and reuse-reads are not the only local mutations a stale instance can
make. On `main` an instance also deletes credentials unconditionally:

| Site | Operation | Trigger |
|---|---|---|
| `core/connection_oauth.go:573-574` → `PersistentTokenStore.ClearToken` (`persistent_token_store.go:331-335`) | `DeleteOAuthToken(serverKey)` | 5xx during MCP initialize |
| `oauth/config.go:812-816` `clearOAuthCredentials`, called at `:1026`, `:1043`, `:1057`, `:1134` | `ClearOAuthClientCredentials(serverKey)` | stale DCR record seen while building the OAuth config (legacy port, unpinned record, redirect mismatch, callback port change) |
| `persistent_token_store.go:122`, `core/oauth_refresh.go:168` | `ClearOAuthClientCredentialsIf` (compare on client id and refresh token) | refresh rejected |
| `core/oauth_stale_dcr_client.go:196`, `:322` | `ClearOAuthClientCredentialsIfClientID` | authorization rejected the persisted client |

The interleaving: the old instance parks just before the delete, an OAuth
config change (or a remove and re-add) commits, the replacement saves fresh
credentials under the new binding, and then the old delete resumes and erases
them. The key is the same, because `GenerateServerKey` uses only the name and
URL. Retirement does not stop it, because the delete is a local storage
mutation.

Every instance-originated delete therefore goes through
`DeleteOAuthTokenIfBound` or `ClearOAuthClientIfBound`, with the binding of
the instance (or, for the transient preflight and login clients, the binding
captured with their config). Each runs one bbolt `Update`. It reads
`upstreams[server]` and requires `{Incarnation, OAuthEpoch}` to equal the
binding. It reads the record and requires the record's own `OAuthEpoch` to
equal the binding's epoch, under the same legacy-zero rule as reads. Then it
requires the record's `Revision` to equal `observedRev`, the revision of the
exact record the caller based its decision on. Only then does it delete. On
any mismatch it deletes nothing and returns `false`, and the caller logs at
debug level and goes on with a fresh flow.

**Record revision, not a content compare (review R7.3).** A field compare
cannot stand in for a revision. A same-binding replacement (`RestartServer`,
`ForceReconnectAll`, Docker recovery) can keep the access token, or re-pin the
same DCR client ID with a new callback port or redirect URI. A parked old
clear would then pass the binding, epoch and field checks and erase the
replacement's record, including `ClientSecret`, `CallbackPort` and
`RedirectURI` (`bbolt.go:1186-1189`). So `OAuthTokenRecord` gains
`Revision uint64` (`json:"revision,omitempty"`). **Every** write to the
`oauth_tokens` bucket sets `Revision = bucket.NextSequence()` inside its own
transaction. On `main` that is the six `Put` sites in `bbolt.go`
(`SaveOAuthToken`, `UpdateOAuthClientCredentials`, `saveOAuthClientCredentialsIf`,
`ClearOAuthClientCredentials`, `UpdateOAuthToken` and
`clearOAuthClientCredentialsWhere`), plus the PR2
`…IfBound` savers. Deletes (`DeleteOAuthToken`, `manager.go:2475`
`ClearOAuthState`) remove the key and need no revision. A unit test enumerates
the bucket's write paths and fails if one leaves `Revision` unchanged. The
bucket sequence is persisted and never reused, so a deleted and re-created
record cannot collide with an older revision. A legacy record reads as
revision 0 until its first rewrite. The observed revision comes from the same
read as the decision:

| Clear | `observedRev` |
|---|---|
| 5xx `ClearToken` | the revision of the record `PersistentTokenStore.GetToken` last served to this instance |
| the four config-build clears in `createOAuthConfigInternal` | the revision of the record the hygiene check read |
| refresh rejected (`persistent_token_store.go:122`, `core/oauth_refresh.go:168`) | the revision of the record the flight's bound `Load` returned |
| stale DCR client (`core/oauth_stale_dcr_client.go:196`, `:322`) | the revision of the record whose client the authorization used |

The existing `…If` content predicates become redundant, because an equal
revision implies equal content, and they are removed. When the replacement
only *read* the record (a restart that reuses the persisted token without
writing), the revision is unchanged and the old clear still applies. That is
intended: it acts on exactly the state it inspected, and the replacement
inspected the same state. Any write by the replacement (a refresh, a sign-in,
a DCR re-pin, a callback-port change) bumps the revision, and the old clear
returns `false`.
The unconditional `BoltDB.DeleteOAuthToken` and `ClearOAuthClientCredentials`
become unexported storage primitives. The PR2 write-site grep (T055d) forbids
calling them, or the old exported names, from `internal/upstream/**` and
`internal/oauth/**`. `storage.Manager.ClearOAuthState` may be called only
from the commit and from `upstream.Manager.ClearOAuthToken` (logout).
The one non-daemon caller is converted in the same PR (review R8.3, below).

**Intentional current-state clears stay separate.** These are deliberate
operator or commit actions on whatever is current, not stale-instance
cleanup: the commit's own OAuth-config clear and removal purge (in the
commit transaction, which also raises the epoch), and `Manager.ClearOAuthToken`
for logout (`upstream/manager.go:3425`). They keep the exact-key
`ClearOAuthState` and are not binding-conditional.

**Standalone CLI logout (review R8.3).** `mcpproxy auth logout` without a
daemon (`cmd/mcpproxy/auth_cmd.go:932-939`) calls
`internal/upstream/cli/client.go:473`, which calls
`c.storage.DeleteOAuthToken(serverKey)` on a `*storage.BoltDB`. Unexporting
that method would break the build of `./cmd/mcpproxy` in both editions, and
keeping it would violate the write-site grep. PR2 therefore adds one
supported, exported current-state clear:

```go
// ClearOAuthTokenForStandaloneLogout deletes the token record at exactly
// serverKey (GenerateServerKey(name, url)). It is an operator action on the
// current state, not stale-instance cleanup, so it is not binding-conditional.
func (b *BoltDB) ClearOAuthTokenForStandaloneLogout(serverKey string) error
```

`cli.Client.ClearOAuthToken` calls it instead of `DeleteOAuthToken`. The
semantics are unchanged from `main`: one exact key, the token record only, and
the same error text. It needs no binding, because no instance can race it. The
CLI path runs only when no daemon is detected, and `cli.NewClient` opens
`config.db` itself. bbolt's exclusive file lock means that a daemon holding the
database makes that open fail, and the CLI then has no storage and returns
"storage not available". No live instance shares the database while the clear
runs. The write-site grep keeps the ban on the unconditional primitives and
their old names in `internal/upstream/**` and `internal/oauth/**`. It allows
`ClearOAuthTokenForStandaloneLogout` in exactly one file,
`internal/upstream/cli/client.go`. The script's self-test plants a call in
another `internal/upstream` file and expects a failure.

The logout flow's
non-atomic flag, clear and disconnect sequence (`runtime.go:3250-3271`) is a
pre-existing race, recorded on #1563 for PR3.

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
    Replaced  bool  // review R8.2: unregister (retire) the old instance even if the new entry is equal
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
func WrapTransport(g *Gate, t transport.Interface) transport.Interface         // gates Start/SendRequest/SendNotification; capability-preserving (review R8.1)
func Unwrap(t transport.Interface) transport.Interface                        // follows Unwrap() until a non-wrapper
type Unwrapper interface{ Unwrap() transport.Interface }
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

**The wrapper preserves every capability that is asserted on the transport
(review R8.1).** Both mcp-go and core discover optional behaviour by type
assertion on the transport. A wrapper that only embeds `transport.Interface`
would make each assertion fail silently. Verified against mcp-go v1.0.0 and
`main`:

| Capability | Asserted at | Inner types that have it | Lost if hidden |
|---|---|---|---|
| `transport.BidirectionalInterface` (`SetRequestHandler`) | mcp-go `client/client.go:201` | `*Stdio`, `*StreamableHTTP` | every server-to-client request (`ping` today; sampling, elicitation and roots if handlers are ever registered, `client.go:709-721`); unanswered, the upstream's request times out |
| `transport.HTTPConnection` (`SetProtocolVersion`) | mcp-go `client/client.go:403` | `*StreamableHTTP`, `*SSE` | the `Mcp-Protocol-Version` header after negotiation |
| `SetConnectionLostHandler(func(error))` | mcp-go `client/client.go:226-229`, used by core `OnConnectionLost` (`connection_http.go:249`, `:291`; `connection_oauth.go:744`) | `*SSE` | SSE connection-loss detection |
| `RequiresLegacyProtocol() bool` | mcp-go `client/protocol.go:44` | `*StreamableHTTP` | the legacy-protocol pin for continuous listening |
| `GetOAuthHandler()` / `IsOAuthEnabled()` | core `client.go:756-765` (`extractOAuthHandler`), used by `bindOAuthRefresher` (`oauth_refresh.go:182`), `RefreshOAuthTokenDirect` (`client.go:780`), `forcedAuthorizationRequired` (`connection_oauth.go:2047`) and `getOAuthHandlerLocked` | `*StreamableHTTP`, `*SSE` | proactive refresh ("no OAuth handler available"), the Spec 113 reactive-refresh binding, and the GH #1271 forced-login path for per-method-auth upstreams |
| concrete `*transport.StreamableHTTP` (session id) | core `session_reinit.go:33` (`SessionSnapshot`) | `*StreamableHTTP` | Spec 113-e 404 session re-initialisation (the snapshot reports an empty id) |

mcp-go's own `LoggingTransport` (`client/transport/logging.go`) shows the
same hazard. It mirrors `BidirectionalInterface` and `HTTPConnection` but not
the other three rows, so it is not reused.

The contract has two parts:

1. **Forwarding, for assertions inside mcp-go**, which core cannot change.
   Core builds exactly three transport kinds, so `WrapTransport` switches on
   the concrete inner type and returns one of three wrapper types:
   `gatedStdio`, `gatedStreamableHTTP` and `gatedSSE`. Each one implements
   exactly the capability rows of its inner kind, and each forwarding method
   delegates to the inner transport. `SetRequestHandler` installs a handler
   that answers with a JSON-RPC error and never calls the client's handler
   once the gate is retired, so a retired instance does no work for the
   upstream. The other forwarders are pure delegation. They take no
   gate lock, and they send nothing that the gated `Write` and dial do not
   already cover. An unknown inner type gets a generic wrapper, and the parity
   test below fails for it, so a new transport kind cannot ship without its own
   wrapper.
2. **Unwrapping, for assertions inside core.** Every wrapper implements
   `Unwrap()`. Core never asserts on `GetTransport()` directly. One helper,
   `core.innerTransport(cl *client.Client) transport.Interface` (which is
   `dialgate.Unwrap(cl.GetTransport())`), feeds `extractOAuthHandler` and
   `SessionSnapshot`. The concrete `*transport.StreamableHTTP` assertion is
   kept, but it now runs on the unwrapped value. Exposing the inner OAuth
   handler does not bypass the gate. Its HTTP client is built on the
   instance's gated `*http.Transport` (FR-024), and `ReinitializeSession`
   sends through the wrapper's gated `SendRequest`. The write-site script
   (T099) forbids `GetTransport()` in `internal/upstream/**` outside
   `core/transport_unwrap.go`.

Parity is enforced mechanically. `TestWrapTransportCapabilityParity` builds
each production kind through the real constructors (the stdio branch and each
`transport.CreateHTTPClient` branch: plain, headers, OAuth, SSE). For each of
the six rows it asserts that the wrapper satisfies the row exactly when the
inner value does, and that `Unwrap` returns the inner value.
`TestMCPGoTransportMethodSetPinned` lists the exported methods of
`*Stdio`, `*StreamableHTTP` and `*SSE` by reflection and compares them with a
reviewed list, so an mcp-go upgrade that adds an optional interface fails CI
until the wrapper is updated.

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
| `ErrStaleGeneration` | commitview (aliased in upstream and returned by storage's `CheckCommittedView`) | worker or action bound to an old generation | internal; debug log |
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
                         |  --Remove+Create in one commit @G5--> present(g=G5,inc=G5,oauthEpoch=G5)  [Replaced: purge, old instance retired, new connect; even when the entry is equal]
absent  --Create@G4--> present(g=G4,inc=G4)  with G4 > G3 > G2 > G1, empty purge set, every inc=G1 approval writer rejected
```
