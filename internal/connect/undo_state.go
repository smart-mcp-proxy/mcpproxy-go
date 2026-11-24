package connect

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

// Per-config undo state (Spec 132 PR-3).
//
// A created-file record describes the file one connect created. It must stop
// authorising an Undo the moment any later MCPProxy write replaces that file,
// whether or not the cleanup of the old record succeeds, and whether or not the
// later write left a record of its own (an API-key connect writes none). Time
// cannot order them (clocks go back and tie), so the order is a generation
// persisted in `<config>.undo.state`:
//
//	{"version":1, "current":"<random 128-bit hex>", "pending":false}
//	{"version":1, "current":"<hex or empty>", "pending":true, "pending_owner":"<random 128-bit hex>"}
//
// Every created-file record carries the generation that was current after its
// connect. It authorises an Undo only while the state file is readable, strictly
// well formed, not pending, and names exactly that generation.
//
// A write goes: pending (with the writer's owner token, `current` kept) -> the
// write -> {new current, not pending}. The state file is written through a temp
// file that is fsynced before the rename, and the directory is fsynced after it,
// so a crash cannot leave a half-written or lost transition behind a completed
// config write. A failure before the write restores the previous state byte for
// byte. A crash, or a failed commit, leaves the state pending, so Undo fails
// closed until the next successful write sets a new generation.
//
// Concurrency. Within a process every write of one config (connect of either
// auth mode, disconnect, undo) runs under a package-level mutex keyed by the
// config path (lockConfig), so the state transitions of two writers never
// interleave; it writes no file. Across processes there is no lock, so the state
// carries the owner token: commit and rollback re-read the state first and write
// only if it is still pending with the writer's own token. A writer that finds
// the state pending by someone else (a crashed writer, or a concurrent one)
// still proceeds with its config write but takes the ownership over, so the old
// owner's later commit or rollback is refused. A writer whose commit is refused
// after it already wrote the config leaves the state pending (it cannot know
// which generation describes the file), and a refused rollback changes nothing
// (it wrote no config). An API-key, disconnect or undo writer that saw no state
// file before its write looks again afterwards and advances a file that has
// appeared. What remains across processes is the window between a writer's
// re-read and its rename: a writer that loses it can overwrite the other's state
// with its own, but every resulting state is either pending or names a
// generation whose record describes that writer's own write, and a record only
// authorises a file whose whole-file digest it holds, so the outcomes are
// conflict, never a changed file accepted. The one residue is a file replaced
// by a cross-process writer inside that window and then edited back, byte for
// byte, to an older record's content before any later write: it needs two
// processes writing the same config within microseconds and a manual revert; the
// result is a refused or accepted Undo of that exact older content. No lock is
// invented for it.
//
// The config file itself is still written by atomicWriteFile, which does not
// fsync (unchanged: it is the path every connect already used). A crash that
// loses the config data but keeps a committed state leaves a file no record's
// digest matches, so Undo answers conflict.
//
// The state file exists only once browser login created it: an API-key connect,
// a disconnect or an undo changes it only if it already exists, so an install
// that never used browser login sees no new file.

const (
	undoStateSuffix  = ".undo.state"
	undoStateVersion = 1
	generationHexLen = 32 // 128 bits
)

type undoState struct {
	Version int    `json:"version"`
	Current string `json:"current"`
	Pending bool   `json:"pending"`
	Owner   string `json:"pending_owner,omitempty"`
}

func undoStatePath(cfgPath string) string { return cfgPath + undoStateSuffix }

// newGeneration returns a random 128-bit hex id.
func newGeneration() (string, error) {
	var b [generationHexLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("undo generation: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func isGeneration(s string) bool {
	if len(s) != generationHexLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// configLocks serialises the writers of one config within the process.
var configLocks sync.Map

// lockConfig takes the process-wide lock of cfgPath and returns its release. It
// is not reentrant: no path that holds it calls another that takes it. It nests
// outside the credential minter's own locks (Undo's credential revocation runs
// under it) and nothing holding a minter lock ever waits for it (the minter only
// reads configs, which takes no lock).
func lockConfig(cfgPath string) func() {
	key := filepath.Clean(cfgPath)
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	m, _ := configLocks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// readUndoStateRaw returns the state file's bytes. Only "does not exist" is
// absence; any other failure is an error.
func readUndoStateRaw(cfgPath string) (raw []byte, exists bool, err error) {
	raw, err = os.ReadFile(undoStatePath(cfgPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// parseUndoState accepts only a state with exactly the known fields, all
// present with the right types: the right version; `current` empty only while
// pending, otherwise a 128-bit hex id; a pending state names its owner (a hex
// id) and a settled one has none. Anything else is not a state.
func parseUndoState(raw []byte) (undoState, bool) {
	var r struct {
		Version *int    `json:"version"`
		Current *string `json:"current"`
		Pending *bool   `json:"pending"`
		Owner   *string `json:"pending_owner"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&r) != nil || dec.More() {
		return undoState{}, false
	}
	if r.Version == nil || *r.Version != undoStateVersion || r.Current == nil || r.Pending == nil {
		return undoState{}, false
	}
	cur := *r.Current
	if cur != "" && !isGeneration(cur) {
		return undoState{}, false
	}
	owner := ""
	if r.Owner != nil {
		owner = *r.Owner
	}
	if *r.Pending {
		if !isGeneration(owner) {
			return undoState{}, false
		}
	} else if owner != "" || cur == "" {
		return undoState{}, false
	}
	return undoState{Version: undoStateVersion, Current: cur, Pending: *r.Pending, Owner: owner}, true
}

// Seams for tests: the bytes written into a staged file, and the fsyncs.
var (
	stateWrite = defaultRecordWrite
	stateSync  = syncFile
	recordSync = syncFile
	dirSync    = syncDirectory
)

func syncFile(f *os.File) error { return f.Sync() }

// syncDirectory fsyncs a directory so a rename in it is durable. Platforms and
// filesystems that cannot sync a directory are skipped.
func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil // a directory that cannot be opened for reading cannot be synced either
		}
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

// writeSidecar publishes raw at path through an exclusively created temp file
// in the same directory (fsynced), a rename and a directory fsync, so a failed
// write leaves neither a partial file nor the temp, and a published file is
// durable. write and sync are the seams.
func writeSidecar(cfgPath, path string, raw []byte, perm os.FileMode, write func(*os.File, []byte) error, sync func(*os.File) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, newDirPerm); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	tmp, err := stageSidecar(cfgPath, dir, raw, perm, write, sync)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish: %w", err)
	}
	if err := dirSync(dir); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

// stageSidecar writes raw into an exclusively created temp file beside the
// config, fsyncs it and returns its name; any failure removes the temp.
func stageSidecar(cfgPath, dir string, raw []byte, perm os.FileMode, write func(*os.File, []byte) error, sync func(*os.File) error) (string, error) {
	tmp, err := os.CreateTemp(dir, filepath.Base(cfgPath)+".undo-tmp-*")
	if err != nil {
		return "", fmt.Errorf("stage: %w", err)
	}
	name := tmp.Name()
	if err := write(tmp, raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(perm.Perm()); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("chmod: %w", err)
	}
	if err := sync(tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("close: %w", err)
	}
	return name, nil
}

// statePerm is the mode of the config, or the private default for a new file.
func statePerm(cfgPath string) os.FileMode {
	if info, err := os.Stat(cfgPath); err == nil {
		return info.Mode().Perm()
	}
	return newFilePerm
}

// errLostOwnership: the state is no longer pending under this writer's token.
var errLostOwnership = errors.New("the undo state is owned by another writer")

// configTx is one MCPProxy write of a config file, as seen by the undo state.
// A nil *configTx means "no state to keep" and every method is a no-op.
type configTx struct {
	cfgPath  string
	hadState bool
	prevRaw  []byte
	perm     os.FileMode
	owner    string
}

// beginConfigTx marks the config's state pending, under a fresh owner token,
// before a write. With create false and no state file there is nothing to do and
// it returns nil (an install that never used browser login is left untouched). A
// state file that exists but cannot be read fails the write: older records could
// not be superseded. A state already pending under another owner is taken over.
func beginConfigTx(cfgPath string, create bool) (*configTx, error) {
	raw, exists, err := readUndoStateRaw(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("undo state: %w", err)
	}
	if !exists && !create {
		return nil, nil
	}
	owner, err := newGeneration()
	if err != nil {
		return nil, err
	}
	t := &configTx{cfgPath: cfgPath, hadState: exists, prevRaw: raw, perm: statePerm(cfgPath), owner: owner}
	cur := ""
	if exists {
		if st, ok := parseUndoState(raw); ok {
			cur = st.Current
		}
	}
	if err := t.write(undoState{Version: undoStateVersion, Current: cur, Pending: true, Owner: owner}); err != nil {
		_ = t.rollback() // the rename may have landed before a failed directory sync
		return nil, fmt.Errorf("undo state: %w", err)
	}
	return t, nil
}

func (t *configTx) write(st undoState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeSidecar(t.cfgPath, undoStatePath(t.cfgPath), raw, t.perm, stateWrite, stateSync)
}

// ownsPending re-reads the state and reports errLostOwnership unless it is still
// pending under this writer's token. exists tells a missing file from a taken-over
// one.
func (t *configTx) ownsPending() (exists bool, err error) {
	raw, exists, rerr := readUndoStateRaw(t.cfgPath)
	if rerr != nil {
		return exists, fmt.Errorf("undo state: %w", rerr)
	}
	if !exists {
		return false, errLostOwnership
	}
	st, ok := parseUndoState(raw)
	if !ok || !st.Pending || st.Owner != t.owner {
		return true, errLostOwnership
	}
	return true, nil
}

// rollback restores the state exactly as it was before beginConfigTx, but only
// while it is still this writer's pending state; otherwise it changes nothing and
// says so (this writer wrote no config, so the other writer's state stands).
func (t *configTx) rollback() error {
	if t == nil {
		return nil
	}
	if _, err := t.ownsPending(); err != nil {
		return err // another writer owns the state: nothing is changed
	}
	if !t.hadState {
		if err := os.Remove(undoStatePath(t.cfgPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeSidecar(t.cfgPath, undoStatePath(t.cfgPath), t.prevRaw, t.perm, stateWrite, stateSync)
}

// settle ends this writer's pending state with next, or, if the writer no longer
// owns it, changes nothing about it and leaves it pending: this writer already
// wrote the config and cannot know which generation describes the file, so the
// state must refuse older records.
func (t *configTx) settle(write func() error) error {
	exists, err := t.ownsPending()
	if err != nil {
		if errors.Is(err, errLostOwnership) && exists {
			_ = t.write(undoState{Version: undoStateVersion, Pending: true, Owner: t.owner})
		}
		return err
	}
	return write()
}

// commit makes gen current and ends the pending state.
func (t *configTx) commit(gen string) error {
	if t == nil {
		return nil
	}
	return t.settle(func() error { return t.write(undoState{Version: undoStateVersion, Current: gen}) })
}

// commitNew commits a freshly generated generation.
func (t *configTx) commitNew() error {
	if t == nil {
		return nil
	}
	gen, err := newGeneration()
	if err != nil {
		return err
	}
	return t.commit(gen)
}

// finishAfterRemoval ends a disconnect's or an undo's write: with no record of
// the config left the state file goes too; otherwise it moves to a new
// generation so the remaining records describe a file that no longer exists.
func (t *configTx) finishAfterRemoval() error {
	if t == nil {
		return nil
	}
	left, err := recordsOfConfig(t.cfgPath)
	if err != nil {
		return err // stays pending: fail closed
	}
	if len(left) == 0 {
		return t.settle(func() error {
			if err := os.Remove(undoStatePath(t.cfgPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return syncDirectoryOf(t.cfgPath)
		})
	}
	return t.commitNew()
}

func syncDirectoryOf(cfgPath string) error {
	if err := dirSync(filepath.Dir(cfgPath)); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

// recordsOfConfig lists every undo record beside cfgPath (not the state file).
func recordsOfConfig(cfgPath string) ([]string, error) {
	files, err := recordFilesWithPrefix(cfgPath, ".undo.")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, undoStateSuffix) {
			out = append(out, f)
		}
	}
	return out, nil
}
