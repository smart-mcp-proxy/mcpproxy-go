package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// Spec 116 PR1: one config commit section.
//
// Every change to the server set or to a server's fields is a commit. A commit
// holds configCommitMu (L0) from reading the live configuration until the new
// configuration is published, and runs one pipeline (plan.md §Commit pipeline):
//
//  1. Build    clone the live config, run fn against the copy.
//  2. Gate     the #937 admission gate against the stored view.
//  3. Prepare  configsvc.Prepare: commit generation G, per-server generations,
//     the change set; the projection diff against storage.
//  4. Locks    the approval lock (L1) of every changed server, sorted; then
//     the UnderApprovalLock hooks.
//  5. Disk     capture the raw file, write the new file (self-write marker).
//  6. Storage  one bbolt transaction (rows with their stamps, the removal
//     purge set of every removed or replaced server, meta), then
//     the index purge and the in-memory purge; release L1. On
//     failure the raw file of step 5 is restored and nothing is
//     published.
//  7. Advance  restamp retained instances (RestampRetained).
//  8. Publish  configsvc.Publish and the legacy r.cfg / desiredCfg swap.
//
// After the lock is released: manager removals, connect workers, discovery
// requests, events and telemetry, at most once per commit.
//
// Lock order (plan.md §Lock order): L0 configCommitMu → L1 approval locks
// (sorted; released before step 7) → L2 storage; L0 → L3 captureMu (W) → L4
// Manager.mu in step 7; L0 → L6 configsvc.updateMu; L0 → L7 Runtime.mu. No code
// holding L1-L7 may call Commit.

// ServerChange is one server's change in a commit.
type ServerChange = configsvc.ServerChange

// CommitResult describes a commit that returned without error.
type CommitResult struct {
	// Generation is the commit generation G, or 0 when nothing was published.
	Generation int64
	// Snapshot is the published snapshot (nil when nothing was published).
	Snapshot *configsvc.Snapshot
	// Changes is the per-server change set.
	Changes map[string]ServerChange
	// Published reports whether the commit published a configuration.
	Published bool
	// DiskWritten reports whether the commit wrote the config file.
	DiskWritten bool
	// Restamped lists the retained instances step 7 updated in place.
	Restamped []string
	// Purged reports, per removed or replaced server, what the storage step
	// deleted from the purge set (Spec 116 PR2, data-model §PurgeSet).
	Purged map[string]storage.PurgeReport
}

// ServerNotFoundError reports a server that is not in the live configuration.
type ServerNotFoundError struct{ Name string }

func (e *ServerNotFoundError) Error() string { return fmt.Sprintf("server '%s' not found", e.Name) }

// Is makes errors.Is(err, ErrServerNotFound) match.
func (e *ServerNotFoundError) Is(target error) bool { return target == ErrServerNotFound }

// ErrServerNotFound matches every *ServerNotFoundError.
var ErrServerNotFound = errors.New("server not found")

// ServerExistsError is returned when a create names a server that is already
// configured (or still stored). Its text is part of the #1570 contract: the
// REST 409 mapping, cliclient and the registry add all match "already exists".
type ServerExistsError struct{ Name string }

func (e *ServerExistsError) Error() string { return fmt.Sprintf("server '%s' already exists", e.Name) }

// ErrServerReplaced reports that the server a caller bound to (by generation or
// incarnation) was removed or replaced before its commit ran. It is
// commitview.ErrServerReplaced, so the scanner and the HTTP layer match it
// without importing the runtime.
var ErrServerReplaced = commitview.ErrServerReplaced

// ErrCommitInvariant marks a commit that violated a Spec 116 invariant: a bug,
// never a user error.
var ErrCommitInvariant = errors.New("config commit invariant violated")

// errCommitReentry is returned when code running inside a commit calls Commit
// again on the same goroutine, which would otherwise deadlock on L0.
var errCommitReentry = fmt.Errorf("%w: Commit called from inside a commit", ErrCommitInvariant)

// CommitTx is the working copy a commit's fn mutates. It is only valid inside
// fn. Its methods never touch storage, disk or the manager; the pipeline does.
type CommitTx struct {
	r      *Runtime
	reason string
	base   *configsvc.Snapshot
	next   *config.Config

	baseNames map[string]bool
	created   map[string]bool // created in this tx and absent from base
	removed   map[string]bool // present in base and removed in this tx
	replaced  map[string]bool // removed and re-created in this tx (review R8.2)
	// quarantineDecided: servers an UpdateServer or SetQuarantined in this tx
	// left explicitly quarantined (#1561): the index purge covers them even
	// when the admission gate had quarantined the entry implicitly, and even
	// when the entry already was an explicit quarantine (Sol round 12): a
	// repeated quarantine removes residual descriptions a failed purge or an
	// older install left behind, as Server.UpdateServer did on main. A
	// quarantined server has nothing indexed legitimately, so the purge is
	// idempotent.
	quarantineDecided map[string]bool

	hooks []approvalHook

	reconcile       bool
	replacedConfig  bool
	bootstrap       bool
	forceDisk       bool
	skipDisk        bool
	suppressWorkers bool
	disk            *commitDiskPlan
	path            string // UpdateConfig's new file path; published only with the commit
	desired         *config.Config
	onPublish       []func(oldCfg, newCfg *config.Config)
	after           []func(*CommitResult)
	eventExtra      map[string]any

	storedLoaded bool
	stored       map[string]*storage.StoredServer
	storedErr    error

	err error
}

type approvalHook struct {
	name string
	fn   func() error
}

// commitDiskPlan replaces the default disk write of step 5 (ApplyConfig writes
// the caller's full desired config with its edits).
type commitDiskPlan struct {
	cfg       *config.Config
	mergeBase *config.Config
	path      string
}

// Server returns a deep copy of the working entry for name.
func (tx *CommitTx) Server(name string) (*config.ServerConfig, bool) {
	if sc := tx.entry(name); sc != nil {
		return config.CopyServerConfig(sc), true
	}
	return nil, false
}

// Generation returns name's committed generation in the base snapshot.
func (tx *CommitTx) Generation(name string) (configsvc.ServerGen, bool) {
	return tx.base.ServerGeneration(name)
}

// Base returns the live configuration the commit started from. Read-only.
func (tx *CommitTx) Base() *config.Config { return tx.base.Config }

// Config returns the working configuration. Callers may read it; they change
// it only through the CommitTx operations (or MutateGlobal for global fields).
func (tx *CommitTx) Config() *config.Config { return tx.next }

func (tx *CommitTx) entry(name string) *config.ServerConfig {
	for _, sc := range tx.next.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

func (tx *CommitTx) entryIndex(name string) int {
	for i, sc := range tx.next.Servers {
		if sc != nil && sc.Name == name {
			return i
		}
	}
	return -1
}

func (tx *CommitTx) loadStored() (map[string]*storage.StoredServer, error) {
	if !tx.storedLoaded {
		tx.storedLoaded = true
		if tx.r.storageManager != nil {
			if f := tx.r.testHooks.storedReadFault; f != nil {
				if err := f(tx.reason); err != nil {
					tx.stored, tx.storedErr = nil, err
					return tx.stored, tx.storedErr
				}
			}
			tx.stored, tx.storedErr = tx.r.storageManager.ListStoredServers()
		}
	}
	return tx.stored, tx.storedErr
}

// CreateServer adds a new server. It fails with *ServerExistsError when the
// name is in the working copy, or when a row is still stored for a name that
// is not in the live configuration (#1570: an add never overwrites).
func (tx *CommitTx) CreateServer(sc *config.ServerConfig) error {
	if sc == nil || strings.TrimSpace(sc.Name) == "" {
		return fmt.Errorf("server name is required")
	}
	if tx.entry(sc.Name) != nil {
		return &ServerExistsError{Name: sc.Name}
	}
	if !tx.baseNames[sc.Name] {
		stored, err := tx.loadStored()
		if err != nil {
			return fmt.Errorf("failed to read stored servers: %w", err)
		}
		if _, ok := stored[sc.Name]; ok {
			return &ServerExistsError{Name: sc.Name}
		}
	}
	tx.next.Servers = append(tx.next.Servers, config.CopyServerConfig(sc))
	if tx.removed[sc.Name] {
		delete(tx.removed, sc.Name)
		tx.replaced[sc.Name] = true
	} else if !tx.baseNames[sc.Name] {
		tx.created[sc.Name] = true
	}
	return nil
}

// UpdateServer runs fn on a deep copy of the live entry and stores the result.
// fn must not change the name. Unknown names fail with *ServerNotFoundError.
func (tx *CommitTx) UpdateServer(name string, fn func(cur *config.ServerConfig) error) error {
	i := tx.entryIndex(name)
	if i < 0 {
		return &ServerNotFoundError{Name: name}
	}
	cp := config.CopyServerConfig(tx.next.Servers[i])
	if err := fn(cp); err != nil {
		return err
	}
	if cp.Name != name {
		return fmt.Errorf("%w: UpdateServer cannot rename %q to %q", ErrCommitInvariant, name, cp.Name)
	}
	if cp.Quarantined && cp.QuarantineExplicitlySet() {
		if tx.quarantineDecided == nil {
			tx.quarantineDecided = map[string]bool{}
		}
		tx.quarantineDecided[name] = true
	}
	tx.next.Servers[i] = cp
	return nil
}

// SetEnabled sets a server's enabled flag.
func (tx *CommitTx) SetEnabled(name string, enabled bool) error {
	return tx.UpdateServer(name, func(cur *config.ServerConfig) error {
		cur.Enabled = enabled
		return nil
	})
}

// SetQuarantined sets a server's quarantine flag and records it as an explicit
// decision (#937), so the admission gate and the next file load obey it.
func (tx *CommitTx) SetQuarantined(name string, quarantined bool) error {
	return tx.UpdateServer(name, func(cur *config.ServerConfig) error {
		cur.Quarantined = quarantined
		cur.MarkQuarantineExplicitlySet(true)
		return nil
	})
}

// RemoveServer removes a server. Unknown names fail with *ServerNotFoundError.
func (tx *CommitTx) RemoveServer(name string) error {
	i := tx.entryIndex(name)
	if i < 0 {
		return &ServerNotFoundError{Name: name}
	}
	tx.next.Servers = append(tx.next.Servers[:i:i], tx.next.Servers[i+1:]...)
	switch {
	case tx.created[name]:
		delete(tx.created, name)
	case tx.replaced[name]:
		delete(tx.replaced, name)
		tx.removed[name] = true
	default:
		tx.removed[name] = true
	}
	return nil
}

// ReplaceConfig makes cfg the new configuration. cfg must be a configuration
// the caller submitted in this call, or the file read inside the commit
// (D14); never a snapshot captured earlier. It cannot express a same-name
// replacement: an entry that keeps its name is an update.
func (tx *CommitTx) ReplaceConfig(cfg *config.Config) {
	tx.next = cloneCommitConfig(cfg)
	tx.reconcile = true
	tx.replacedConfig = true
	tx.created = map[string]bool{}
	tx.removed = map[string]bool{}
	tx.replaced = map[string]bool{}
	tx.quarantineDecided = nil
}

// ReconcileLive reconciles storage with the live configuration (projection
// repair). It takes no argument: the base is the config read inside the lock.
func (tx *CommitTx) ReconcileLive() { tx.reconcile = true }

// Bootstrap is for the boot reconcile only, always with ReconcileLive: it
// forces the pipeline to run and schedules a connect for every enabled server.
// Anywhere else it is an invariant error (review R9.6).
func (tx *CommitTx) Bootstrap() {
	if tx.reason != commitReasonBoot || tx.r.bootstrapped.Load() {
		tx.err = fmt.Errorf("%w: Bootstrap() outside the boot commit (reason %q)", ErrCommitInvariant, tx.reason)
		return
	}
	tx.bootstrap = true
}

// ForceDisk makes the commit write the config file even when nothing changed
// (SaveConfiguration).
func (tx *CommitTx) ForceDisk() { tx.forceDisk = true }

// MutateGlobal runs fn on the working configuration for global fields (listen,
// registries, profiles, …). Servers must be changed through the server
// operations, which keep the replacement bookkeeping.
func (tx *CommitTx) MutateGlobal(fn func(cfg *config.Config) error) error {
	before := serverNameSet(tx.next)
	if err := fn(tx.next); err != nil {
		return err
	}
	if !sameNameSet(before, serverNameSet(tx.next)) {
		return fmt.Errorf("%w: MutateGlobal must not add or remove servers", ErrCommitInvariant)
	}
	return nil
}

// ExpectGeneration fails the commit with ErrServerReplaced unless name's
// committed generation is gen.
func (tx *CommitTx) ExpectGeneration(name string, gen int64) error {
	g, ok := tx.base.ServerGeneration(name)
	if !ok || g.Gen != gen {
		return fmt.Errorf("%w: server '%s' changed since generation %d", ErrServerReplaced, name, gen)
	}
	return nil
}

// ExpectIncarnation fails the commit with ErrServerReplaced unless name's
// committed incarnation is inc.
func (tx *CommitTx) ExpectIncarnation(name string, inc int64) error {
	g, ok := tx.base.ServerGeneration(name)
	if !ok || g.Incarnation != inc {
		return serverReplacedError(name)
	}
	return nil
}

// UnderApprovalLock registers fn to run in step 4 while the commit holds
// name's approval lock (the Spec 117 review-bound approval integration
// point). fn may only write fail-safe state; an error aborts the commit with
// nothing written.
func (tx *CommitTx) UnderApprovalLock(name string, fn func() error) error {
	if tx.entry(name) == nil && !tx.baseNames[name] {
		return &ServerNotFoundError{Name: name}
	}
	tx.hooks = append(tx.hooks, approvalHook{name: name, fn: fn})
	return nil
}

// --- unexported plumbing used by the runtime's own commit callers ---

func (tx *CommitTx) noDisk()                   { tx.skipDisk = true }
func (tx *CommitTx) noWorkers()                { tx.suppressWorkers = true }
func (tx *CommitTx) setDisk(p *commitDiskPlan) { tx.disk = p }

// setPath makes p the config file path of this commit: step 5 writes it, and
// step 8 publishes it with the snapshot (r.cfgPath follows). A commit that
// fails before step 8 leaves the old path published (review round 3).
func (tx *CommitTx) setPath(p string)              { tx.path = p }
func (tx *CommitTx) setDesired(cfg *config.Config) { tx.desired = cfg }

// publishHook runs in step 8 with Runtime.mu held, after r.cfg was swapped.
func (tx *CommitTx) publishHook(fn func(oldCfg, newCfg *config.Config)) {
	tx.onPublish = append(tx.onPublish, fn)
}

// afterCommit runs after the commit lock is released, only on success.
func (tx *CommitTx) afterCommit(fn func(*CommitResult)) { tx.after = append(tx.after, fn) }

// EventExtra adds caller fields (for example {"server", "quarantined"}) to the
// commit's one servers.changed event, so a caller never sends a second,
// same-reason copy of it (FR-028: at most once per commit and reason). The
// commit's own "servers" and "generation" fields win on a clash.
func (tx *CommitTx) EventExtra(extra map[string]any) {
	if tx.eventExtra == nil {
		tx.eventExtra = make(map[string]any, len(extra))
	}
	for k, v := range extra {
		tx.eventExtra[k] = v
	}
}

const (
	commitReasonBoot = "boot"
	// commitReasonAdmissionGate is the startup admission commit
	// (gateInitialConfig), which runs before the boot commit.
	commitReasonAdmissionGate = "config_load_admission_gate"
	commitReasonFileReload    = "file_reload"
)

// commitState is the runtime's commit bookkeeping (embedded in Runtime).
type commitState struct {
	commitOwner  atomic.Int64 // goroutine id holding configCommitMu inside Commit
	bootstrapped atomic.Bool

	// telemetryMu orders the after-unlock telemetry notifications by commit
	// generation (review rounds 19-20): telemetryGen is the newest generation
	// whose configuration the telemetry service was given.
	telemetryMu  sync.Mutex
	telemetryGen int64
}

// Commit runs fn against a working copy of the live configuration while holding
// configCommitMu, then persists and publishes the result (see the pipeline
// above). An error from fn, or from any step before publication, aborts the
// commit with nothing published and storage untouched.
func (r *Runtime) Commit(reason string, fn func(tx *CommitTx) error) (*CommitResult, error) {
	if r.configSvc == nil {
		return nil, fmt.Errorf("config service not initialized")
	}
	gid := currentGoroutineID()
	if gid != 0 && r.commitOwner.Load() == gid {
		return nil, errCommitReentry
	}
	r.lockConfigCommit()
	r.commitOwner.Store(gid)
	res, after, err := r.commitLocked(reason, fn)
	r.commitOwner.Store(0)
	r.configCommitMu.Unlock()
	if err != nil {
		return nil, err
	}
	if after != nil {
		after()
	}
	return res, nil
}

func cloneCommitConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return &config.Config{}
	}
	// Every exported global field is deep-copied (review round 5): MutateGlobal
	// edits nested state (cfg.Logging.Level, a profile's servers or tool
	// rules), and with a shallow copy that edit wrote straight into the
	// published base — it leaked when the commit aborted or failed, and a
	// successful one was invisible to change detection because base and next
	// moved together. Unexported fields (load diagnostics, explicit-set
	// markers) are copied by value; MutateGlobal cannot reach them.
	next := deepCopyExported(reflect.ValueOf(cfg).Elem()).Interface().(config.Config)
	next.Servers = make([]*config.ServerConfig, 0, len(cfg.Servers))
	for _, sc := range cfg.Servers {
		if sc != nil {
			next.Servers = append(next.Servers, config.CopyServerConfig(sc))
		}
	}
	return &next
}

// deepCopyExported returns a copy of v whose pointers, slices and maps reachable
// through exported fields are fresh. A struct is first copied by value (which
// carries its unexported fields as they are), then each exported field is
// replaced by its own deep copy. The configuration types hold no cycles,
// funcs, channels or locks.
func deepCopyExported(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopyExported(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopyExported(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), deepCopyExported(iter.Value()))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopyExported(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).IsExported() {
				out.Field(i).Set(deepCopyExported(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}

func serverNameSet(cfg *config.Config) map[string]bool {
	out := make(map[string]bool, len(cfg.Servers))
	for _, sc := range cfg.Servers {
		if sc != nil {
			out[sc.Name] = true
		}
	}
	return out
}

func sameNameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// diskState is the raw config file read inside the commit lock before step 5
// (review R9.2): the compensation restores exactly these bytes and mode.
type diskState struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func readDiskState(path string) (diskState, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return diskState{}, nil
	}
	if err != nil {
		return diskState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return diskState{}, err
	}
	return diskState{exists: true, data: data, mode: info.Mode().Perm()}, nil
}

func (r *Runtime) commitLocked(reason string, fn func(tx *CommitTx) error) (*CommitResult, func(), error) {
	hooks := r.testHooks
	base := r.configSvc.Current()
	tx := &CommitTx{
		r:         r,
		reason:    reason,
		base:      base,
		next:      cloneCommitConfig(base.Config),
		baseNames: serverNameSet(base.Config),
		created:   map[string]bool{},
		removed:   map[string]bool{},
		replaced:  map[string]bool{},
	}

	// Step 1: build.
	if fn != nil {
		if err := fn(tx); err != nil {
			return nil, nil, err
		}
	}
	if tx.err != nil {
		return nil, nil, tx.err
	}
	if h := hooks.afterBuild; h != nil {
		h(reason)
	}

	// The reconcile reads the stored view here, after the build.
	if tx.reconcile {
		if h := hooks.reconcileEntry; h != nil {
			h()
		}
		tx.storedLoaded = false // re-read: the reconcile works on the current rows
	}

	// Step 2: gate against the stored view.
	stored, storedErr := tx.loadStored()
	storageOK := r.storageManager != nil && storedErr == nil
	if r.storageManager != nil && storedErr != nil {
		r.logger.Error("Config commit: failed to read stored servers", zap.String("reason", reason), zap.Error(storedErr))
		// Without the stored view the projection diff cannot be computed, so
		// the storage step would be skipped while disk and the live config
		// move: the commit aborts with nothing written (review round 1).
		// Only the boot commit continues, as main's boot sync did: it writes
		// nothing to storage or disk, publishes the file it was started with
		// and schedules the connects, and the admission gate abstains (#937).
		if reason != commitReasonBoot {
			return nil, nil, fmt.Errorf("failed to read stored servers: %w", storedErr)
		}
	}
	storedCfg := make(map[string]*config.ServerConfig, len(stored))
	for name, s := range stored {
		storedCfg[name] = s.Config
	}
	if r.storageManager != nil {
		if gated, changed := r.applyConfigLoadAdmissionGate(tx.next, storedCfg, storageOK); changed {
			tx.next = gated
		}
	}

	// Step 3: prepare.
	var seed map[string]configsvc.SeedStamp
	if base.ServerGenerations == nil && storageOK {
		seed = make(map[string]configsvc.SeedStamp)
		for _, sc := range tx.next.Servers {
			if s, ok := stored[sc.Name]; ok {
				seed[sc.Name] = configsvc.SeedStamp{Stamp: s.Stamp, Matches: storage.ServerRowMatches(sc, s.Config)}
			}
		}
	}
	replaced := tx.replaced
	if len(replaced) == 0 {
		replaced = nil
	}
	updateType := configsvc.UpdateTypeModify
	if reason == commitReasonFileReload {
		updateType = configsvc.UpdateTypeReload
	}
	prepared, err := r.configSvc.Prepare(configsvc.PrepareInput{
		Config: tx.next, Source: reason, Type: updateType,
		Replaced: replaced, Seed: seed, Path: tx.path,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrCommitInvariant, err)
	}
	if h := hooks.afterPrepare; h != nil {
		h(reason)
	}
	changes := prepared.Changes
	gens := prepared.Snapshot().ServerGenerations
	configChanged := len(changes) > 0 || !configsEquivalent(base.Config, tx.next)

	// Projection diff: the rows storage must write so it equals tx.next.
	var batch *storage.ServerCommitBatch
	if r.storageManager != nil && storageOK {
		batch = &storage.ServerCommitBatch{Generation: prepared.Generation()}
		for _, sc := range tx.next.Servers {
			sg := gens[sc.Name]
			row, ok := stored[sc.Name]
			if replaced[sc.Name] || !ok || row.Stamp != sg || !storage.ServerRowMatches(sc, row.Config) {
				batch.Saves = append(batch.Saves, storage.ServerSave{Config: sc, Stamp: sg})
			}
		}
		next := serverNameSet(tx.next)
		for name := range stored {
			if !next[name] {
				batch.Removes = append(batch.Removes, name)
			}
		}
		for name := range replaced {
			batch.Replaces = append(batch.Replaces, name)
		}
		sort.Strings(batch.Removes)
		sort.Strings(batch.Replaces)
		// The boot-time empty-set guard (as in commitOrphanGC): a startup
		// commit that loads no server at all deletes the stale rows but keeps
		// their approvals and credentials, so a configuration that came up
		// empty never wipes the trust state of every server.
		batch.RowOnlyRemoves = len(tx.next.Servers) == 0 &&
			(reason == commitReasonBoot || reason == commitReasonAdmissionGate)
		batch.OAuthClears = r.commitOAuthClears(tx, changes, stored, replaced, batch.Saves)
		qe := tx.next.IsQuarantineEnabled()
		if enabled, ok, _ := r.storageManager.StoredQuarantineEnabled(); !ok || enabled != qe {
			batch.QuarantineEnabled = &qe
		}
		// Every save carries exactly the stamp Prepare computed (review R9.3).
		for _, s := range batch.Saves {
			if s.Stamp != gens[s.Config.Name] {
				return nil, nil, fmt.Errorf("%w: stamp for %q diverged from the prepared snapshot", ErrCommitInvariant, s.Config.Name)
			}
		}
	}
	// Every published generation is recorded in meta.config_commit_generation,
	// including a global-only commit with no row to write (review round 1):
	// the seed after a restart is that value, so skipping it would let the
	// next process publish a generation below this one's.
	curPath := r.configSvc.Current().Path
	pathChanged := tx.path != "" && tx.path != curPath
	storageNeeded := batch != nil && (len(batch.Saves) > 0 || len(batch.Removes) > 0 || len(batch.Replaces) > 0 ||
		batch.QuarantineEnabled != nil || configChanged || tx.bootstrap || pathChanged)

	path := curPath
	if tx.path != "" {
		path = tx.path
	}
	if tx.disk != nil && tx.disk.path != "" {
		path = tx.disk.path
	}
	// A path change writes the committed config at the new path (review
	// round 4): publishing a path whose file is missing, or holds other
	// content, would make the next reload fail or import that content.
	diskNeeded := path != "" && !tx.skipDisk && (configChanged || pathChanged || tx.forceDisk || tx.disk != nil)

	// An explicit quarantine of a server that is still quarantined purges its
	// index entries even when nothing else changed (Sol round 12): the
	// commit then takes the server's approval lock, purges and publishes
	// nothing.
	var purgeOnly []string
	for name := range tx.quarantineDecided {
		if sc := tx.entry(name); sc != nil && sc.Quarantined {
			purgeOnly = append(purgeOnly, name)
		}
	}

	// Skip rule (FR-001a): nothing to do unless something changed or was forced.
	if !configChanged && !storageNeeded && !diskNeeded && !tx.bootstrap && !pathChanged && len(tx.hooks) == 0 && len(purgeOnly) == 0 {
		if tx.reconcile {
			if h := hooks.reconcileExit; h != nil {
				h()
			}
		}
		// A reload that changes only a restart-gated value, or cancels a
		// pending one, publishes nothing but still moves the desired config
		// (review round 1): the next write must not re-persist what the file
		// no longer says.
		if tx.desired != nil {
			r.mu.Lock()
			r.desiredCfg = tx.desired
			r.mu.Unlock()
		}
		return &CommitResult{Changes: changes}, nil, nil
	}

	// Step 4: approval locks for changed names (sorted), then the hooks.
	lockSet := map[string]bool{}
	for name := range changes {
		lockSet[name] = true
	}
	for _, h := range tx.hooks {
		lockSet[h.name] = true
	}
	for _, n := range purgeOnly {
		lockSet[n] = true
	}
	if batch != nil {
		for _, n := range batch.Removes {
			lockSet[n] = true
		}
		// Every row step 6 writes takes its server's lock (review round 4,
		// #1571): a projection repair of a server the commit did not change
		// still rewrites the stored trust/quarantine policy discovery reads
		// under that lock, and a quarantine repair purges its index.
		for _, s := range batch.Saves {
			lockSet[s.Config.Name] = true
		}
		for _, n := range batch.Replaces {
			lockSet[n] = true
		}
		for _, n := range batch.OAuthClears {
			lockSet[n] = true
		}
		// FR-008c: a global approval-policy change takes every server's lock.
		if batch.QuarantineEnabled != nil {
			for _, sc := range tx.next.Servers {
				lockSet[sc.Name] = true
			}
			for name := range tx.baseNames {
				lockSet[name] = true
			}
		}
	}
	lockNames := make([]string, 0, len(lockSet))
	for n := range lockSet {
		lockNames = append(lockNames, n)
	}
	sort.Strings(lockNames)
	unlocks := make([]func(), 0, len(lockNames))
	releaseLocks := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
		unlocks = nil
	}
	for _, n := range lockNames {
		unlocks = append(unlocks, r.lockToolApprovals(n))
	}
	for _, h := range tx.hooks {
		if err := h.fn(); err != nil {
			releaseLocks()
			return nil, nil, err
		}
	}

	// Step 5: disk.
	var prevDisk diskState
	diskWritten := false
	var forgetWrite func()
	if diskNeeded {
		prevDisk, err = readDiskState(path)
		if err != nil {
			releaseLocks()
			return nil, nil, fmt.Errorf("failed to read config file before saving: %w", err)
		}
		if fault := hooks.diskWriteFault; fault != nil {
			if ferr := fault(reason); ferr != nil {
				releaseLocks()
				return nil, nil, fmt.Errorf("failed to save configuration: %w", ferr)
			}
		}
		forgetWrite, err = r.writeCommitDisk(tx, path)
		if err != nil {
			releaseLocks()
			return nil, nil, fmt.Errorf("failed to save configuration: %w", err)
		}
		diskWritten = true
	}
	if h := hooks.afterDisk; h != nil {
		h(reason)
	}

	// Step 6: storage (with the removal purge, Spec 116 PR2), then index
	// purge, then the in-memory purge, all under the approval locks.
	var purgeReports map[string]storage.PurgeReport
	if storageNeeded {
		serr := error(nil)
		if fault := hooks.storageFault; fault != nil {
			serr = fault(reason)
		}
		if serr == nil {
			purgeReports, serr = r.storageManager.ApplyServerCommitWithReport(batch)
		}
		if serr != nil {
			if diskWritten {
				r.compensateDisk(path, prevDisk, forgetWrite)
			}
			releaseLocks()
			return nil, nil, fmt.Errorf("failed to commit server configuration to storage: %w", serr)
		}
	}
	purged := r.commitIndexPurge(tx, changes, batch, stored)
	r.commitMemoryPurgeHeld(purgeReports)
	if tx.reconcile || len(tx.removed) > 0 || len(tx.replaced) > 0 || (batch != nil && len(batch.Removes) > 0) {
		r.commitOrphanGC(tx)
	}
	releaseLocks()
	if tx.reconcile {
		if h := hooks.reconcileExit; h != nil {
			h()
		}
	}
	if h := hooks.afterStorage; h != nil {
		h(reason)
	}

	// The restart-required-only apply: disk written, nothing hot changed.
	publish := configChanged || storageNeeded || tx.bootstrap || pathChanged
	if !publish {
		if tx.desired != nil {
			r.mu.Lock()
			r.desiredCfg = tx.desired
			r.mu.Unlock()
		}
		res := &CommitResult{Changes: changes, DiskWritten: diskWritten, Purged: purgeReports}
		return res, r.afterCommitFunc(tx, res, nil, purged), nil
	}

	// Step 7: advance the manager (PR1: restamp retained instances).
	var restamped []string
	if r.upstreamManager != nil && !hooks.skipAdvance {
		var reqs []upstream.RestampRequest
		for name, ch := range changes {
			if ch.Kind != configsvc.ChangeUpdated || ch.Reconnect {
				continue
			}
			if sc := tx.entry(name); sc != nil {
				reqs = append(reqs, upstream.RestampRequest{Name: name, Config: sc, Gen: ch.New.Gen})
			}
		}
		sort.Slice(reqs, func(i, j int) bool { return reqs[i].Name < reqs[j].Name })
		restamped = r.upstreamManager.RestampRetained(reqs)
	}
	if h := hooks.afterAdvance; h != nil {
		h(reason)
	}

	// Step 8: publish.
	snap, perr := r.configSvc.Publish(prepared)
	if perr != nil {
		r.logger.Error("Config commit: publish failed after storage committed (invariant violation)",
			zap.String("reason", reason), zap.Error(perr))
		return nil, nil, fmt.Errorf("%w: %v", ErrCommitInvariant, perr)
	}
	r.mu.Lock()
	oldCfg := r.cfg
	r.cfg = tx.next
	if tx.desired != nil {
		r.desiredCfg = tx.desired
	} else {
		r.setDesiredLocked(tx.next)
	}
	if snap.Path != "" && snap.Path != r.cfgPath {
		r.cfgPath = snap.Path
	}
	for _, fn := range tx.onPublish {
		fn(oldCfg, tx.next)
	}
	r.mu.Unlock()
	if r.upstreamManager != nil {
		r.upstreamManager.SetGlobalConfig(tx.next)
	}
	if tx.bootstrap {
		r.bootstrapped.Store(true)
	}

	res := &CommitResult{
		Generation:  snap.Version,
		Snapshot:    snap,
		Changes:     changes,
		Published:   true,
		DiskWritten: diskWritten,
		Restamped:   restamped,
		Purged:      purgeReports,
	}
	return res, r.afterCommitFunc(tx, res, snap, purged), nil
}

// writeCommitDisk is step 5: the pending-aware write of the new config (or the
// caller's disk plan) with its self-write marker. It returns a function that
// forgets the marker if the commit is later compensated.
func (r *Runtime) writeCommitDisk(tx *CommitTx, path string) (func(), error) {
	if tx.disk != nil {
		cfg, mergeBase := tx.disk.cfg, tx.disk.mergeBase
		r.noteConfigSelfWriteWithEdits(cfg, mergeBase, path)
		if err := config.SaveConfigWithEdits(cfg, mergeBase, path); err != nil {
			r.forgetConfigSelfWriteWithEdits(cfg, mergeBase, path)
			return nil, err
		}
		return func() { r.forgetConfigSelfWriteWithEdits(cfg, mergeBase, path) }, nil
	}
	diskCfg := tx.next
	if pending := r.pendingAwareDiskConfig(tx.next); pending != nil {
		diskCfg = pending
	}
	r.noteConfigSelfWrite(diskCfg, path)
	if err := config.SaveConfig(diskCfg, path); err != nil {
		r.forgetConfigSelfWrite(diskCfg, path)
		return nil, err
	}
	return func() { r.forgetConfigSelfWrite(diskCfg, path) }, nil
}

// compensateDisk restores the config file captured before step 5 after the
// storage step failed (FR-001c, reviews R5.5, R9.2): the same bytes and mode,
// or no file when there was none. desiredCfg and the pending-restart report
// are left alone; the restored bytes get a self-write marker so the watcher
// does not re-run a failed reload in a loop.
func (r *Runtime) compensateDisk(path string, prev diskState, forgetWrite func()) {
	if forgetWrite != nil {
		forgetWrite()
	}
	var err error
	if prev.exists {
		r.noteConfigSelfWriteBytes(prev.data)
		err = config.RestoreConfigFileBytes(path, prev.data, prev.mode)
	} else {
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	if err != nil {
		// A2: disk stays ahead of the live config until the next commit.
		r.logger.Error("Config commit: failed to restore the config file after a storage failure",
			zap.String("path", path), zap.Error(err))
	}
}

// commitIndexPurge removes from the search index every server the commit
// removed, newly quarantined (FR-008, B1) or disabled. It runs in step 6 under
// the approval locks, so a discovery pass either finished before (and is
// purged) or re-checks the stored row after and writes nothing (D15). The
// per-profile rebuild runs after the commit lock is released.
//
// Security (issue #1061): this is not an optimisation, it is the control.
// Quarantine exists to keep an unreviewed server's tool DESCRIPTIONS away from
// the agent, because that is where a Tool Poisoning Attack payload lives, and
// the description-bearing branch of the search path has no query-time
// quarantine filter — absence from the index IS the enforcement. Running it in
// the commit makes it cover every path that can set the flag (REST, MCP
// quarantine_security, a config file edit, the admission gate). Failure is
// logged and swallowed: the server is quarantined either way, and the boot GC
// removes index entries of unconfigured servers.
func (r *Runtime) commitIndexPurge(tx *CommitTx, changes map[string]ServerChange, batch *storage.ServerCommitBatch, stored map[string]*storage.StoredServer) []string {
	if r.indexManager == nil {
		return nil
	}
	targets := map[string]bool{}
	// Relative to the stored view too, as the reconcile always did (#1061): a
	// quarantined entry whose row was not quarantined, or that has no row at
	// all, is purged. In a consistent install this equals the change-based
	// rule below; it also covers a stored view that drifted from the config.
	for _, sc := range tx.next.Servers {
		if !sc.Quarantined {
			continue
		}
		if row, ok := stored[sc.Name]; !ok || row.Config == nil || !row.Config.Quarantined {
			targets[sc.Name] = true
		}
	}
	// An explicit quarantine made in this commit purges too, even when the
	// entry was already quarantined, implicitly by the admission gate (#1561)
	// or explicitly by an earlier decision (Sol round 12), and even when the
	// commit changes nothing else: a REST PATCH {"quarantined":true}, the
	// quarantine endpoints and MCP quarantine_server are the operator's
	// quarantine and must leave no description indexed. A whole-config
	// replacement (ApplyConfig, a reload) is not such a decision: a JSON round
	// trip marks every present flag explicit.
	for name := range tx.quarantineDecided {
		if sc := tx.entry(name); sc != nil && sc.Quarantined {
			targets[name] = true
		}
	}
	for name, ch := range changes {
		switch ch.Kind {
		case configsvc.ChangeRemoved, configsvc.ChangeReplaced:
			targets[name] = true
		default:
			if ch.QuarantineRaised {
				targets[name] = true
			}
			if sc := tx.entry(name); sc != nil && !sc.Enabled {
				if prev := baseEntry(tx.base.Config, name); prev == nil || prev.Enabled {
					targets[name] = true
				}
			}
		}
	}
	if batch != nil {
		for _, n := range batch.Removes {
			targets[n] = true
		}
	}
	names := make([]string, 0, len(targets))
	for n := range targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := r.indexManager.DeleteServerTools(name); err != nil {
			r.logger.Warn("Config commit: failed to purge server tools from the index",
				zap.String("server", name), zap.Error(err))
			continue
		}
		r.logger.Info("Removed server tools from index (config commit)", zap.String("server", name))
	}
	return names
}

func baseEntry(cfg *config.Config, name string) *config.ServerConfig {
	if cfg == nil {
		return nil
	}
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

// commitOAuthClears returns the servers whose cached OAuth state (tokens and
// DCR client credentials) the storage step deletes in the row transaction
// (data-model §1, review round 3), because their OAuth configuration changed:
//
//   - relative to the base snapshot (an edit through any writer), and
//   - relative to the stored row (as main's reconcile did): an OAuth-only file
//     edit made while the daemon was stopped is already in the boot commit's
//     base, so only the stored row still holds the old OAuth config.
//
// Only names whose row this commit writes qualify; a replaced name is a new
// server whose whole purge set the storage step deletes. The result is sorted.
func (r *Runtime) commitOAuthClears(tx *CommitTx, changes map[string]ServerChange, stored map[string]*storage.StoredServer, replaced map[string]bool, saves []storage.ServerSave) []string {
	var names []string
	for _, s := range saves {
		name := s.Config.Name
		if replaced[name] {
			continue
		}
		next := s.Config
		clear := false
		if ch, ok := changes[name]; ok && ch.Kind == configsvc.ChangeUpdated {
			if prev := baseEntry(tx.base.Config, name); prev != nil && config.OAuthConfigChanged(prev.OAuth, next.OAuth) {
				clear = true
			}
		}
		if row, ok := stored[name]; ok && row.Config != nil && config.OAuthConfigChanged(row.Config.OAuth, next.OAuth) {
			clear = true
		}
		if clear {
			r.logger.Info("OAuth config changed, clearing cached OAuth state in the config commit", zap.String("server", name))
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// commitOrphanGC drops tool-approval records and call history of servers that
// are not in the new configuration (MCP-1002, #1176), using the commit's
// working copy (FR-005). The empty-set guard applies only at startup: the
// admission commit and the boot reconcile (review round 8: the admission
// commit runs first, so guarding only the boot commit pruned every approval
// before the guard was reached).
func (r *Runtime) commitOrphanGC(tx *CommitTx) {
	if r.storageManager == nil {
		return
	}
	names := make([]string, 0, len(tx.next.Servers))
	for _, sc := range tx.next.Servers {
		names = append(names, sc.Name)
	}
	if len(names) == 0 && (tx.reason == commitReasonBoot || tx.reason == commitReasonAdmissionGate) {
		return
	}
	if pruned, err := r.storageManager.PruneOrphanToolApprovals(names); err != nil {
		r.logger.Warn("Failed to prune orphan tool approvals", zap.Error(err))
	} else if pruned > 0 {
		r.logger.Info("Pruned orphan tool-approval records", zap.Int("removed", pruned))
	}
	if pruned, err := r.storageManager.PruneOrphanToolCalls(names); err != nil {
		r.logger.Warn("Failed to prune orphan tool-call history", zap.Error(err))
	} else if pruned > 0 {
		r.logger.Info("Pruned orphan tool-call history", zap.Int("servers_removed", pruned))
	}
}

// afterCommitFunc builds the work that runs after the commit lock is released:
// manager removals, connect workers, discovery for restamped instances, the
// per-profile index rebuild, events and telemetry (FR-028: at most once per
// commit).
func (r *Runtime) afterCommitFunc(tx *CommitTx, res *CommitResult, snap *configsvc.Snapshot, purged []string) func() {
	return func() {
		for _, name := range purged {
			r.reindexAffectedProfiles(name)
		}
		r.afterServerPurge(res.Purged)
		if res.Published {
			r.runCommitWorkers(tx, res, snap)
			r.notifyTelemetryInOrder(res.Generation, tx.next)
			if len(res.Changes) > 0 {
				names := make([]string, 0, len(res.Changes))
				for n := range res.Changes {
					names = append(names, n)
				}
				sort.Strings(names)
				extra := make(map[string]any, len(tx.eventExtra)+2)
				for k, v := range tx.eventExtra {
					extra[k] = v
				}
				extra["servers"], extra["generation"] = names, res.Generation
				r.emitServersChanged(tx.reason, extra)
			}
			for name, ch := range res.Changes {
				prev, next := baseEntry(tx.base.Config, name), tx.entry(name)
				quarantineMoved := ch.QuarantineRaised || (prev != nil && next != nil && prev.Quarantined != next.Quarantined)
				if quarantineMoved || ch.Kind == configsvc.ChangeRemoved || ch.Kind == configsvc.ChangeReplaced {
					r.emitReviewChanged(name)
				}
			}
		}
		if res.DiskWritten {
			r.emitConfigSaved(r.configSvc.Current().Path)
		}
		for _, fn := range tx.after {
			fn(res)
		}
	}
}

// notifyTelemetryInOrder hands cfg, published at generation gen, to the
// telemetry service unless a newer commit's configuration was already handed
// over. The notification runs after the commit lock is released, so a commit
// parked in its follow-up work (a profile rebuild) can reach it after a newer
// commit; installing its older configuration then would undo the newer
// commit's opt-out (review rounds 19-20). The newer commit's configuration
// already includes everything the older one published.
func (r *Runtime) notifyTelemetryInOrder(gen int64, cfg *config.Config) {
	if r.telemetryService == nil {
		return
	}
	r.telemetryMu.Lock()
	defer r.telemetryMu.Unlock()
	if gen <= r.telemetryGen {
		return
	}
	r.telemetryGen = gen
	r.telemetryService.NotifyConfigChanged(cfg)
}

// runCommitWorkers is the after-unlock scheduling (T025). Removed and replaced
// servers are unregistered synchronously, so a following commit's worker can
// never be undone by this one's removal. Created, replaced and reconnecting
// servers get a connect (enabled) or disconnect (disabled) worker; the boot
// commit schedules every server. Restamped connected servers get one
// discovery pass (review R9.1). PR3 binds the workers to their generation.
func (r *Runtime) runCommitWorkers(tx *CommitTx, res *CommitResult, snap *configsvc.Snapshot) {
	if r.upstreamManager == nil || tx.suppressWorkers {
		return
	}
	for name, ch := range res.Changes {
		if ch.Kind == configsvc.ChangeRemoved || ch.Kind == configsvc.ChangeReplaced {
			r.upstreamManager.RemoveServer(name)
		}
	}
	schedule := map[string]bool{}
	for name, ch := range res.Changes {
		if ch.Kind != configsvc.ChangeRemoved && ch.Reconnect {
			schedule[name] = true
		}
	}
	if tx.bootstrap {
		for _, sc := range tx.next.Servers {
			schedule[sc.Name] = true
		}
	}
	cfgPath := ""
	if snap != nil {
		cfgPath = snap.Path
	}
	for _, sc := range tx.next.Servers {
		if !schedule[sc.Name] {
			continue
		}
		sg, _ := snap.ServerGeneration(sc.Name)
		if sc.Enabled {
			go func(cfg *config.ServerConfig, gen, inc int64) {
				if err := r.upstreamManager.AddServerAt(cfg.Name, cfg, gen); err != nil {
					r.logger.Error("Failed to add/update upstream server", zap.Error(err), zap.String("server", cfg.Name))
					return
				}
				if r.storageManager != nil {
					if h := r.testHooks.beforeRegisterIdentity; h != nil {
						h(cfg.Name)
					}
					// Incarnation-conditional (FR-014c): a worker that
					// outlives a removal registers nothing for the
					// removed server or its replacement.
					if _, written, err := r.storageManager.RegisterServerIdentityIfIncarnation(cfg, cfgPath, inc); err != nil {
						r.logger.Warn("Failed to register server identity", zap.Error(err), zap.String("server", cfg.Name))
					} else if !written {
						r.logger.Debug("Server identity not registered: the server was removed or replaced",
							zap.String("server", cfg.Name), zap.Int64("incarnation", inc))
					}
				}
			}(sc, sg.Gen, sg.Incarnation)
		} else {
			go func(name string) {
				r.upstreamManager.RemoveServer(name)
			}(sc.Name)
		}
	}
	ctx := r.AppContext()
	for _, name := range res.Restamped {
		if c, ok := r.upstreamManager.GetClient(name); ok && c != nil && c.IsConnected() && ctx != nil {
			go func(name string) {
				if err := r.DiscoverAndIndexToolsForServer(ctx, name); err != nil {
					r.logger.Debug("Discovery after a retained-instance restamp failed", zap.String("server", name), zap.Error(err))
				}
			}(name)
		}
	}
}

// currentGoroutineID parses the running goroutine's id from its stack header.
// Used only to turn a re-entrant Commit into an error instead of a deadlock.
func currentGoroutineID() int64 {
	var buf [64]byte
	n := goruntime.Stack(buf[:], false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseInt(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
