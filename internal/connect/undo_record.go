package connect

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Write provenance for Undo (Spec 132 PR-3). Replaying a connect cannot tell
// the auth modes apart (a browser-login entry edited to the plain endpoint is
// byte-equal to a keyless key entry), so every connect records the SHA-256 of
// the file exactly as it wrote it, bound to the client and entry name, in a
// small sidecar beside the backup. Undo then treats the file as untouched if
// and only if its digest equals the recorded one.
//
//   - with a backup `<config>.bak.<ts>` the record is `<config>.undo.<ts>`;
//   - when the connect created the file there is no backup, and the record is
//     `<config>.undo.new`.
//
// The sidecar has the config's permissions. Like the backup, nothing prunes it.
// A backup made before this record existed has no sidecar and is undone by the
// original key-path replay.

const undoRecordVersion = 1

// undoNewPrefix starts the name of a record of a connect that created the file:
// `<config>.undo.new.<random>`. Connect holds no lock around read, backup and
// write for a browser-login connect (nor does the key path across its initial
// read; the key path's only guard is the minter's per-client claim, which a
// browser-login connect does not take because it mints nothing), so two
// concurrent first-time connects can both find no file. Each keeps its own
// record, so neither overwrites the other's; whichever wrote the file last is
// the one whose digest matches, and a file that matches none of them is a
// conflict. The fail-closed outcome is the limit: Undo never restores over a
// file it cannot account for.
const undoNewPrefix = ".undo.new."

type undoRecord struct {
	Version int    `json:"version"`
	Client  string `json:"client"`
	Server  string `json:"server"`
	// Backup is the backup's base name, "" when the connect created the file.
	Backup string `json:"backup"`
	// Auth is "oauth" or "key".
	Auth   string `json:"auth"`
	SHA256 string `json:"sha256"`
	// Gen is the generation that was current after this connect (see
	// undo_state.go). A created-file record authorises an Undo only while the
	// state file still names it.
	Gen string `json:"gen,omitempty"`
}

// undoRecordPath is where the record of the write that took backupPath lives.
// A connect that created the file has no backup, so its record name carries a
// random suffix instead (see undoNewPrefix); the caller supplies it.
func undoRecordPath(cfgPath, backupPath string) string {
	if backupPath == "" {
		return cfgPath + undoNewPrefix
	}
	base := filepath.Base(backupPath)
	if i := strings.LastIndex(base, ".bak."); i >= 0 {
		return filepath.Join(filepath.Dir(backupPath), base[:i]+".undo."+base[i+len(".bak."):])
	}
	return backupPath + ".undo"
}

// fileDigest is the SHA-256 of a whole config file, in hex. It is a change
// detector for the file a connect wrote, not a way to store a credential.
func fileDigest(b []byte) string {
	h := sha256.New()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// stagedRecord is a record written to a temp file but not yet published. A
// connect stages it before it writes the config and publishes it only after the
// config write succeeded, so a connect that fails leaves every older record as
// it was.
type stagedRecord struct {
	tmp, final string
}

// stageUndoRecord prepares the record of the connect that is about to write
// `written`. It creates the directory if needed and writes the complete record
// into an exclusively created temp file beside its final name; any failure
// removes the temp file.
func stageUndoRecord(cfgPath, backupPath, clientID, serverName, gen string, written []byte, perm os.FileMode) (*stagedRecord, error) {
	rec := undoRecord{Version: undoRecordVersion, Client: clientID, Server: serverName, Auth: AuthOAuth, SHA256: fileDigest(written), Gen: gen}
	if backupPath != "" {
		rec.Backup = filepath.Base(backupPath)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	path := undoRecordPath(cfgPath, backupPath)
	if backupPath == "" {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, fmt.Errorf("undo record name: %w", err)
		}
		path = cfgPath + undoNewPrefix + hex.EncodeToString(rnd[:])
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, newDirPerm); err != nil {
		return nil, fmt.Errorf("create directory for undo record: %w", err)
	}
	tmpName, err := stageSidecar(cfgPath, dir, raw, perm, recordWrite, recordSync)
	if err != nil {
		return nil, fmt.Errorf("write undo record: %w", err)
	}
	return &stagedRecord{tmp: tmpName, final: path}, nil
}

// discard removes an unpublished record (nil-safe).
func (r *stagedRecord) discard() {
	if r != nil {
		_ = os.Remove(r.tmp)
	}
}

// publish renames the staged record into place (nil-safe, "" for nil).
func (r *stagedRecord) publish() (string, error) {
	if r == nil {
		return "", nil
	}
	if err := os.Rename(r.tmp, r.final); err != nil {
		_ = os.Remove(r.tmp)
		return "", fmt.Errorf("publish undo record: %w", err)
	}
	if err := dirSync(filepath.Dir(r.final)); err != nil {
		return r.final, fmt.Errorf("sync directory after publishing the undo record: %w", err)
	}
	return r.final, nil
}

// finishUndoRecords runs right after a successful config write: it commits the
// new generation (which at once supersedes every older created-file record),
// publishes the staged record (browser-login connects) and then retires the
// older records. None of it can fail the connect, which has already happened: a
// problem is returned as a note for the result. A record that outlives its
// retirement, a failed publish or a failed commit cannot authorise anything: the
// state is pending (fail closed) or names another generation.
func finishUndoRecords(staged *stagedRecord, tx *configTx, gen, cfgPath, backupPath, clientID, serverName string, strict bool) string {
	var notes []string
	if tx == nil {
		// A state file that appeared while an API-key connect wrote is advanced too.
		var lerr error
		if tx, lerr = beginConfigTx(cfgPath, false); lerr != nil {
			notes = append(notes, "undo state: "+lerr.Error())
		}
	}
	var cerr error
	if gen != "" {
		cerr = tx.commit(gen)
	} else {
		cerr = tx.commitNew()
	}
	if cerr != nil {
		notes = append(notes, "undo state not updated, Undo will refuse older records: "+cerr.Error())
	}
	keep, err := staged.publish()
	if err != nil {
		notes = append(notes, err.Error())
	}
	if err := retireStaleUndoRecords(cfgPath, backupPath, clientID, serverName, keep, strict); err != nil {
		notes = append(notes, err.Error())
	}
	return strings.Join(notes, "; ")
}

// undoVerdict is what the record says about the current file.
type undoVerdict int

const (
	undoLegacy    undoVerdict = iota // no record: a backup from before records existed
	undoUntouched                    // the file is exactly what the connect wrote
	undoConflict                     // edited, or the record cannot be trusted
	undoForeign                      // a created-file record of another client or entry
)

// checkUndoRecord compares the current file with the record of the connect
// that took backupPath. A record that is unreadable, malformed or bound to
// another client, entry or backup fails closed.
//
// The verdict also names the record it matched, for cleanup. A connect that
// created the file may own any of several records (see undoNewPrefix): the file
// is untouched if it matches one of them.
func checkUndoRecord(cfgPath, backupPath, clientID, serverName string, current []byte) (undoVerdict, string, string) {
	paths := []string{undoRecordPath(cfgPath, backupPath)}
	if backupPath == "" {
		var err error
		paths, err = recordFilesWithPrefix(cfgPath, undoNewPrefix)
		if err != nil {
			// Records may exist that cannot be seen: never fall back to the key
			// replay, which could accept an edited browser-login file.
			return undoConflict, "", ""
		}
		if len(paths) == 0 {
			return undoLegacy, "", ""
		}
	}
	gate := stateGate{}
	if backupPath == "" {
		gate = readStateGate(cfgPath)
	}
	verdict, auth, matched := undoLegacy, "", ""
	for _, p := range paths {
		v, a := checkUndoRecordFile(p, backupPath, clientID, serverName, current, gate)
		if v == undoForeign {
			continue // another client's or entry's record says nothing about this file
		}
		switch v {
		case undoUntouched:
			return v, a, p
		case undoConflict:
			verdict = undoConflict
		case undoLegacy, undoForeign:
		}
	}
	return verdict, auth, matched
}

// stateGate is what the per-config state allows created-file records to do.
type stateGate struct {
	ok      bool   // readable, well formed and not pending
	current string // the generation a record must carry
}

func readStateGate(cfgPath string) stateGate {
	raw, exists, err := readUndoStateRaw(cfgPath)
	if err != nil || !exists {
		return stateGate{}
	}
	st, ok := parseUndoState(raw)
	if !ok || st.Pending || st.Current == "" {
		return stateGate{}
	}
	return stateGate{ok: true, current: st.Current}
}

func checkUndoRecordFile(path, backupPath, clientID, serverName string, current []byte, gate stateGate) (undoVerdict, string) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return undoLegacy, ""
	}
	if err != nil {
		return undoConflict, ""
	}
	var rec undoRecord
	if json.Unmarshal(raw, &rec) != nil || rec.Version != undoRecordVersion || len(rec.SHA256) != sha256.Size*2 {
		return undoConflict, ""
	}
	wantBackup := ""
	if backupPath != "" {
		wantBackup = filepath.Base(backupPath)
	}
	if rec.Client != clientID || rec.Server != serverName {
		if backupPath == "" {
			return undoForeign, ""
		}
		return undoConflict, ""
	}
	if rec.Backup != wantBackup {
		return undoConflict, ""
	}
	// A created-file record counts only for the generation the state names; a
	// missing, unreadable, malformed or pending state, or another generation,
	// fails closed.
	if backupPath == "" && (!gate.ok || rec.Gen != gate.current) {
		return undoConflict, ""
	}
	if subtle.ConstantTimeCompare([]byte(rec.SHA256), []byte(fileDigest(current))) != 1 {
		return undoConflict, ""
	}
	return undoUntouched, rec.Auth
}

// removeUndoRecord deletes one record, best effort ("" is a no-op).
func removeUndoRecord(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// recordFilesWithPrefix lists the files beside cfgPath named
// `<config basename><suffix>…`. It reads the literal directory: cfgPath is never
// used as a glob pattern, so a home directory containing [ ] * ? hides nothing.
//
// Only "the directory does not exist" means there are no records; any other
// failure to list it is returned, so a caller can fail closed.
func recordFilesWithPrefix(cfgPath, suffix string) ([]string, error) {
	dir, prefix := filepath.Dir(cfgPath), filepath.Base(cfgPath)+suffix
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, &listError{err}
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// listError marks a failure to enumerate the config's directory, as opposed to
// a failure to remove a record that was seen.
type listError struct{ error }

func (e *listError) Unwrap() error { return e.error }

// inspectRecord reports whether a record file exists at path and whether it is
// bound to clientID's entry serverName. A record that is malformed counts as
// existing and not belonging to the caller. Only "does not exist" is absence.
func inspectRecord(path, clientID, serverName string) (exists, belongs bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var rec undoRecord
	if json.Unmarshal(raw, &rec) != nil {
		return true, false, nil
	}
	return true, rec.Client == clientID && rec.Server == serverName, nil
}

// removeCreatedFileRecords deletes every created-file record (see undoNewPrefix)
// of clientID's entry serverName beside cfgPath. It reports a record it could
// not remove; with no such record it reads the directory and writes nothing.
func removeCreatedFileRecords(cfgPath, clientID, serverName, keep string) error {
	files, err := recordFilesWithPrefix(cfgPath, undoNewPrefix)
	if err != nil {
		return err
	}
	var firstErr error
	for _, m := range files {
		if m == keep {
			continue
		}
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var rec undoRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Client == clientID && rec.Server == serverName {
			if err := removeRecordFile(m); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// retireStaleUndoRecords runs for EVERY connect that writes the config, key
// connects included, before it writes: a record describes one file, and this
// write replaces (or creates) that file. It removes the created-file records of
// the entry and, when the connect took a backup, any record already sitting at
// that backup's record name (the name of a deleted backup can be reused). It
// writes nothing, and with no record present is a directory read only, so an
// install that never used browser login is untouched. A record that cannot be
// removed fails the connect: a stale record must never survive to be matched.
//
// strict is true for a browser-login connect, which must be able to see the
// directory before it publishes a record. An API-key connect is not strict: if
// the directory cannot be listed there is nothing it could retire that it can
// see, so it skips retirement and proceeds exactly as it did before records
// existed (it must never newly fail on an install that never used them).
func retireStaleUndoRecords(cfgPath, backupPath, clientID, serverName, keep string, strict bool) error {
	var le *listError
	if err := removeCreatedFileRecords(cfgPath, clientID, serverName, keep); err != nil {
		if !errors.As(err, &le) || strict {
			return fmt.Errorf("retire stale undo record: %w", err)
		}
	}
	if backupPath != "" {
		rp := undoRecordPath(cfgPath, backupPath)
		exists, belongs, err := inspectRecord(rp, clientID, serverName)
		switch {
		case rp == keep:
			// the record just published
		case err != nil && strict:
			return fmt.Errorf("retire stale undo record: %w", err)
		case err != nil, !exists:
			// nothing visible to retire
		case !belongs:
			// backupFileAvoiding never hands out a name with a foreign record.
			return fmt.Errorf("retire stale undo record: %s belongs to another entry", filepath.Base(rp))
		default:
			if err := removeRecordFile(rp); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("retire stale undo record: %w", err)
			}
		}
	}
	return nil
}

// backupNameTaken is the predicate for backupFileAvoiding: a candidate backup
// name is taken when a record already sits at its record name and belongs to
// another client or entry (or cannot be told). A record of this very entry is
// stale and is retired instead. Only a strict (browser-login) connect treats an
// unreadable record as taken.
func backupNameTaken(cfgPath, clientID, serverName string, strict bool) func(string) bool {
	return func(candidate string) bool {
		exists, belongs, err := inspectRecord(undoRecordPath(cfgPath, candidate), clientID, serverName)
		if err != nil {
			return strict
		}
		return exists && !belongs
	}
}

// removeUndoRecords deletes the records of clientID's entry serverName beside
// cfgPath once the entry is gone (Disconnect), so no stray file outlives it.
// Records of other clients or entries, and anything unreadable, are left alone.
func removeUndoRecords(cfgPath, clientID, serverName string) error {
	files, err := recordFilesWithPrefix(cfgPath, ".undo.")
	if err != nil {
		return err
	}
	var firstErr error
	for _, m := range files {
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var rec undoRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Client == clientID && rec.Server == serverName {
			if err := removeRecordFile(m); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// recordWrite writes a record's bytes into its staging file. A seam, as the
// HMAC key code has, so a test can make the write fail half way.
var recordWrite = defaultRecordWrite

func defaultRecordWrite(f *os.File, b []byte) error {
	_, err := f.Write(b)
	return err
}

// removeRecordFile removes a retired record. A seam, so a test can make a
// retirement fail the way a sticky directory or an ACL would.
var removeRecordFile = os.Remove
