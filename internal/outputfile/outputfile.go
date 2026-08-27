// Package outputfile resolves and writes the `save_to_file` capability of
// mcpproxy-go's call_tool_read/write/destructive tools (Spec 076): full,
// untruncated upstream tool responses are written to a file under a
// config-whitelisted root instead of being returned (and truncated) inline.
//
// The package is deliberately pure/stdlib-only and side-effect-light so its
// path-validation logic (the security-sensitive part) is fully unit
// testable without touching the MCP server plumbing.
package outputfile

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Errors returned by Resolve and Write. Callers should prefix the .Error()
// text with "save_to_file: " when surfacing it to an agent; the messages
// here intentionally reveal nothing about the contents of other roots or
// the filesystem beyond the fact that the given path was rejected.
var (
	// ErrInvalidPath covers malformed requests: empty, containing a NUL
	// byte, not absolute, a final path component that is a symlink or any
	// other non-regular file, or a request that resolves to exactly a
	// configured root (a root is a directory, not a save_to_file target).
	ErrInvalidPath = errors.New("save_to_file: invalid path")
	// ErrOutsideRoots is returned when the resolved path does not fall
	// under any configured root.
	ErrOutsideRoots = errors.New("save_to_file: path is outside configured tool_output_roots")
	// ErrDisabled is returned when no roots are configured at all. The
	// exact wording is part of the tool's public contract (surfaced
	// verbatim to agents), so callers should not further wrap this message.
	ErrDisabled = errors.New("save_to_file is disabled: configure tool_output_roots")
	// ErrExists is returned when the resolved target already exists as a
	// regular file and the caller did not request overwrite.
	ErrExists = errors.New("save_to_file: file already exists (pass save_overwrite=true to replace it)")
	// ErrTooLarge is returned by Write when data exceeds the configured
	// tool_output_max_bytes. No file (not even a temp file) is left behind.
	ErrTooLarge = errors.New("save_to_file: data exceeds tool_output_max_bytes")
	// ErrRootUnavailable is returned when a configured root cannot be opened,
	// created, or verified to still be the same directory Resolve just
	// finished resolving. Resolve performs an identity check immediately
	// after opening the root (see Resolve's doc comment) — a mismatch means
	// the path was replaced with a symlink (or a different directory) in the
	// narrow window between symlink resolution and the open, so this fails
	// closed rather than trusting a directory that may not be the one an
	// operator configured. Also returned when os.MkdirAll or os.OpenRoot on
	// the root itself fails outright.
	ErrRootUnavailable = errors.New("save_to_file: configured root is unavailable or was replaced")
	// ErrWriteFailed wraps a lower-level filesystem error encountered while
	// writing the target file itself inside an already-opened root (creating
	// intermediate directories, the temp file, fsync, the pre-rename
	// existence re-check, or the final rename) — as opposed to a
	// request-shape error like ErrInvalidPath, ErrOutsideRoots, or a
	// root-level failure (ErrRootUnavailable).
	ErrWriteFailed = errors.New("save_to_file: failed to write file")
)

// Info describes a file successfully written by Write.
type Info struct {
	Path   string
	Bytes  int64
	SHA256 string
}

// Target is a save_to_file destination that has passed Resolve's whitelist
// checks. Handle and Rel — never Path or Root as strings — are what Write
// actually uses: Resolve itself opens Handle (an *os.Root scoped to the
// resolved root directory) and every filesystem operation Write performs
// goes through Handle via Rel, so a symlink planted anywhere along Root
// AFTER Resolve returns — including at Root's own path — cannot move the
// write outside the directory Handle was opened against (see the TOCTOU
// note on Write, and Resolve's identity check).
type Target struct {
	// Path is the fully resolved (symlink-free), absolute path — retained
	// for display in errors/envelopes/history only. Never open this path
	// directly; always go through Handle+Rel (see Write).
	Path string
	// Root is the absolute, symlink-resolved directory (one of the
	// configured tool_output_roots, after its own symlinks are resolved)
	// that Path falls under — retained for display/logging only, exactly
	// like Path. Never reopened by path; Handle is the live reference.
	Root string
	// Rel is Path relative to Root — the name Write passes to Handle's
	// methods.
	Rel string
	// Handle is the *os.Root opened by Resolve against Root, already
	// identity-checked (see Resolve). The caller MUST close it — via
	// Close() — once done with Target, typically deferred immediately after
	// a successful Resolve call. A Resolve error never returns a Target with
	// a non-nil Handle still open (any handle opened partway through a
	// failing Resolve call is closed before the error returns).
	Handle *os.Root
}

// Close releases the *os.Root handle opened by Resolve. Safe to call on a
// zero Target or one whose Handle is nil (a no-op). Callers must defer
// Close() immediately after a successful Resolve call (see writeSaveToFile
// in internal/server/content_forward.go); Resolve itself never leaks an
// open handle on an error return, so Close is only ever needed after
// success.
func (t Target) Close() error {
	if t.Handle == nil {
		return nil
	}
	return t.Handle.Close()
}

// Resolve validates a requested absolute path against the configured
// whitelist roots and returns a Target — carrying an already-opened,
// identity-checked *os.Root handle (Target.Handle) — describing the
// fully-resolved (symlink-free) destination to write to, or an error
// explaining why the request was rejected. On any error return, no handle
// is left open (see step 6 below and Target.Handle's doc comment).
//
// Rules, applied in order:
//  1. roots empty                                -> ErrDisabled
//  2. requested empty, contains a NUL byte, or is
//     not absolute                                -> ErrInvalidPath
//  3. requested is filepath.Clean'ed
//  4. the deepest EXISTING ancestor DIRECTORY of the cleaned path is
//     resolved via filepath.EvalSymlinks (so a symlinked directory
//     anywhere along the path — e.g. macOS's /var -> /private/var, or an
//     attacker-controlled symlink planted inside a root — cannot be used to
//     escape the whitelist), and the non-existing remainder (including the
//     final path component, which is NEVER symlink-resolved so a symlink
//     sitting exactly at the target path is still visible to step 7) is
//     re-joined onto it
//  5. each configured root is resolved through the SAME deepest-existing-
//     ancestor logic (full EvalSymlinks when the root itself already
//     exists — including a symlinked root directory, which is intentionally
//     honored — or ancestor-resolved-and-rejoined when it does not yet
//     exist, so a not-yet-created root under a symlinked ancestor, e.g.
//     macOS's /tmp -> /private/tmp, is not wrongly rejected). The resolved
//     target must equal one resolved root, or fall strictly inside it (a
//     separator-bounded prefix match, so "/r/proj-evil" is never accepted
//     against root "/r/proj") — otherwise ErrOutsideRoots. A target that
//     resolves to EXACTLY a configured root is always ErrInvalidPath (a
//     root is a directory to write under, never itself the file to write)
//  6. the matched root is created if it does not exist yet
//     (os.MkdirAll(root, 0700) — configured roots need not exist at
//     startup), then opened via os.OpenRoot and identity-checked: the
//     opened directory's Stat(".") is compared (os.SameFile) against a
//     fresh os.Lstat of the root path. A mismatch — the root path is now a
//     symlink, or resolves to a different directory than what was just
//     opened — closes the handle and fails with ErrRootUnavailable rather
//     than trusting a directory that may not be the one that was just
//     validated. Any other MkdirAll/OpenRoot/Stat/Lstat failure also fails
//     with ErrRootUnavailable. See the package-level security note below
//     this function for exactly what this check does and does not close.
//  7. if the resolved target already exists:
//     - a symlink, or any other non-regular file (device, socket, dir, …)
//     -> ErrInvalidPath (never overwritten, even with save_overwrite=true)
//     - a regular file and !overwrite                -> ErrExists
//     - a regular file and overwrite                  -> accepted
//
// # Security: what the identity check closes, and what it does not
//
// Once Resolve returns a Target, every filesystem operation Write performs
// goes through Target.Handle — an already-open fd/handle — never through a
// path string. That structurally closes the classic TOCTOU race for
// everything that happens AFTER the handle is open: a symlink or rename
// planted inside the root, or a replacement of the root directory itself or
// any of its ancestors, between Resolve returning and Write running, cannot
// move where the write lands (the open handle keeps referencing the
// original directory even if its path is later replaced).
//
// What this does NOT close is the sub-microsecond window between this
// function's own symlink resolution (steps 4-5, via filepath.EvalSymlinks)
// and the os.OpenRoot call in step 6: a same-user process that wins that
// exact race — swapping a directory component for a symlink in the
// instant between EvalSymlinks and OpenRoot — could, in principle, get
// OpenRoot to open the wrong directory undetected if it also removes the
// symlink again before the identity check's os.Lstat runs. Ancestors of a
// configured root are administrator-controlled, and a same-user process
// capable of winning that race against its own filesystem already has
// every other means of writing anywhere that user can write — so this is
// not treated as a meaningful escalation, only documented as a residual.
// The identity check DOES reliably catch the realistic version of this
// attack (a symlink planted at the root's own path and left in place, e.g.
// "rename the real root aside, drop a symlink at its former path"), because
// that symlink is still there for the os.Lstat comparison to see.
func Resolve(roots []string, requested string, overwrite bool) (Target, error) {
	if len(roots) == 0 {
		return Target{}, ErrDisabled
	}
	if requested == "" || strings.IndexByte(requested, 0) >= 0 {
		return Target{}, ErrInvalidPath
	}
	if !filepath.IsAbs(requested) {
		return Target{}, ErrInvalidPath
	}

	cleaned := filepath.Clean(requested)

	resolvedTarget, err := resolveTargetPath(cleaned)
	if err != nil {
		return Target{}, ErrInvalidPath
	}

	matchedRoot := ""
	for _, root := range roots {
		resolvedRoot := resolveRootPath(root)
		if resolvedTarget == resolvedRoot {
			return Target{}, fmt.Errorf("%w: target must be a file inside a root, not the root itself", ErrInvalidPath)
		}
		if matchedRoot == "" && strings.HasPrefix(resolvedTarget, resolvedRoot+string(os.PathSeparator)) {
			matchedRoot = resolvedRoot
		}
	}
	if matchedRoot == "" {
		return Target{}, ErrOutsideRoots
	}

	// Configured roots need not exist at startup (config.go's ToolOutputRoots
	// doc comment promises this) — os.OpenRoot below cannot create the
	// directory itself, so create it here, under the already-ancestor-resolved
	// matchedRoot, before opening it.
	if err := os.MkdirAll(matchedRoot, 0o700); err != nil {
		return Target{}, fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}

	// Open the root HERE, immediately after it is matched/created, and carry
	// the handle in the returned Target — Write must never re-derive a root
	// from a path string again (that was the TOCTOU this closes: re-opening
	// by path after Resolve returned gave a symlink swapped in during the gap
	// something to be followed). See the package-level security note above
	// this function.
	handle, err := os.OpenRoot(matchedRoot)
	if err != nil {
		return Target{}, fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}
	openedInfo, err := handle.Stat(".")
	if err != nil {
		_ = handle.Close()
		return Target{}, fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}
	diskInfo, err := os.Lstat(matchedRoot)
	if err != nil {
		_ = handle.Close()
		return Target{}, fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}
	if !os.SameFile(openedInfo, diskInfo) {
		// The root path no longer names the directory that was just opened
		// (e.g. it is now a symlink, or a different directory) — see the
		// "Security" note above: this is exactly the identity check that
		// catches a root swapped-for-a-symlink between resolution and open.
		_ = handle.Close()
		return Target{}, ErrRootUnavailable
	}

	if fi, statErr := os.Lstat(resolvedTarget); statErr == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			_ = handle.Close()
			return Target{}, ErrInvalidPath
		}
		if !overwrite {
			_ = handle.Close()
			return Target{}, ErrExists
		}
	} else if !os.IsNotExist(statErr) {
		// Permission error or similar — fail closed rather than silently
		// proceeding against a path we could not stat.
		_ = handle.Close()
		return Target{}, ErrInvalidPath
	}

	rel, err := filepath.Rel(matchedRoot, resolvedTarget)
	if err != nil {
		// Should not happen: resolvedTarget was just proven to be matchedRoot
		// or a separator-bounded descendant of it.
		_ = handle.Close()
		return Target{}, ErrInvalidPath
	}

	return Target{Path: resolvedTarget, Root: matchedRoot, Rel: rel, Handle: handle}, nil
}

// resolveTargetPath finds the deepest existing ancestor DIRECTORY of
// cleaned (always a proper ancestor — the final path component itself is
// never passed to EvalSymlinks so callers can still detect it being a
// symlink via Lstat afterwards), resolves that ancestor's symlinks, and
// re-joins the non-existing remainder (including the final component)
// literally onto the resolved ancestor.
func resolveTargetPath(cleaned string) (string, error) {
	return resolveDeepestExistingAncestor(cleaned, true)
}

// resolveRootPath resolves a single configured root for comparison against a
// resolved target. If the root already exists (including as a symlink to a
// directory — intentionally honored, an admin explicitly configured it),
// its symlinks are fully resolved. If it does not exist yet, it is resolved
// through its deepest existing ancestor and the non-existing remainder is
// rejoined literally — the same escape-safe logic Resolve applies to
// targets — so a not-yet-created root under a symlinked ancestor (e.g.
// macOS's /tmp -> /private/tmp) is not silently mismatched.
func resolveRootPath(root string) string {
	resolved, err := resolveDeepestExistingAncestor(filepath.Clean(root), false)
	if err != nil {
		return filepath.Clean(root)
	}
	return resolved
}

// resolveDeepestExistingAncestor climbs from cleaned toward the filesystem
// root until it finds a path component that currently exists, resolves that
// existing ancestor's symlinks via filepath.EvalSymlinks, and rejoins the
// non-existing remainder onto the resolved ancestor literally (never
// symlink-resolved, since it doesn't exist to resolve).
//
// When excludeFinal is true, the final path component of cleaned is always
// treated as part of the "remainder" — even if it currently exists on disk
// — so it is never itself passed through EvalSymlinks. This is what target
// resolution needs: a symlink sitting exactly at the target path must
// remain visible to a subsequent Lstat check rather than being silently
// followed. When excludeFinal is false, an existing full path is resolved
// end-to-end (equivalent to a plain EvalSymlinks(cleaned)) — what root
// resolution needs, since a fully-existing symlinked root is an intentional
// admin configuration, not an attack.
func resolveDeepestExistingAncestor(cleaned string, excludeFinal bool) (string, error) {
	dir := cleaned
	var remainder []string
	if excludeFinal {
		dir = filepath.Dir(cleaned)
		remainder = []string{filepath.Base(cleaned)}
	}

	for {
		if _, statErr := os.Lstat(dir); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without it "existing" — should not
			// happen in practice, but stop climbing rather than loop forever.
			break
		}
		remainder = append([]string{filepath.Base(dir)}, remainder...)
		dir = parent
	}

	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}

	result := resolvedDir
	for _, part := range remainder {
		result = filepath.Join(result, part)
	}
	return result, nil
}

// Write atomically writes data under target: a temp file is created inside
// target.Handle (with a randomly-suffixed name), written, fsync'ed, and
// renamed into place. Every filesystem operation goes through
// target.Handle — the *os.Root Resolve already opened and identity-checked
// — and Rel; Write never touches target.Root or target.Path as strings, and
// never calls os.OpenRoot itself. That is what closes the TOCTOU: even if a
// local process replaces a directory component of target.Rel with a
// symlink after Resolve returned (the classic check-then-use race: Resolve
// validates "<root>/sub/f.txt", then something creates "<root>/sub" as a
// symlink to an arbitrary location before Write runs), Handle's
// per-component containment check refuses to follow it outside the
// directory it was opened against — and a swap of the root's OWN path after
// Resolve returned cannot move Handle's fd at all, since Handle no longer
// looks anything up by that path. This closes the race that a plain
// os.MkdirAll + os.CreateTemp + os.Rename sequence (which follows symlinks
// at every step, the same as any other os/syscall path lookup) cannot
// close, and closes it more completely than re-opening the root by path in
// Write would (that still left the root's own path open to a swap between
// Resolve and Write — see Resolve's package-level security note for what
// remains a residual after this fix).
//
// Write does NOT close target.Handle — the caller owns the handle's
// lifecycle (opened by Resolve, closed by the caller via Target.Close()),
// since a caller may reasonably call Write more than once against the same
// resolved Target within one held-open root.
//
// maxBytes <= 0 disables the size check. When data exceeds maxBytes, Write
// returns ErrTooLarge WITHOUT creating any file, temp or otherwise.
//
// When overwrite is false, Write re-checks that the target does not exist
// immediately before the rename (in addition to any check the caller made
// via Resolve) and fails with ErrExists if it now does; a small window
// between that check and the rename remains (renaming over a
// concurrently-created file of the same name), which — unlike the
// directory-escape race above — cannot move the write itself outside
// target.Root, so it is left as an accepted, documented residual race
// rather than solved with exclusive-create semantics on the final name
// (which would require choosing new not-quite-atomic-either semantics for
// the caller-visible rename step).
//
// Every other filesystem error Write encounters (creating intermediate
// directories, opening/writing/syncing/closing the temp file, the
// pre-rename existence re-check, or the rename itself) is wrapped as
// fmt.Errorf("%w: %v", ErrWriteFailed, err) so callers can distinguish "the
// write itself failed" from the request-shape errors Resolve returns.
//
// Intermediate directories MkdirAll creates under target.Root are NOT
// cleaned up if a later step in this same Write call fails (only the temp
// file itself is removed via removeTemp) — an accepted residual; a
// half-created directory chain with no file in it is not a meaningful
// disclosure or escape and cleaning it up correctly would need to track
// exactly which components this call created versus already existed.
func Write(target Target, data []byte, maxBytes int64, overwrite bool) (Info, error) {
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return Info{}, ErrTooLarge
	}
	if target.Handle == nil {
		return Info{}, fmt.Errorf("%w: target has no open root handle (must come from Resolve)", ErrRootUnavailable)
	}
	root := target.Handle

	relDir := filepath.Dir(target.Rel)
	if relDir != "." && relDir != "" {
		if err := root.MkdirAll(relDir, 0o700); err != nil {
			return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
		}
	}

	tmpRel, err := randomTempName(relDir)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}

	tmpFile, err := root.OpenFile(tmpRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}
	removeTemp := func() { _ = root.Remove(tmpRel) }

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		removeTemp()
		return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		removeTemp()
		return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}
	if err := tmpFile.Close(); err != nil {
		removeTemp()
		return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}

	if !overwrite {
		if _, statErr := root.Lstat(target.Rel); statErr == nil {
			removeTemp()
			return Info{}, ErrExists
		} else if !os.IsNotExist(statErr) {
			removeTemp()
			return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, statErr)
		}
	}

	if err := root.Rename(tmpRel, target.Rel); err != nil {
		removeTemp()
		return Info{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}

	sum := sha256.Sum256(data)
	return Info{
		Path:   target.Path,
		Bytes:  int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

// randomTempName builds a ".mcpproxy-<random-hex>.tmp" name inside dir
// (dir == "." or "" means "at the root of the os.Root"). os.CreateTemp is
// not usable here — it is not root-scoped — so the random suffix is
// generated by hand from crypto/rand.
func randomTempName(dir string) (string, error) {
	var buf [16]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return "", err
	}
	name := ".mcpproxy-" + hex.EncodeToString(buf[:]) + ".tmp"
	if dir == "." || dir == "" {
		return name, nil
	}
	return filepath.Join(dir, name), nil
}
