package connect

import (
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
func undoRecordPath(cfgPath, backupPath string) string {
	if backupPath == "" {
		return cfgPath + ".undo.new"
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
	if err := os.MkdirAll(filepath.Dir(path), newDirPerm); err != nil {
		return "", fmt.Errorf("create directory for undo record: %w", err)
	}
	if err := os.WriteFile(path, raw, perm.Perm()); err != nil {
		return "", fmt.Errorf("write undo record: %w", err)
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
func checkUndoRecord(cfgPath, backupPath, clientID, serverName string, current []byte) (undoVerdict, string) {
	raw, err := os.ReadFile(undoRecordPath(cfgPath, backupPath))
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
