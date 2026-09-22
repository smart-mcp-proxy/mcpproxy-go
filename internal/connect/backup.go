package connect

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// backupNow is the clock seam for backup naming (overridden in tests to force
// same-second collisions deterministically).
var backupNow = time.Now

// backupFile creates a timestamped backup of the given file.
// Returns the backup path, or empty string if the source file does not exist.
//
// Backup names have second granularity (<config>.bak.<YYYYMMDD-HHMMSS>), so two
// operations within one second would collide and the second would silently
// overwrite the first — destroying the very backup a later undo needs (Spec 078
// US3). On collision a numeric suffix is appended (-1, -2, …), keeping the
// timestamped name as a prefix so existing backups still sort/glob together.
//
// All failure paths wrap their OS cause with %w, so a permission denial here
// (e.g. macOS TCC App-Data) preserves fs.ErrPermission up the call chain and is
// classified into a typed *AccessError by the Connect/Disconnect boundary
// (Spec 075 FR-004, see Service.asAccessError).
func backupFile(path string) (string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil // nothing to back up
	}
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}

	src, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source for backup: %w", err)
	}
	defer src.Close()

	// Uniqueness on same-second collision: O_EXCL guarantees a fresh file, so an
	// existing backup is never truncated even under a create/create race.
	ts := backupNow().Format("20060102-150405")
	backupPath := fmt.Sprintf("%s.bak.%s", path, ts)
	var dst *os.File
	for n := 1; ; n++ {
		dst, err = os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode())
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create backup file: %w", err)
		}
		backupPath = fmt.Sprintf("%s.bak.%s-%d", path, ts, n)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy to backup: %w", err)
	}

	return backupPath, nil
}

// atomicWriteFile writes data to path atomically by writing to a temp file
// in the same directory and renaming. This prevents partial writes.
// atomicWriteFile stages data into a temp file in the same directory and
// renames it over path, so a reader never observes a partially-written file.
//
// preRename, when non-nil, is called AFTER every real filesystem operation
// that stages the temp file (MkdirAll, CreateTemp, Write, Close, Chmod) and
// IMMEDIATELY before the rename that actually replaces path's content — the
// last point at which this function can still back out. A non-nil return
// aborts: the temp file is removed and path is left untouched. This exists
// for connectJSON/connectTOML's servers-section race guard (Spec 091 FR-005
// gap, round-5 cross-model review of PR #1340): those callers' own re-checks
// before calling this function still left a real, I/O-bearing gap (temp-file
// staging) between the check and the rename; running the SAME check here, at
// this exact point, closes that gap down to the (unavoidable without an
// OS-level lock across the whole read-modify-write sequence) span between
// this call and the os.Rename two lines below — a few fast local syscalls,
// not a real I/O operation an external writer could plausibly land inside.
func atomicWriteFile(path string, data []byte, perm os.FileMode, preRename func() error) error {
	dir := filepath.Dir(path)

	// Ensure the directory exists
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".mcpproxy-connect-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// Clean up on failure
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if preRename != nil {
		if err := preRename(); err != nil {
			return err
		}
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp to target: %w", err)
	}

	success = true
	return nil
}
