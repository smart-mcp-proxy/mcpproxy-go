package outputfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_UnderRootOK(t *testing.T) {
	root := t.TempDir()
	requested := filepath.Join(root, "sub", "file.txt")

	got, err := Resolve([]string{root}, requested, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	if got.Root != wantRoot {
		t.Fatalf("Resolve().Root = %q, want %q", got.Root, wantRoot)
	}
	wantPrefix := wantRoot + string(os.PathSeparator)
	if !strings.HasPrefix(got.Path, wantPrefix) {
		t.Fatalf("Resolve().Path = %q, want a path under %q", got.Path, wantRoot)
	}
	if got.Rel != filepath.Join("sub", "file.txt") {
		t.Fatalf("Resolve().Rel = %q, want %q", got.Rel, filepath.Join("sub", "file.txt"))
	}
}

// TestResolve_RootItselfRejected pins the rule that a request resolving to
// exactly a configured root is rejected — save_to_file must target a file
// INSIDE a root, not the root directory itself. (Previously this was
// accepted — see the removed TestResolve_RootItselfOK.)
func TestResolve_RootItselfRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "exact-root-file")

	_, err := Resolve([]string{root}, root, false)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidPath", err)
	}
}

func TestResolve_PrefixBoundaryRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(base, "proj-evil", "f")

	_, err := Resolve([]string{root}, evil, false)
	if !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("Resolve() error = %v, want ErrOutsideRoots", err)
	}
}

func TestResolve_DotDotEscapeRejected(t *testing.T) {
	root := t.TempDir()
	escaped := filepath.Join(root, "..", "evil", "file.txt")

	_, err := Resolve([]string{root}, escaped, false)
	if !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("Resolve() error = %v, want ErrOutsideRoots", err)
	}
}

func TestResolve_SymlinkedDirectoryInsideRootEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	requested := filepath.Join(link, "file.txt")
	_, err := Resolve([]string{root}, requested, false)
	if !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("Resolve() error = %v, want ErrOutsideRoots", err)
	}
}

func TestResolve_SymlinkedRootAccepted(t *testing.T) {
	real := t.TempDir()
	base := t.TempDir()
	rootLink := filepath.Join(base, "root-link")
	if err := os.Symlink(real, rootLink); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	requested := filepath.Join(rootLink, "file.txt")
	got, err := Resolve([]string{rootLink}, requested, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	wantReal, _ := filepath.EvalSymlinks(real)
	want := filepath.Join(wantReal, "file.txt")
	if got.Path != want {
		t.Fatalf("Resolve().Path = %q, want %q", got.Path, want)
	}
	if got.Root != wantReal {
		t.Fatalf("Resolve().Root = %q, want %q", got.Root, wantReal)
	}
	if got.Rel != "file.txt" {
		t.Fatalf("Resolve().Rel = %q, want %q", got.Rel, "file.txt")
	}
}

// TestResolve_RootMissingUnderSymlinkedAncestor pins the rule that a
// configured root that does not exist YET, but whose ancestor is a symlink
// (e.g. macOS's /tmp -> /private/tmp), must still be resolved and matched
// correctly instead of being wrongly rejected as ErrOutsideRoots.
// Hand-builds the symlinked-ancestor scenario so it runs on every OS with
// symlink support, not just macOS. This only pins Resolve's own path
// resolution — see TestWrite_RootCreatedWhenMissing in this file for the
// end-to-end Write pin (the root not existing yet must not make Write fail
// with "no such file or directory").
func TestResolve_RootMissingUnderSymlinkedAncestor(t *testing.T) {
	real := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	// root itself ("link/out") does not exist — only its symlinked
	// ancestor ("link" -> real) does.
	root := filepath.Join(link, "out")
	requested := filepath.Join(root, "f.txt")

	got, err := Resolve([]string{root}, requested, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil (root missing under a symlinked ancestor must still resolve)", err)
	}
	wantReal, _ := filepath.EvalSymlinks(real)
	wantRoot := filepath.Join(wantReal, "out")
	if got.Root != wantRoot {
		t.Fatalf("Resolve().Root = %q, want %q", got.Root, wantRoot)
	}
	wantPath := filepath.Join(wantRoot, "f.txt")
	if got.Path != wantPath {
		t.Fatalf("Resolve().Path = %q, want %q", got.Path, wantPath)
	}
	if got.Rel != "f.txt" {
		t.Fatalf("Resolve().Rel = %q, want %q", got.Rel, "f.txt")
	}
}

func TestResolve_FinalTargetSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	dest := filepath.Join(other, "dest.txt")
	if err := os.WriteFile(dest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leaf.txt")
	if err := os.Symlink(dest, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	_, err := Resolve([]string{root}, link, true)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidPath (even with save_overwrite=true)", err)
	}
}

func TestResolve_RelativeRejected(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve([]string{root}, "relative/path.txt", false)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidPath", err)
	}
}

func TestResolve_NULByteRejected(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve([]string{root}, root+"/f\x00ile", false)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidPath", err)
	}
}

func TestResolve_EmptyRequestedRejected(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve([]string{root}, "", false)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Resolve() error = %v, want ErrInvalidPath", err)
	}
}

func TestResolve_RootsEmptyDisabled(t *testing.T) {
	_, err := Resolve(nil, "/tmp/whatever", false)
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Resolve() error = %v, want ErrDisabled", err)
	}
	if err.Error() != "save_to_file is disabled: configure tool_output_roots" {
		t.Fatalf("Resolve() error text = %q, want exact contract message", err.Error())
	}
}

func TestResolve_ExistsNoOverwriteRejected(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Resolve([]string{root}, target, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Resolve() error = %v, want ErrExists", err)
	}
}

func TestResolve_ExistsOverwriteAccepted(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve([]string{root}, target, true)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if got.Path == "" {
		t.Fatal("Resolve() returned empty Path")
	}
}

func TestResolve_MissingIntermediateDirsOK(t *testing.T) {
	root := t.TempDir()
	requested := filepath.Join(root, "a", "b", "c", "file.txt")

	got, err := Resolve([]string{root}, requested, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(root)
	wantPath := filepath.Join(wantRoot, "a", "b", "c", "file.txt")
	if got.Path != wantPath {
		t.Fatalf("Resolve().Path = %q, want %q", got.Path, wantPath)
	}
	wantRel := filepath.Join("a", "b", "c", "file.txt")
	if got.Rel != wantRel {
		t.Fatalf("Resolve().Rel = %q, want %q", got.Rel, wantRel)
	}
}

func TestWrite_HappyPath(t *testing.T) {
	root := t.TempDir()
	target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	data := []byte("hello world")

	info, err := Write(target, data, 0, false)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if info.Path != target.Path {
		t.Errorf("info.Path = %q, want %q", info.Path, target.Path)
	}
	if info.Bytes != int64(len(data)) {
		t.Errorf("info.Bytes = %d, want %d", info.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	wantSHA := hex.EncodeToString(sum[:])
	if info.SHA256 != wantSHA {
		t.Errorf("info.SHA256 = %q, want %q", info.SHA256, wantSHA)
	}

	got, err := os.ReadFile(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("file content = %q, want %q", got, data)
	}

	// Files are written 0600, not world/group-readable.
	fi, err := os.Stat(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestWrite_TooLargeLeavesNoFile(t *testing.T) {
	root := t.TempDir()
	target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	data := []byte("0123456789")

	_, err = Write(target, data, 5, false)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Write() error = %v, want ErrTooLarge", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory not empty after ErrTooLarge (no temp file must remain): %v", entries)
	}
}

// tool_output_max_bytes boundary — exactly at the limit is allowed.
func TestWrite_ExactlyMaxBytesAllowed(t *testing.T) {
	root := t.TempDir()
	target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	data := []byte("0123456789")

	info, err := Write(target, data, int64(len(data)), false)
	if err != nil {
		t.Fatalf("Write() error = %v, want nil when len(data) == maxBytes", err)
	}
	if info.Bytes != int64(len(data)) {
		t.Errorf("info.Bytes = %d, want %d", info.Bytes, len(data))
	}
}

// maxBytes <= 0 disables the size check entirely.
func TestWrite_NonPositiveMaxBytesMeansNoLimit(t *testing.T) {
	root := t.TempDir()
	data := []byte(strings.Repeat("z", 100000))

	for _, maxBytes := range []int64{0, -1} {
		target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), true)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if _, err := Write(target, data, maxBytes, true); err != nil {
			t.Fatalf("Write() error = %v with maxBytes=%d, want nil (no limit)", err, maxBytes)
		}
	}
}

func TestWrite_MissingIntermediateDirsCreated(t *testing.T) {
	root := t.TempDir()
	target, err := Resolve([]string{root}, filepath.Join(root, "a", "b", "c", "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if _, err := Write(target, []byte("x"), 0, false); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(target.Path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	// Intermediate directories are created 0700, not world/group-readable.
	fi, err := os.Stat(filepath.Join(root, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestWrite_OverwriteReplacesContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "out.txt")
	target, err := Resolve([]string{root}, path, false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := Write(target, []byte("old"), 0, false); err != nil {
		t.Fatal(err)
	}

	target2, err := Resolve([]string{root}, path, true)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	info, err := Write(target2, []byte("new-content"), 0, true)
	if err != nil {
		t.Fatalf("Write() overwrite error = %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new-content" {
		t.Errorf("file content = %q, want %q", got, "new-content")
	}
	if info.Bytes != int64(len("new-content")) {
		t.Errorf("info.Bytes = %d, want %d", info.Bytes, len("new-content"))
	}
}

func TestWrite_NoOverwriteExistingFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Resolve itself rejects an existing file without overwrite (ErrExists),
	// so build the Target by hand (with a real opened Handle — Write requires
	// one) to exercise Write's own re-check directly.
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	target := Target{Path: path, Root: root, Rel: "out.txt", Handle: handle}
	_, err = Write(target, []byte("new"), 0, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Write() error = %v, want ErrExists", err)
	}
	// Content must be untouched.
	got, _ := os.ReadFile(path)
	if string(got) != "old" {
		t.Errorf("file content changed despite ErrExists: %q", got)
	}
	// No stray temp file left behind.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry in dir, got %d: %v", len(entries), entries)
	}
}

func TestWrite_NoTempFileLeftOnSuccess(t *testing.T) {
	root := t.TempDir()
	target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := Write(target, []byte("data"), 0, false); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.txt" {
		t.Fatalf("unexpected directory contents after Write: %v", entries)
	}
}

// TestWrite_SymlinkInsideRootPlantedAfterResolve pins the TOCTOU where a
// local process (the untrusted party is the upstream MCP server itself, not
// the requester) replaces an intermediate directory INSIDE an
// already-Resolve'd target's root with a symlink pointing outside every
// configured root, between the Resolve call and the Write call. Before this
// was fixed (plain os.MkdirAll + os.CreateTemp + os.Rename, which all follow
// symlinks like any other syscall path lookup), this landed the temp file
// and the final write outside the whitelist. Target.Handle is an *os.Root
// scoped to the root directory, opened by Resolve BEFORE this symlink is
// planted — its per-component containment (methods on os.Root follow
// symlinks but refuse to let them reference a location outside the root)
// still refuses to follow a symlink planted at "sub", so the write must fail
// and nothing must appear outside root. See
// TestWrite_RootItselfSwappedForSymlinkAfterResolve below for the more
// severe TOCTOU this feature originally had: the root's OWN path (not a
// directory inside it) being swapped for a symlink.
func TestWrite_SymlinkInsideRootPlantedAfterResolve(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	target, err := Resolve([]string{root}, filepath.Join(root, "sub", "f.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	defer func() { _ = target.Close() }()

	// Attacker/race window: "sub" did not exist at Resolve time (Write was
	// going to MkdirAll it); now plant it as a symlink to outside.
	subPath := filepath.Join(root, "sub")
	if err := os.Symlink(outside, subPath); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	_, err = Write(target, []byte("payload"), 0, false)
	if err == nil {
		t.Fatal("Write() error = nil, want an error — the planted symlink must not be followed outside root")
	}

	// Nothing must have landed outside the root.
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("write escaped the whitelist via the planted symlink: %v", entries)
	}
}

// TestWrite_RootItselfSwappedForSymlinkAfterResolve pins the TOCTOU a prior
// version of this package had: Write re-opened the root BY PATH
// (os.OpenRoot(target.Root)) inside Write itself, so a symlink planted at
// the root's own path between Resolve returning and Write running caused
// every subsequent os.Root operation to be scoped to whatever the symlink
// pointed at instead of the directory Resolve had actually validated.
//
// With Resolve opening (and identity-checking) the *os.Root handle itself,
// and Write using ONLY that already-open handle, this scenario is closed
// structurally rather than by a check: once the handle is open, a later swap
// of the root's own path cannot move the underlying fd — Write keeps
// operating on the ORIGINAL directory Resolve validated, wherever it now
// lives, regardless of what now occupies that path. This test asserts
// exactly that guarantee: the write succeeds and its bytes land inside the
// ORIGINAL root directory (found at its new, moved-aside location), and
// nothing is ever written into the attacker's directory.
func TestWrite_RootItselfSwappedForSymlinkAfterResolve(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	attacker := t.TempDir()

	target, err := Resolve([]string{root}, filepath.Join(root, "f.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if target.Handle == nil {
		t.Fatal("Resolve() returned a Target with a nil Handle")
	}
	defer func() { _ = target.Close() }()

	// Attacker/race window: rename the real root aside and drop a symlink to
	// an attacker-controlled directory at the root's former path — AFTER
	// Resolve returned (i.e. after Resolve's identity check and os.OpenRoot
	// already succeeded against the real directory).
	movedAside := filepath.Join(base, "root-moved-aside")
	if err := os.Rename(root, movedAside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attacker, root); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	if _, err := Write(target, []byte("payload"), 0, false); err != nil {
		t.Fatalf("Write() error = %v, want nil — the already-open handle must keep writing to the original directory regardless of the later swap at its path", err)
	}

	// Nothing must have landed in the attacker's directory.
	attackerEntries, err := os.ReadDir(attacker)
	if err != nil {
		t.Fatal(err)
	}
	if len(attackerEntries) != 0 {
		t.Fatalf("write escaped into the attacker's directory via the swapped root path: %v", attackerEntries)
	}

	// The write must have landed in the ORIGINAL directory (now living at
	// movedAside — os.Root's own doc comment: "If the directory is moved,
	// methods on Root reference the original directory in its new location").
	got, err := os.ReadFile(filepath.Join(movedAside, "f.txt"))
	if err != nil {
		t.Fatalf("expected the write to land in the original (moved) root directory: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("file content = %q, want %q", got, "payload")
	}
}

// TestWrite_RootCreatedWhenMissing pins a configured root that does not
// exist yet still working end-to-end — Resolve creates it (os.MkdirAll)
// before opening it, rather than accepting the (Resolve-time-valid) path and
// then failing inside Write with a raw "no such file or directory" once
// os.OpenRoot discovers the directory still doesn't exist.
func TestWrite_RootCreatedWhenMissing(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "not-yet-created")

	target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil (root not existing yet must not be rejected)", err)
	}
	defer func() { _ = target.Close() }()

	if _, err := Write(target, []byte("payload"), 0, false); err != nil {
		t.Fatalf("Write() error = %v, want nil — Resolve must have created the root directory", err)
	}

	fi, err := os.Stat(root)
	if err != nil {
		t.Fatalf("expected root directory to have been created: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("root path exists but is not a directory")
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("root dir mode = %v, want 0700", fi.Mode().Perm())
	}

	got, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Errorf("file content = %q, want %q", got, "payload")
	}
}

// TestWrite_RootCreatedWhenMissingUnderSymlinkedAncestor pins the same
// not-yet-created-root scenario as TestWrite_RootCreatedWhenMissing, but
// under a symlinked ancestor (the real-world macOS /tmp -> /private/tmp
// case, mirrored by TestResolve_RootMissingUnderSymlinkedAncestor above,
// which only pins Resolve) — the root must be created AND written to
// successfully end-to-end.
func TestWrite_RootCreatedWhenMissingUnderSymlinkedAncestor(t *testing.T) {
	real := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	root := filepath.Join(link, "out") // does not exist yet
	target, err := Resolve([]string{root}, filepath.Join(root, "f.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	defer func() { _ = target.Close() }()

	if _, err := Write(target, []byte("payload"), 0, false); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	wantReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(wantReal, "out", "f.txt"))
	if err != nil {
		t.Fatalf("expected the file under the real (symlink-resolved) ancestor: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("file content = %q, want %q", got, "payload")
	}
	fi, err := os.Stat(filepath.Join(wantReal, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("created root dir mode = %v, want 0700", fi.Mode().Perm())
	}
}

// TestWrite_ErrorsWrapSentinels pins the sentinel-wrapping contract: a
// caller must be able to tell "the root is unavailable" (ErrRootUnavailable)
// apart from "the write itself failed" (ErrWriteFailed) via errors.Is,
// without the underlying *PathError text being the only signal.
func TestWrite_ErrorsWrapSentinels(t *testing.T) {
	root := t.TempDir()
	target, err := Resolve([]string{root}, filepath.Join(root, "out.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// Close the handle early to force every subsequent Write filesystem call
	// through it to fail — a closed *os.Root's operations return errors, and
	// none of them are ErrExists/ErrTooLarge, so this exercises the generic
	// ErrWriteFailed wrapping path deterministically.
	if err := target.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Write(target, []byte("payload"), 0, false)
	if err == nil {
		t.Fatal("Write() error = nil, want an error from the closed handle")
	}
	if !errors.Is(err, ErrWriteFailed) {
		t.Fatalf("Write() error = %v, want errors.Is(err, ErrWriteFailed)", err)
	}
	if errors.Is(err, ErrExists) || errors.Is(err, ErrTooLarge) {
		t.Fatalf("Write() error = %v unexpectedly also matches ErrExists/ErrTooLarge", err)
	}

	// Target.Handle == nil is the other ErrRootUnavailable source in Write —
	// pin it directly (a hand-built Target that didn't come from Resolve).
	_, err = Write(Target{Path: filepath.Join(root, "other.txt"), Root: root, Rel: "other.txt"}, []byte("x"), 0, false)
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Write() error = %v, want errors.Is(err, ErrRootUnavailable) for a nil Handle", err)
	}
}
