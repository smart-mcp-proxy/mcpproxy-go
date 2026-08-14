package codescripts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScript writes a script file into dir and returns its path.
func writeScript(t *testing.T, dir, filename, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// traversalCorpus is the SC-003 corpus: every entry must be rejected as an
// invalid NAME, before any filesystem access happens.
var traversalCorpus = []struct {
	name  string
	value string
}{
	{"empty", ""},
	{"dot", "."},
	{"dotdot", ".."},
	{"relative traversal", "../etc/passwd"},
	{"relative traversal windows separators", "..\\..\\windows\\win.ini"},
	{"absolute unix path", "/etc/passwd"},
	{"absolute windows path", `C:\Windows\win.ini`},
	{"forward separator", "sub/script"},
	{"backslash separator", `sub\script`},
	{"leading dot name", ".hidden"},
	{"name with extension", "fetch-prs.js"},
	{"dot segment inside", "a/./b"},
	{"unicode letters", "scrïpt"},
	{"unicode homoglyph separator", "a\u2044b"},
	{"space", "fetch prs"},
	{"nul byte", "fetch\x00prs"},
	{"colon", "stream:name"},
	{"tilde home", "~/script"},
	{"url encoded traversal", "%2e%2e%2fscript"},
	{"too long", strings.Repeat("a", MaxNameLen+1)},
	{"newline", "fetch\nprs"},
	{"glob", "fetch*"},
}

// TestValidateName_TraversalCorpus proves the name validator — the confinement
// boundary (FR-003 / SC-003) — rejects every traversal-shaped value and accepts
// only the documented token.
func TestValidateName_TraversalCorpus(t *testing.T) {
	for _, tc := range traversalCorpus {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.value)
			require.Error(t, err, "value %q must be rejected", tc.value)
			var invalid *InvalidNameError
			require.True(t, errors.As(err, &invalid), "want *InvalidNameError, got %T: %v", err, err)
		})
	}

	valid := []string{
		"a",
		"fetch-prs",
		"fetch_prs",
		"Fetch2PRs",
		"0",
		"-leading-hyphen",
		"_leading_underscore",
		strings.Repeat("a", MaxNameLen),
	}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			require.NoError(t, ValidateName(name))
		})
	}
}

// TestResolve_ValidatesNameBeforeFilesystemAccess is the SC-003 ordering proof:
// with a scripts directory that does not exist (so ANY filesystem probe would
// report "not found"), an invalid name still comes back as an invalid-NAME
// error — the validator ran first. A valid name against the same directory
// yields NotFound, showing the filesystem is reached only after validation.
func TestResolve_ValidatesNameBeforeFilesystemAccess(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "no-such-dir")

	for _, tc := range traversalCorpus {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Resolve(missingDir, tc.value, "")
			require.Error(t, err)
			var invalid *InvalidNameError
			require.True(t, errors.As(err, &invalid),
				"invalid name %q must be rejected before any filesystem access; got %T: %v", tc.value, err, err)
		})
	}

	_, _, err := Resolve(missingDir, "valid-name", "")
	var notFound *NotFoundError
	require.True(t, errors.As(err, &notFound), "valid name against a missing dir must reach the filesystem: %v", err)
	assert.Equal(t, 0, notFound.Total)

	_, statErr := os.Stat(missingDir)
	assert.True(t, os.IsNotExist(statErr), "resolution must never create the scripts directory")
}

func TestResolve_JavaScriptAndTypeScript(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "fetch-prs.js", "({ok: true})")
	writeScript(t, dir, "typed.ts", "const x: number = 1; ({x})")

	src, lang, err := Resolve(dir, "fetch-prs", "")
	require.NoError(t, err)
	assert.Equal(t, "({ok: true})", string(src))
	assert.Equal(t, LanguageJavaScript, lang)

	src, lang, err = Resolve(dir, "typed", "")
	require.NoError(t, err)
	assert.Equal(t, "const x: number = 1; ({x})", string(src))
	assert.Equal(t, LanguageTypeScript, lang)
}

func TestResolve_ExplicitLanguage(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "typed.ts", "const x: number = 1")
	writeScript(t, dir, "plain.js", "1")

	t.Run("agreeing explicit language is accepted", func(t *testing.T) {
		_, lang, err := Resolve(dir, "typed", LanguageTypeScript)
		require.NoError(t, err)
		assert.Equal(t, LanguageTypeScript, lang)
	})

	t.Run("contradicting explicit language is rejected", func(t *testing.T) {
		_, _, err := Resolve(dir, "typed", LanguageJavaScript)
		require.Error(t, err)
		var mismatch *LanguageMismatchError
		require.True(t, errors.As(err, &mismatch), "want *LanguageMismatchError, got %T: %v", err, err)
		assert.Equal(t, LanguageTypeScript, mismatch.Derived)
		assert.Equal(t, LanguageJavaScript, mismatch.Requested)
	})

	t.Run("contradicting explicit language on a .js script is rejected", func(t *testing.T) {
		_, _, err := Resolve(dir, "plain", LanguageTypeScript)
		var mismatch *LanguageMismatchError
		require.True(t, errors.As(err, &mismatch), "want *LanguageMismatchError, got %T: %v", err, err)
	})

	t.Run("unknown explicit language is rejected", func(t *testing.T) {
		_, _, err := Resolve(dir, "plain", "python")
		var mismatch *LanguageMismatchError
		require.True(t, errors.As(err, &mismatch), "want *LanguageMismatchError, got %T: %v", err, err)
	})
}

// TestResolve_NotFoundListsAvailable pins FR-004: the not-found error is the
// MCP discovery mechanism — first 20 ok names alphabetically plus the total.
func TestResolve_NotFoundListsAvailable(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		writeScript(t, dir, fmt.Sprintf("script-%02d.js", i), "1")
	}
	// Noise that must not be counted as available.
	writeScript(t, dir, "broken.js", "")
	writeScript(t, dir, "not a token.js", "1")
	writeScript(t, dir, "readme.md", "docs")

	_, _, err := Resolve(dir, "missing", "")
	require.Error(t, err)
	var notFound *NotFoundError
	require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)

	require.Len(t, notFound.Available, MaxErrorNames)
	assert.Equal(t, 25, notFound.Total, "only ok scripts count as available")
	want := make([]string, 0, MaxErrorNames)
	for i := 0; i < MaxErrorNames; i++ {
		want = append(want, fmt.Sprintf("script-%02d", i))
	}
	assert.Equal(t, want, notFound.Available, "available names are alphabetical")

	msg := err.Error()
	assert.Contains(t, msg, "missing")
	assert.Contains(t, msg, "script-00")
	assert.Contains(t, msg, "25")
	assert.NotContains(t, msg, "broken", "invalid scripts are not advertised as available")
}

func TestResolve_NotFoundEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	_, _, err := Resolve(dir, "missing", "")
	var notFound *NotFoundError
	require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
	assert.Empty(t, notFound.Available)
	assert.Equal(t, 0, notFound.Total)
	assert.Contains(t, err.Error(), "no stored scripts")
}

func TestResolve_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	jsPath := writeScript(t, dir, "dup.js", "1")
	tsPath := writeScript(t, dir, "dup.ts", "1")

	_, _, err := Resolve(dir, "dup", "")
	var ambiguous *AmbiguousError
	require.True(t, errors.As(err, &ambiguous), "want *AmbiguousError, got %T: %v", err, err)
	assert.Equal(t, []string{jsPath, tsPath}, ambiguous.Paths)
}

func TestResolve_EmptyAndOversized(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "empty.js", "")
	writeScript(t, dir, "at-limit.js", strings.Repeat("a", MaxSizeBytes))
	writeScript(t, dir, "over-limit.js", strings.Repeat("a", MaxSizeBytes+1))

	t.Run("empty", func(t *testing.T) {
		_, _, err := Resolve(dir, "empty", "")
		var invalid *InvalidError
		require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
		assert.Equal(t, ReasonEmpty, invalid.Reason)
	})

	t.Run("exactly at the limit is accepted", func(t *testing.T) {
		src, _, err := Resolve(dir, "at-limit", "")
		require.NoError(t, err)
		assert.Len(t, src, MaxSizeBytes)
	})

	t.Run("one byte over the limit is rejected", func(t *testing.T) {
		_, _, err := Resolve(dir, "over-limit", "")
		var invalid *InvalidError
		require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
		assert.Equal(t, ReasonOversized, invalid.Reason)
	})
}

func TestResolve_Unreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions are not enforced")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "secret.js", "1")
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, _, err := Resolve(dir, "secret", "")
	var invalid *InvalidError
	require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
	assert.Equal(t, ReasonUnreadable, invalid.Reason)
}

func TestResolve_Directory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "adir.js"), 0o755))

	_, _, err := Resolve(dir, "adir", "")
	var invalid *InvalidError
	require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
	assert.Equal(t, ReasonNonRegular, invalid.Reason)
}

// mustSymlink creates a symlink, skipping the test only when the platform
// refuses for privilege reasons (unprivileged Windows). Never build-tagged
// away: a symlink test that silently vanishes proves nothing.
func mustSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation requires elevation on this Windows host: %v", err)
		}
		require.NoError(t, err)
	}
}

// TestResolve_SymlinkRejected covers FR-003's non-regular rejection for the
// three shapes that matter: a link escaping the scripts dir, a link staying
// inside it, and (on Windows) a directory reparse point.
func TestResolve_SymlinkRejected(t *testing.T) {
	t.Run("symlink escaping the scripts directory", func(t *testing.T) {
		outside := t.TempDir()
		target := writeScript(t, outside, "outside.js", "({escaped: true})")
		dir := t.TempDir()
		mustSymlink(t, target, filepath.Join(dir, "escape.js"))

		_, _, err := Resolve(dir, "escape", "")
		var invalid *InvalidError
		require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
		assert.Equal(t, ReasonNonRegular, invalid.Reason)
	})

	t.Run("symlink inside the scripts directory", func(t *testing.T) {
		dir := t.TempDir()
		target := writeScript(t, dir, "real.js", "({real: true})")
		mustSymlink(t, target, filepath.Join(dir, "alias.js"))

		_, _, err := Resolve(dir, "alias", "")
		var invalid *InvalidError
		require.True(t, errors.As(err, &invalid), "an in-directory symlink is still not a regular file; got %T: %v", err, err)
		assert.Equal(t, ReasonNonRegular, invalid.Reason)

		// The real file next to it stays resolvable.
		src, _, err := Resolve(dir, "real", "")
		require.NoError(t, err)
		assert.Equal(t, "({real: true})", string(src))
	})

	t.Run("directory reparse point", func(t *testing.T) {
		outside := t.TempDir()
		writeScript(t, outside, "inner.js", "1")
		dir := t.TempDir()
		mustSymlink(t, outside, filepath.Join(dir, "linkdir"))

		// The link is a directory, so no <name>.js candidate exists under a
		// token-valid name; resolution must not walk through it.
		_, _, err := Resolve(dir, "linkdir", "")
		var notFound *NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
	})
}

// TestResolve_Freshness pins FR-009: an atomic replacement is picked up by the
// very next resolution, with nothing to invalidate.
func TestResolve_Freshness(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "hot.js", "({v: 1})")

	src, _, err := Resolve(dir, "hot", "")
	require.NoError(t, err)
	assert.Equal(t, "({v: 1})", string(src))

	staging := filepath.Join(t.TempDir(), "hot.js.tmp")
	require.NoError(t, os.WriteFile(staging, []byte("({v: 2})"), 0o644))
	require.NoError(t, os.Rename(staging, filepath.Join(dir, "hot.js")))

	src, _, err = Resolve(dir, "hot", "")
	require.NoError(t, err)
	assert.Equal(t, "({v: 2})", string(src), "an atomic replacement must be visible on the next resolution")

	require.NoError(t, os.Remove(filepath.Join(dir, "hot.js")))
	_, _, err = Resolve(dir, "hot", "")
	var notFound *NotFoundError
	require.True(t, errors.As(err, &notFound), "a removed script must stop resolving; got %T: %v", err, err)
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	okPath := writeScript(t, dir, "alpha.js", "1")
	tsPath := writeScript(t, dir, "beta.ts", "1")
	dupJS := writeScript(t, dir, "dup.js", "1")
	dupTS := writeScript(t, dir, "dup.ts", "1")
	writeScript(t, dir, "empty.js", "")
	writeScript(t, dir, "huge.js", strings.Repeat("a", MaxSizeBytes+1))
	writeScript(t, dir, "notes.md", "docs")    // unsupported extension
	writeScript(t, dir, "UPPER.JS", "1")       // extensions are lowercase only
	writeScript(t, dir, "not a token.js", "1") // not a token-valid name
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o755))

	entries, err := List(dir)
	require.NoError(t, err)

	byName := map[string]Entry{}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
		names = append(names, e.Name)
	}
	assert.Equal(t, []string{"alpha", "beta", "dup", "empty", "huge"}, names,
		"only token-valid .js/.ts entries are listed, alphabetically")

	assert.Equal(t, Entry{Name: "alpha", Paths: []string{okPath}, Status: StatusOK}, byName["alpha"])
	assert.Equal(t, Entry{Name: "beta", Paths: []string{tsPath}, Status: StatusOK}, byName["beta"])
	assert.Equal(t, Entry{Name: "dup", Paths: []string{dupJS, dupTS}, Status: StatusAmbiguous}, byName["dup"])
	assert.Equal(t, StatusInvalid, byName["empty"].Status)
	assert.Equal(t, ReasonEmpty, byName["empty"].Reason)
	assert.Equal(t, StatusInvalid, byName["huge"].Status)
	assert.Equal(t, ReasonOversized, byName["huge"].Reason)
}

func TestList_MissingAndEmptyDirectory(t *testing.T) {
	t.Run("absent directory yields an empty list, not an error", func(t *testing.T) {
		entries, err := List(filepath.Join(t.TempDir(), "no-such-dir"))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("empty directory yields an empty list", func(t *testing.T) {
		entries, err := List(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("empty scripts dir path yields an empty list", func(t *testing.T) {
		entries, err := List("")
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestList_SymlinkEntryIsNotOK(t *testing.T) {
	dir := t.TempDir()
	target := writeScript(t, dir, "real.js", "1")
	mustSymlink(t, target, filepath.Join(dir, "alias.js"))

	entries, err := List(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name == "alias" {
			assert.Equal(t, StatusInvalid, e.Status)
			assert.Equal(t, ReasonNonRegular, e.Reason)
			return
		}
	}
	t.Fatalf("alias entry missing from listing: %+v", entries)
}

func TestDeriveLanguage(t *testing.T) {
	tests := []struct {
		ext      string
		explicit string
		want     string
		wantErr  bool
	}{
		{".js", "", LanguageJavaScript, false},
		{".ts", "", LanguageTypeScript, false},
		{".js", LanguageJavaScript, LanguageJavaScript, false},
		{".ts", LanguageTypeScript, LanguageTypeScript, false},
		{".js", LanguageTypeScript, "", true},
		{".ts", LanguageJavaScript, "", true},
		{".ts", "ruby", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.ext+"/"+tc.explicit, func(t *testing.T) {
			got, err := DeriveLanguage("some-script", tc.ext, tc.explicit)
			if tc.wantErr {
				require.Error(t, err)
				var mismatch *LanguageMismatchError
				assert.True(t, errors.As(err, &mismatch), "want *LanguageMismatchError, got %T", err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDirFor(t *testing.T) {
	assert.Equal(t,
		filepath.Join("/home", "u", ".mcpproxy", "scripts"),
		DirFor(filepath.Join("/home", "u", ".mcpproxy", "mcp_config.json")))
	assert.Empty(t, DirFor(""), "no config path means no scripts directory")
}
