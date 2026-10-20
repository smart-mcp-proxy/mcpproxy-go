package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Content-addressed last-known-good (LKG) store (spec 101 FR-011a, research
// D6). The serving external bundle is durable in <dataDir>/tpa-lkg so a
// restart can recover the (bundle, watermark) pair the persisted state names
// even when the operator-configured file has since changed or vanished.
//
// Files are named by the SHA-256 of their bytes: <digest>.json for the bundle
// and <digest>.json.sig for the sidecar. A file whose bytes do not hash to its
// name is treated as missing, so substituting an LKG file can only downgrade
// the install to the embedded default, never put attacker bytes into service.
//
// Everything here is best-effort against an attacker who controls the data
// directory (the spec's threat-model boundary, SC-012); what it guarantees is
// crash consistency, not tamper-proofness.

const (
	lkgDirName    = "tpa-lkg"
	lkgTempPrefix = ".tmp-"
)

// Fault-injection seams (FR-011a). Production code leaves both nil; a crash
// test aborts the run at the named boundary and then simulates a restart over
// the same store and data directory.
var (
	// lkgAfterWriteHook runs once the LKG files are durable and before the
	// state commit.
	lkgAfterWriteHook func()
	// stateAfterCommitHook runs once the state is committed and before the new
	// bundle is published.
	stateAfterCommitHook func()
)

// digestHexPattern is the only shape a digest may have before it is allowed to
// become part of a file name: the persisted state is not trusted to be
// path-safe.
var digestHexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func lkgDirPath(dataDir string) string { return filepath.Join(dataDir, lkgDirName) }

// errLKGSubstituted marks an LKG file whose bytes do not hash to the digest the
// state recorded for it.
var errLKGSubstituted = errors.New("last-known-good file does not match its recorded digest")

// lkgWrite makes one bundle (and its sidecar, when signed) durable:
//
//	temp file in the same directory -> write -> fsync -> close -> rename to the
//	final name -> fsync the directory.
//
// It is idempotent: a final file that already holds the same bytes is left
// alone. Only after lkgWrite returns may the state commit and the publish
// happen (the recoverable LKG must be durable before the pointer moves).
func lkgWrite(dataDir, digest string, bundle, sidecar []byte, signed bool) error {
	if !digestHexPattern.MatchString(digest) {
		return fmt.Errorf("refusing to name an LKG file after %q", digest)
	}
	dir := lkgDirPath(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := lkgWriteFile(dir, digest+".json", bundle); err != nil {
		return err
	}
	sigPath := filepath.Join(dir, digest+".json.sig")
	if signed {
		if err := lkgWriteFile(dir, digest+".json.sig", sidecar); err != nil {
			return err
		}
	} else if err := os.Remove(sigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return lkgSyncDir(dir)
}

// lkgWriteFile writes data to dir/name via a synced temp file and a rename.
func lkgWriteFile(dir, name string, data []byte) error {
	final := filepath.Join(dir, name)
	if st, err := os.Lstat(final); err == nil && st.Mode().IsRegular() {
		if existing, rerr := os.ReadFile(final); rerr == nil && bytes.Equal(existing, data) { //nolint:gosec // path is dataDir/tpa-lkg/<validated digest>
			return nil
		}
	}
	tmp, err := os.CreateTemp(dir, lkgTempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		cleanup()
		return err
	}
	return nil
}

// lkgSyncDir fsyncs the directory so the renames are durable. Directory fsync
// is not supported on Windows (opening a directory handle for sync fails
// there); NTFS journals the rename itself, so it is skipped.
func lkgSyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir) //nolint:gosec // dataDir/tpa-lkg
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// lkgRead returns the candidate the state's Active record names, read through
// the same descriptor-first, ceiling-bounded reader as a file drop, and checked
// against the recorded digests.
func lkgRead(ctx context.Context, dataDir string, rec *TPABundleActiveRecord) (Candidate, error) {
	if !digestHexPattern.MatchString(rec.FullDigest) {
		return Candidate{}, errLKGSubstituted
	}
	dir := lkgDirPath(dataDir)
	bundle, err := readCandidateFile(ctx, filepath.Join(dir, rec.FullDigest+".json"), MaxBundleBytes)
	if err != nil {
		return Candidate{}, err
	}
	if digestHex(bundle) != rec.FullDigest {
		return Candidate{}, errLKGSubstituted
	}
	c := Candidate{Bundle: bundle, Source: rec.Source, Path: rec.Path}
	if rec.SignatureVerified {
		sidecar, err := readCandidateFile(ctx, filepath.Join(dir, rec.FullDigest+".json.sig"), MaxSidecarBytes)
		if err != nil {
			return Candidate{}, err
		}
		if digestHex(sidecar) != rec.SidecarDigest {
			return Candidate{}, errLKGSubstituted
		}
		c.Sidecar, c.SidecarPresent = sidecar, true
	}
	return c, nil
}

// lkgReferenced is the set of digests the state still needs on disk: the
// serving record and every authority's rollback pin.
func lkgReferenced(st *TPABundleState) map[string]struct{} {
	keep := map[string]struct{}{}
	if st == nil {
		return keep
	}
	if st.Active != nil {
		keep[st.Active.FullDigest] = struct{}{}
	}
	for _, a := range st.Authorities {
		if a != nil && a.Pin != nil {
			keep[a.Pin.FullDigest] = struct{}{}
		}
	}
	return keep
}

// lkgReconcile removes LKG files and leftover temp files that no live state
// record references: the debris of an interrupted activation, a superseded
// release, or a refused candidate. It never deletes outside the tpa-lkg
// directory, never follows a symlink (non-regular entries are skipped) and
// leaves alone any name that is not an LKG entry. It returns the removed names.
func lkgReconcile(dataDir string, st *TPABundleState) ([]string, error) {
	dir := lkgDirPath(dataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	keep := lkgReferenced(st)
	var removed []string
	var firstErr error
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		remove := false
		switch {
		case strings.HasPrefix(name, lkgTempPrefix):
			remove = true
		case strings.HasSuffix(name, ".json.sig"):
			remove = lkgOrphan(strings.TrimSuffix(name, ".json.sig"), keep)
		case strings.HasSuffix(name, ".json"):
			remove = lkgOrphan(strings.TrimSuffix(name, ".json"), keep)
		}
		if !remove {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed = append(removed, name)
	}
	return removed, firstErr
}

// lkgOrphan reports whether stem is a well-formed digest nothing references.
func lkgOrphan(stem string, keep map[string]struct{}) bool {
	if !digestHexPattern.MatchString(stem) {
		return false
	}
	_, referenced := keep[stem]
	return !referenced
}
