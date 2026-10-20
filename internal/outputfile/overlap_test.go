package outputfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolve_NestedRootSymlinkEscapeRejected pins the escape found in the
// Spec 076 security review: with roots "a" and "a/sub" configured, anything
// able to write inside "a" can plant a symlink at the (not-yet-created)
// inner root; that root then RESOLVES to the symlink target and a save
// under the target used to be accepted. Overlapping roots now fail closed.
func TestResolve_NestedRootSymlinkEscapeRejected(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(a, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(a, "sub")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	roots := []string{a, filepath.Join(a, "sub")}

	for _, requested := range []string{
		filepath.Join(outside, "pwned.txt"),
		filepath.Join(a, "sub", "pwned.txt"),
		filepath.Join(a, "ok.txt"),
	} {
		tg, err := Resolve(roots, requested, false)
		if err == nil {
			_ = tg.Close()
			t.Fatalf("Resolve(%q) succeeded with overlapping roots, want ErrRootUnavailable", requested)
		}
		if !errors.Is(err, ErrRootUnavailable) {
			t.Fatalf("Resolve(%q) error = %v, want ErrRootUnavailable", requested, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("file written outside roots: stat err = %v", err)
	}
}

// TestResolve_OverlapOnlyAfterResolutionRejected: two roots that look
// disjoint literally but resolve into each other (one is a symlink into the
// other) are also refused.
func TestResolve_OverlapOnlyAfterResolutionRejected(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	if err := os.MkdirAll(filepath.Join(outer, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(outer, "inner"), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Resolve([]string{outer, alias}, filepath.Join(outer, "f.txt"), false)
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
}

// TestResolve_AliasOverlapRejected: roots that overlap only through a
// symlinked ancestor ("out" and "alias/sub" with alias -> out), with a
// symlink planted at out/sub, look disjoint both literally and fully
// resolved. The parent-resolved comparison must still refuse them.
func TestResolve_AliasOverlapRejected(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "out")
	escape := filepath.Join(base, "escape")
	for _, d := range []string{out, escape} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(out, filepath.Join(base, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(escape, filepath.Join(out, "sub")); err != nil {
		t.Fatal(err)
	}
	roots := []string{out, filepath.Join(base, "alias", "sub")}
	tg, err := Resolve(roots, filepath.Join(base, "alias", "sub", "f.txt"), false)
	if err == nil {
		_ = tg.Close()
		t.Fatal("Resolve succeeded with alias-overlapping roots")
	}
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
	if _, statErr := os.Stat(filepath.Join(escape, "f.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file written outside roots: %v", statErr)
	}
}

// TestResolve_DeepAliasOverlapRejected: same class as above, with the
// planted symlink one level deeper ("out" and "alias/x/sub", alias -> out,
// out/x -> escape), so even the parent-resolved form of the second root
// looks disjoint. The per-prefix check must still refuse it.
func TestResolve_DeepAliasOverlapRejected(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "out")
	escape := filepath.Join(base, "escape")
	for _, d := range []string{out, filepath.Join(escape, "sub")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(out, filepath.Join(base, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(escape, filepath.Join(out, "x")); err != nil {
		t.Fatal(err)
	}
	roots := []string{out, filepath.Join(base, "alias", "x", "sub")}
	tg, err := Resolve(roots, filepath.Join(base, "alias", "x", "sub", "f.txt"), false)
	if err == nil {
		_ = tg.Close()
		t.Fatal("Resolve succeeded with deep alias-overlapping roots")
	}
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
	if _, statErr := os.Stat(filepath.Join(escape, "sub", "f.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file written outside roots: %v", statErr)
	}
}

// TestResolve_LinkIntoRootOverlapRejected: an admin link that points INTO
// another root (l -> out/x) with a symlink planted at out/x -> escape. A
// one-shot resolution of "l" jumps straight to "escape"; the manual walk
// sees the hop through out/x and refuses the overlap.
func TestResolve_LinkIntoRootOverlapRejected(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "out")
	escape := filepath.Join(base, "escape")
	for _, d := range []string{out, filepath.Join(escape, "sub")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(out, "x"), filepath.Join(base, "l")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(escape, filepath.Join(out, "x")); err != nil {
		t.Fatal(err)
	}
	roots := []string{out, filepath.Join(base, "l", "sub")}
	tg, err := Resolve(roots, filepath.Join(base, "l", "sub", "f.txt"), false)
	if err == nil {
		_ = tg.Close()
		t.Fatal("Resolve succeeded with a root linked through another root")
	}
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
	if _, statErr := os.Stat(filepath.Join(escape, "sub", "f.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file written outside roots: %v", statErr)
	}
}

// TestResolve_SiblingRootsUnderCommonAncestorAccepted guards the per-prefix
// check against false positives: a shared ancestor directory is not overlap.
func TestResolve_SiblingRootsUnderCommonAncestorAccepted(t *testing.T) {
	base := t.TempDir()
	r1 := filepath.Join(base, "a", "out")
	r2 := filepath.Join(base, "a", "out2")
	for _, d := range []string{r1, r2} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tg, err := Resolve([]string{r1, r2}, filepath.Join(r1, "f.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	_ = tg.Close()
}

func TestPathsOverlap_CaseFoldOnCaseInsensitivePlatforms(t *testing.T) {
	a := string(os.PathSeparator) + filepath.Join("x", "out")
	b := string(os.PathSeparator) + filepath.Join("x", "OUT", "sub")
	want := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	if got := pathsOverlap(a, b); got != want {
		t.Fatalf("pathsOverlap(%q, %q) = %v, want %v on %s", a, b, got, want, runtime.GOOS)
	}
}

func TestResolve_DisjointRootsStillWork(t *testing.T) {
	base := t.TempDir()
	r1 := filepath.Join(base, "out")
	r2 := filepath.Join(base, "out2") // shares a string prefix, not a path prefix
	tg, err := Resolve([]string{r1, r2}, filepath.Join(r2, "f.txt"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	_ = tg.Close()
}

func TestPathsOverlap(t *testing.T) {
	sep := string(os.PathSeparator)
	p := func(parts ...string) string { return sep + filepath.Join(parts...) }
	cases := []struct {
		a, b string
		want bool
	}{
		{p("data", "out"), p("data", "out"), true},
		{p("data", "out"), p("data", "out", "sub"), true},
		{p("data", "out", "sub"), p("data", "out"), true},
		{p("data", "out"), p("data", "out2"), false},
		{p("data", "out"), p("data", "other"), false},
	}
	for _, c := range cases {
		if got := pathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestVolumeAllowed pins the UNC / device-path pre-check: on Windows such a
// path is refused before any filesystem call unless a root shares its
// volume; on Unix VolumeName is always "" so every path passes through to
// the normal root match.
func TestVolumeAllowed(t *testing.T) {
	if runtime.GOOS != "windows" {
		if !volumeAllowed(`/tmp/x`, []string{"/tmp"}) {
			t.Fatal("unix path must pass the volume pre-check")
		}
		if !volumeAllowed(`//host/share/x`, []string{"/tmp"}) {
			t.Fatal("unix has no UNC volumes; the root match decides")
		}
		return
	}
	roots := []string{`C:\out`}
	for _, p := range []string{
		`\\attacker\share\x.txt`, `\\.\pipe\x`, `\\?\C:\out\x.txt`, `//attacker/share/x`,
		`\??\UNC\h\s\x`, `/??/UNC/h/s/x`, `\??\GLOBALROOT\Device\Mup\h\s`,
	} {
		if volumeAllowed(filepath.Clean(p), roots) {
			t.Errorf("volumeAllowed(%q) = true, want false", p)
		}
		if _, err := Resolve(roots, p, false); !errors.Is(err, ErrOutsideRoots) && !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Resolve(%q) error = %v, want ErrOutsideRoots", p, err)
		}
	}
	if !volumeAllowed(`C:\out\x.txt`, roots) {
		t.Error("drive-letter path must pass the volume pre-check")
	}
	if !volumeAllowed(`\\fs\share\out\x.txt`, []string{`\\fs\share\out`}) {
		t.Error("UNC path on a root's own volume must pass the volume pre-check")
	}
}
