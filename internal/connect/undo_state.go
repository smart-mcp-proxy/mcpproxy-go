package connect

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
//
// Every created-file record carries the generation that was current after its
// connect. It authorises an Undo only while the state file is readable, is not
// pending, and names exactly that generation.
//
// A write goes: pending=true (current kept) -> the write -> {new current,
// pending=false}. A failure before the write restores the previous state byte
// for byte. A crash, or a failed commit, leaves pending=true, so Undo fails
// closed until the next successful write sets a new generation.
//
// The state file exists only once browser login created it: an API-key connect,
// a disconnect or an undo changes it only if it already exists, so an install
// that never used browser login sees no new file. Connect has no per-config
// lock (the minter's per-client claim is its only guard, and a browser-login
// connect takes none), so two racing writers can interleave their state steps;
// the outcome is fail-closed (a pending or unequal generation is a conflict),
// never a wrongly accepted file.

const (
	undoStateSuffix  = ".undo.state"
	undoStateVersion = 1
)

type undoState struct {
	Version int    `json:"version"`
	Current string `json:"current"`
	Pending bool   `json:"pending"`
}

func undoStatePath(cfgPath string) string { return cfgPath + undoStateSuffix }

// newGeneration returns a random 128-bit hex id.
func newGeneration() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("undo generation: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
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

func parseUndoState(raw []byte) (undoState, bool) {
	var st undoState
	if json.Unmarshal(raw, &st) != nil || st.Version != undoStateVersion {
		return undoState{}, false
	}
	return st, true
}

// stateWrite writes the state bytes into their staging file; a seam for tests.
var stateWrite = defaultRecordWrite

// writeSidecar publishes raw at path through an exclusively created temp file
// in the same directory and a rename, so a failed write leaves neither a
// partial file nor the temp. write is the seam that writes the bytes.
func writeSidecar(cfgPath, path string, raw []byte, perm os.FileMode, write func(*os.File, []byte) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, newDirPerm); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	tmp, err := stageSidecar(cfgPath, dir, raw, perm, write)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

// stageSidecar writes raw into an exclusively created temp file beside the
// config and returns its name; any failure removes the temp.
func stageSidecar(cfgPath, dir string, raw []byte, perm os.FileMode, write func(*os.File, []byte) error) (string, error) {
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

// configTx is one MCPProxy write of a config file, as seen by the undo state.
// A nil *configTx means "no state to keep" and every method is a no-op.
type configTx struct {
	cfgPath  string
	hadState bool
	prevRaw  []byte
	perm     os.FileMode
}

// beginConfigTx marks the config's state pending before a write. With create
// false and no state file there is nothing to do and it returns nil (an install
// that never used browser login is left untouched). A state file that exists
// but cannot be read fails the write: older records could not be superseded.
func beginConfigTx(cfgPath string, create bool) (*configTx, error) {
	raw, exists, err := readUndoStateRaw(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("undo state: %w", err)
	}
	if !exists && !create {
		return nil, nil
	}
	t := &configTx{cfgPath: cfgPath, hadState: exists, prevRaw: raw, perm: statePerm(cfgPath)}
	cur := ""
	if exists {
		if st, ok := parseUndoState(raw); ok {
			cur = st.Current
		}
	}
	if err := t.write(undoState{Version: undoStateVersion, Current: cur, Pending: true}); err != nil {
		return nil, fmt.Errorf("undo state: %w", err)
	}
	return t, nil
}

func (t *configTx) write(st undoState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeSidecar(t.cfgPath, undoStatePath(t.cfgPath), raw, t.perm, stateWrite)
}

// rollback restores the state exactly as it was before beginConfigTx.
func (t *configTx) rollback() error {
	if t == nil {
		return nil
	}
	if !t.hadState {
		if err := os.Remove(undoStatePath(t.cfgPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeSidecar(t.cfgPath, undoStatePath(t.cfgPath), t.prevRaw, t.perm, stateWrite)
}

// commit makes gen current and ends the pending state.
func (t *configTx) commit(gen string) error {
	if t == nil {
		return nil
	}
	return t.write(undoState{Version: undoStateVersion, Current: gen})
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
		if err := os.Remove(undoStatePath(t.cfgPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return t.commitNew()
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
