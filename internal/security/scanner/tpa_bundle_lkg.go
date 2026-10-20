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
// Files are named by the SHA-256 of their bytes: <bundleDigest>.json for the
// bundle and <bundleDigest>.<sidecarDigest>.sig for the sidecar. The sidecar is
// addressed by its OWN digest as well because its bytes are not a function of
// the bundle's (the same signature can arrive in differently formatted JSON):
// a re-formatted sidecar for the same bundle must never overwrite the file the
// committed state still names. A file whose bytes do not hash to its name is
// treated as missing, so substituting an LKG file can only downgrade the
// install to the embedded default, never put attacker bytes into service.
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

// lkgSidecarName is the file name of one (bundle, sidecar) pair's sidecar.
func lkgSidecarName(bundleDigest, sidecarDigest string) string {
	return bundleDigest + "." + sidecarDigest + ".sig"
}

// lkgSidecarKey is the reconcile key of one (bundle, sidecar) pair.
func lkgSidecarKey(bundleDigest, sidecarDigest string) string {
	return bundleDigest + "." + sidecarDigest
}

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
	if signed {
		if err := lkgWriteFile(dir, lkgSidecarName(digest, digestHex(sidecar)), sidecar); err != nil {
			return err
		}
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
	return lkgReadPair(ctx, dataDir, rec.FullDigest, rec.SidecarDigest, rec.SignatureVerified, rec.Source, rec.Path)
}

// lkgReadPair reads the bundle (and, when signed, the sidecar) the digests
// name and verifies the bytes hash to them.
func lkgReadPair(ctx context.Context, dataDir, bundleDigest, sidecarDigest string, signed bool, source, path string) (Candidate, error) {
	if !digestHexPattern.MatchString(bundleDigest) {
		return Candidate{}, errLKGSubstituted
	}
	dir := lkgDirPath(dataDir)
	bundle, err := readCandidateFile(ctx, filepath.Join(dir, bundleDigest+".json"), MaxBundleBytes)
	if err != nil {
		return Candidate{}, err
	}
	if digestHex(bundle) != bundleDigest {
		return Candidate{}, errLKGSubstituted
	}
	c := Candidate{Bundle: bundle, Source: source, Path: path}
	if signed {
		if !digestHexPattern.MatchString(sidecarDigest) {
			return Candidate{}, errLKGSubstituted
		}
		sidecar, err := readCandidateFile(ctx, filepath.Join(dir, lkgSidecarName(bundleDigest, sidecarDigest)), MaxSidecarBytes)
		if err != nil {
			return Candidate{}, err
		}
		if digestHex(sidecar) != sidecarDigest {
			return Candidate{}, errLKGSubstituted
		}
		c.Sidecar, c.SidecarPresent = sidecar, true
	}
	return c, nil
}

// lkgRefs is what the state still needs on disk.
type lkgRefs struct {
	bundles  map[string]struct{} // bundle digests
	sidecars map[string]struct{} // lkgSidecarKey(bundle, sidecar) pairs
}

// lkgReferenced is the set of files the state still needs: the serving record
// and every authority's rollback pin, each as the exact (bundle, sidecar) pair.
func lkgReferenced(st *TPABundleState) lkgRefs {
	keep := lkgRefs{bundles: map[string]struct{}{}, sidecars: map[string]struct{}{}}
	if st == nil {
		return keep
	}
	add := func(bundleDigest, sidecarDigest string) {
		keep.bundles[bundleDigest] = struct{}{}
		if sidecarDigest != "" {
			keep.sidecars[lkgSidecarKey(bundleDigest, sidecarDigest)] = struct{}{}
		}
	}
	if st.Active != nil {
		add(st.Active.FullDigest, st.Active.SidecarDigest)
	}
	for _, a := range st.Authorities {
		if a != nil && a.Pin != nil {
			add(a.Pin.FullDigest, a.Pin.SidecarDigest)
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
			// The pre-content-addressed sidecar name: never read, always debris.
			remove = digestHexPattern.MatchString(strings.TrimSuffix(name, ".json.sig"))
		case strings.HasSuffix(name, ".sig"):
			stem := strings.TrimSuffix(name, ".sig")
			if b, sc, ok := strings.Cut(stem, "."); ok && digestHexPattern.MatchString(b) && digestHexPattern.MatchString(sc) {
				_, referenced := keep.sidecars[stem]
				remove = !referenced
			}
		case strings.HasSuffix(name, ".json"):
			stem := strings.TrimSuffix(name, ".json")
			if digestHexPattern.MatchString(stem) {
				_, referenced := keep.bundles[stem]
				remove = !referenced
			}
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
