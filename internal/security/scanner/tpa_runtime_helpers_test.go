package scanner

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// fakeBundleStore is an in-memory TPABundleStateStore with fault injection. It
// round-trips every value through JSON, exactly like the bbolt-backed
// implementation, so a runtime that kept a pointer into "persisted" state would
// be caught by the tests rather than by an attacker.
type fakeBundleStore struct {
	mu      sync.Mutex
	state   []byte // nil => never written
	history []TPABundleActivation

	commits int
	loads   int

	// failCommit, when non-nil, is returned by every CommitTPABundleState and
	// nothing is written.
	failCommit error
	// failLoad, when non-nil, is returned by every LoadTPABundleState.
	failLoad error
	// loadOverride, when non-nil, is returned INSTEAD of the stored state: the
	// SC-012 "attacker substituted config.db" lever.
	loadOverride *TPABundleState
}

func (s *fakeBundleStore) LoadTPABundleState() (*TPABundleState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.failLoad != nil {
		return nil, s.failLoad
	}
	if s.loadOverride != nil {
		return cloneState(s.loadOverride), nil
	}
	if s.state == nil {
		return nil, nil
	}
	var st TPABundleState
	if err := json.Unmarshal(s.state, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *fakeBundleStore) CommitTPABundleState(state *TPABundleState, entry *TPABundleActivation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCommit != nil {
		return s.failCommit
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	s.state = raw
	if entry != nil {
		s.history = append(s.history, *entry)
	}
	s.commits++
	return nil
}

func (s *fakeBundleStore) ListTPABundleHistory() ([]TPABundleActivation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TPABundleActivation(nil), s.history...), nil
}

// persisted returns the committed state (nil when never written).
func (s *fakeBundleStore) persisted(t *testing.T) *TPABundleState {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return nil
	}
	var st TPABundleState
	require.NoError(t, json.Unmarshal(s.state, &st))
	return &st
}

func (s *fakeBundleStore) commitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits
}

func (s *fakeBundleStore) historyRows() []TPABundleActivation {
	rows, _ := s.ListTPABundleHistory()
	return rows
}

// clearBundleRuntime wipes every piece of runtime state and the published
// snapshot, leaving the package as a fresh process would find it.
func clearBundleRuntime() {
	bundleRT.mu.Lock()
	bundleRT.resetLocked()
	bundleRT.mu.Unlock()
	storeBundle(nil, BundleInfo{})
	lkgAfterWriteHook = nil
	stateAfterCommitHook = nil
	bundleSelfCheck = nil
	bundleCompileHook = nil
}

// resetBundleRuntimeForTest clears the runtime now and again on cleanup,
// restoring the embedded default so no state leaks into the rest of the package.
func resetBundleRuntimeForTest(t *testing.T) {
	t.Helper()
	clearBundleRuntime()
	t.Cleanup(func() {
		clearBundleRuntime()
		ConfigureBundle("", zap.NewNop())
	})
}

// rtOpts parametrizes newRTFixture.
type rtOpts struct {
	// EmbeddedSeq is the sequence stamped into the embedded default (0 = none),
	// i.e. the publisher baseline.
	EmbeddedSeq uint64
	// Sec mutates the SecurityConfig after the defaults are applied.
	Sec func(*config.SecurityConfig)
	// NoStore runs the runtime with no state store (in-memory state only).
	NoStore bool
	// NoDataDir runs the runtime with no LKG directory.
	NoDataDir bool
}

// rtFixture is one runtime: a fake store, a data dir, a drop directory holding
// the configured bundle path, and an observed logger.
type rtFixture struct {
	t     *testing.T
	store *fakeBundleStore
	dir   string // data dir (tpa-lkg lives under it)
	drop  string // directory of the configured bundle path
	path  string // configured bundle path
	sec   *config.SecurityConfig
	logs  *observer.ObservedLogs
	log   *zap.Logger
}

func newRTFixture(t *testing.T, o rtOpts) *rtFixture {
	t.Helper()
	t.Setenv(config.EnvTPABundlePath, "")
	resetBundleRuntimeForTest(t)
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 0,
		buildBundle(t, bundleOpts{Seq: o.EmbeddedSeq, Tag: "embedded"}))

	core, logs := observer.New(zapcore.DebugLevel)
	f := &rtFixture{
		t:     t,
		store: &fakeBundleStore{},
		dir:   t.TempDir(),
		drop:  t.TempDir(),
		logs:  logs,
		log:   zap.New(core),
	}
	f.path = filepath.Join(f.drop, "scanner-bundle.json")
	f.sec = &config.SecurityConfig{TPABundlePath: f.path, TPAOperatorSigners: []string{keyOperator.b64}}
	if o.Sec != nil {
		o.Sec(f.sec)
	}
	var store TPABundleStateStore
	if !o.NoStore {
		store = f.store
	}
	dir := f.dir
	if o.NoDataDir {
		dir = ""
	}
	SetBundleRuntime(store, dir)
	return f
}

// put writes the candidate to the configured path (and its sidecar, or removes
// a stale one).
func (f *rtFixture) put(c Candidate) { f.putAt(f.path, c) }

func (f *rtFixture) putAt(path string, c Candidate) {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(path, c.Bundle, 0o600))
	if c.SidecarPresent {
		require.NoError(f.t, os.WriteFile(path+".sig", c.Sidecar, 0o600))
		return
	}
	if err := os.Remove(path + ".sig"); err != nil && !errors.Is(err, os.ErrNotExist) {
		f.t.Fatal(err)
	}
}

// remove deletes the configured bundle file and its sidecar.
func (f *rtFixture) remove() {
	f.t.Helper()
	for _, p := range []string{f.path, f.path + ".sig"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			f.t.Fatal(err)
		}
	}
}

// run applies the fixture's config through the production entry point and
// returns the published status.
func (f *rtFixture) run() BundleInfo {
	f.t.Helper()
	ConfigureBundleFromConfig(f.sec, f.log)
	return BundleStatus()
}

// restart simulates a process restart over the same store and data dir: every
// in-memory piece is dropped, the persisted state and the LKG files remain.
func (f *rtFixture) restart() {
	f.t.Helper()
	clearBundleRuntime()
	SetBundleRuntime(f.store, f.dir)
}

func (f *rtFixture) lkgFiles() []string {
	entries, err := os.ReadDir(filepath.Join(f.dir, "tpa-lkg"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(f.t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func (f *rtFixture) logMessages(level zapcore.Level) []string {
	var out []string
	for _, e := range f.logs.All() {
		if e.Level == level {
			out = append(out, e.Message)
		}
	}
	return out
}

// servingCheck is the identity of the published check (pointer), for "the
// last-known-good kept serving" assertions.
func servingCheck() *BundleCheck {
	check, _ := snapshotBundle()
	return check
}

// requireRejected asserts the last run was refused with reason while the
// previous serving bundle (identified by digest and check pointer) kept
// serving.
func (f *rtFixture) requireRejected(info BundleInfo, reason string, prevDigest string, prevCheck *BundleCheck) {
	f.t.Helper()
	require.NotNil(f.t, info.LastRejection, "a rejection must be recorded")
	require.Equal(f.t, reason, info.LastRejection.Reason, "detail: %s", info.LastRejection.Detail)
	require.False(f.t, info.LastRejection.At.IsZero())
	require.NotEmpty(f.t, info.LoadError)
	require.Contains(f.t, info.LoadError, reason)
	require.Equal(f.t, prevDigest, info.FullDigest, "last-known-good must keep serving")
	require.Same(f.t, prevCheck, servingCheck(), "the published check must be the last-known-good check")
}

// activate drops c, runs the pipeline and requires c to be the serving bundle.
func (f *rtFixture) activate(c Candidate) BundleInfo {
	f.t.Helper()
	f.put(c)
	info := f.run()
	require.Equal(f.t, digestOf(c.Bundle), info.FullDigest, "candidate must activate; load_error=%q", info.LoadError)
	require.Empty(f.t, info.LoadError)
	return info
}

// embeddedDigest is the full digest of the embedded default currently in force.
func embeddedDigest() string { return digestOf(bundledScannerBundle) }

// writeFileForTest writes data to path with owner-only permissions.
func writeFileForTest(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
