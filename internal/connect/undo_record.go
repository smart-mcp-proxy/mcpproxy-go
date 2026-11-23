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
	"runtime"
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

func fileDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeUndoRecord records what the connect is about to write. It runs before
// the config is replaced, so a failure leaves the config untouched.
func writeUndoRecord(cfgPath, backupPath, clientID, serverName string, written []byte, perm os.FileMode) (string, error) {
	rec := undoRecord{Version: undoRecordVersion, Client: clientID, Server: serverName, Auth: AuthOAuth, SHA256: fileDigest(written)}
	if backupPath != "" {
		rec.Backup = filepath.Base(backupPath)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	path := undoRecordPath(cfgPath, backupPath)
	if backupPath == "" {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", fmt.Errorf("undo record name: %w", err)
		}
		path = cfgPath + undoNewPrefix + hex.EncodeToString(rnd[:])
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, newDirPerm); err != nil {
		return "", fmt.Errorf("create directory for undo record: %w", err)
	}
	// Stage the whole record in an exclusively created temp file beside it and
	// publish it with a rename, so a failed write or close leaves neither a
	// partial record nor the temp file.
	tmp, err := os.CreateTemp(dir, filepath.Base(cfgPath)+".undo-tmp-*")
	if err != nil {
		return "", fmt.Errorf("stage undo record: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write undo record: %w", err)
	}
	if err := recordWrite(tmp, raw); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm.Perm()); err != nil && runtime.GOOS != "windows" {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write undo record: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("publish undo record: %w", err)
	}
	return path, nil
}

// undoVerdict is what the record says about the current file.
type undoVerdict int

const (
	undoLegacy    undoVerdict = iota // no record: a backup from before records existed
	undoUntouched                    // the file is exactly what the connect wrote
	undoConflict                     // edited, or the record cannot be trusted
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
		paths, _ = filepath.Glob(cfgPath + undoNewPrefix + "*")
		if len(paths) == 0 {
			return undoLegacy, "", ""
		}
	}
	verdict, auth, matched := undoLegacy, "", ""
	for _, p := range paths {
		v, a := checkUndoRecordFile(p, backupPath, clientID, serverName, current)
		switch v {
		case undoUntouched:
			return v, a, p
		case undoConflict:
			verdict = undoConflict
		case undoLegacy:
		}
	}
	return verdict, auth, matched
}

func checkUndoRecordFile(path, backupPath, clientID, serverName string, current []byte) (undoVerdict, string) {
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
	if rec.Client != clientID || rec.Server != serverName || rec.Backup != wantBackup {
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

// removeUndoRecords deletes the records of clientID's entry serverName beside
// cfgPath once the entry is gone (Disconnect), so no stray file outlives it.
// Records of other clients or entries, and anything unreadable, are left alone.
func removeUndoRecords(cfgPath, clientID, serverName string) {
	matches, _ := filepath.Glob(cfgPath + ".undo.*")
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var rec undoRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Client == clientID && rec.Server == serverName {
			_ = os.Remove(m)
		}
	}
}

// recordWrite writes a record's bytes into its staging file. A seam, as the
// HMAC key code has, so a test can make the write fail half way.
var recordWrite = defaultRecordWrite

func defaultRecordWrite(f *os.File, b []byte) error {
	_, err := f.Write(b)
	return err
}
