package scanner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestParsePackageSpec(t *testing.T) {
	cases := []struct {
		spec        string
		wantName    string
		wantVersion string
	}{
		{"google-flights-mcp-server@0.2.4", "google-flights-mcp-server", "0.2.4"},
		{"@modelcontextprotocol/server-everything@1.0.0", "@modelcontextprotocol/server-everything", "1.0.0"},
		{"@modelcontextprotocol/server-everything", "@modelcontextprotocol/server-everything", ""},
		{"plain-package", "plain-package", ""},
		{"some-pkg==2.3.1", "some-pkg", "2.3.1"},
		{"some-pkg>=2.0", "some-pkg", ""}, // only exact pins are honored
		{"", "", ""},
	}
	for _, c := range cases {
		name, version := parsePackageSpec(c.spec)
		if name != c.wantName || version != c.wantVersion {
			t.Errorf("parsePackageSpec(%q) = (%q, %q), want (%q, %q)",
				c.spec, name, version, c.wantName, c.wantVersion)
		}
	}
}

func TestRunnerPackageSpec(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
		want    string
	}{
		// npx: -y is a boolean flag, the package follows.
		{"npx -y", "npx", []string{"-y", "google-flights-mcp-server@0.2.4"}, "google-flights-mcp-server@0.2.4"},
		{"npx bare", "npx", []string{"server-everything"}, "server-everything"},
		// npx --package / -p NAMES the target; -c carries a command string.
		{"npx -p", "npx", []string{"-p", "@scope/srv@1.2.3", "-c", "srv"}, "@scope/srv@1.2.3"},
		{"npx --package", "npx", []string{"--package", "srv", "-c", "run-srv"}, "srv"},
		// Subcommand runners: the subcommand keyword is NOT the package.
		{"pipx run", "pipx", []string{"run", "some-pkg"}, "some-pkg"},
		{"pnpm dlx", "pnpm", []string{"dlx", "some-pkg"}, "some-pkg"},
		{"yarn dlx", "yarn", []string{"dlx", "some-pkg@2.0.0"}, "some-pkg@2.0.0"},
		{"bun x", "bun", []string{"x", "some-pkg"}, "some-pkg"},
		// uvx: --from names the distribution; a positional after it is the command.
		{"uvx --from", "uvx", []string{"--from", "pkg-a", "cmd"}, "pkg-a"},
		{"uvx bare", "uvx", []string{"pkg-only"}, "pkg-only"},
		// uvx --with adds an EXTRA dep; the target is the first real positional.
		{"uvx --with then target", "uvx", []string{"--with", "extra-dep", "main-pkg"}, "main-pkg"},
		{"uvx --with then --from", "uvx", []string{"--with", "extra-dep", "--from", "main-pkg", "cmd"}, "main-pkg"},
		// uvx -p/--python carries a version, NOT the package.
		{"uvx -p python", "uvx", []string{"-p", "3.11", "main-pkg"}, "main-pkg"},
		{"uvx --python", "uvx", []string{"--python", "3.12", "--from", "main-pkg", "cmd"}, "main-pkg"},
		// Degenerate inputs.
		{"npx flag only", "npx", []string{"-y"}, ""},
		{"nil", "npx", nil, ""},
		{"pipx run only", "pipx", []string{"run"}, ""},
	}
	for _, c := range cases {
		got := runnerPackageSpec(filepath.Base(c.command), c.args)
		if got != c.want {
			t.Errorf("%s: runnerPackageSpec(%q, %v) = %q, want %q", c.name, c.command, c.args, got, c.want)
		}
	}
}

// writeTarGz builds an in-memory .tar.gz from a map of relative path -> contents.
func writeTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarballGz_Normal(t *testing.T) {
	// npm tarballs wrap everything under a top-level "package/" dir.
	data := writeTarGz(t, map[string]string{
		"package/index.js":     "console.log('hi')",
		"package/package.json": `{"name":"x"}`,
		"package/lib/util.js":  "module.exports = {}",
	})
	dest := t.TempDir()
	if err := extractTarballGz(bytes.NewReader(data), dest, 1000, 10<<20, fetchMaxFileBytes); err != nil {
		t.Fatalf("extractTarballGz: %v", err)
	}
	for _, rel := range []string{"package/index.js", "package/package.json", "package/lib/util.js"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("expected extracted file %q: %v", rel, err)
		}
	}
}

func TestExtractTarballGz_RejectsZipSlip(t *testing.T) {
	data := writeTarGz(t, map[string]string{
		"package/ok.js":      "ok",
		"../../etc/evil.txt": "pwned",
	})
	dest := t.TempDir()
	// Extraction should not error fatally on the bad entry but MUST skip it.
	_ = extractTarballGz(bytes.NewReader(data), dest, 1000, 10<<20, fetchMaxFileBytes)
	escaped := filepath.Join(filepath.Dir(filepath.Dir(dest)), "etc", "evil.txt")
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("zip-slip: file escaped to %s", escaped)
	}
	// safeJoin must REJECT the traversal entry, not rewrite it into an in-dest
	// path (the MCP-2444 bug): the rewritten location must not exist either.
	rewritten := filepath.Join(dest, "etc", "evil.txt")
	if _, err := os.Stat(rewritten); err == nil {
		t.Fatalf("traversal entry was rewritten into dest at %s instead of being rejected", rewritten)
	}
	// The legitimate entry should still be present.
	if _, err := os.Stat(filepath.Join(dest, "package", "ok.js")); err != nil {
		t.Errorf("legit entry missing: %v", err)
	}
}

func TestExtractTarballGz_RejectsSymlinkEscape(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	// A symlink pointing outside the dest is an escape vector.
	hdr := &tar.Header{Name: "package/evil-link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()
	dest := t.TempDir()
	_ = extractTarballGz(bytes.NewReader(buf.Bytes()), dest, 1000, 10<<20, fetchMaxFileBytes)
	link := filepath.Join(dest, "package", "evil-link")
	if _, err := os.Lstat(link); err == nil {
		t.Fatalf("symlink entry should have been skipped, found %s", link)
	}
}

func TestExtractTarballGz_SizeCap(t *testing.T) {
	data := writeTarGz(t, map[string]string{
		"package/big.bin": strings.Repeat("A", 1024),
	})
	dest := t.TempDir()
	if err := extractTarballGz(bytes.NewReader(data), dest, 1000, 100, fetchMaxFileBytes); err == nil {
		t.Fatal("expected size-cap error, got nil")
	}
}

// tarItem is an ordered tar entry (the map-based writeTarGz can't control order
// or emit directory/non-regular entries).
type tarItem struct {
	name     string
	body     string
	typeflag byte
}

// writeTarGzOrdered builds an in-memory .tar.gz from ordered entries.
func writeTarGzOrdered(t *testing.T, items []tarItem) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, it := range items {
		hdr := &tar.Header{Name: it.name, Mode: 0o644, Size: int64(len(it.body)), Typeflag: it.typeflag}
		if it.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if len(it.body) > 0 {
			if _, err := tw.Write([]byte(it.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// MCP-2444 bug #2: safeJoin must REJECT path-traversal / absolute entries with an
// error, never sanitize/rewrite them into an in-dest path.
func TestSafeJoin_RejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	reject := []string{
		"../escape",
		"../../etc/passwd",
		"a/../../b",
		"/etc/passwd",
		"package/../../escape",
	}
	for _, name := range reject {
		if got, err := safeJoin(dest, name); err == nil {
			t.Errorf("safeJoin(%q) = %q, want error (path traversal must be rejected)", name, got)
		}
	}
	allow := []string{"package/index.js", "pkg/sub/file.py", "flights-0.2.4.dist-info/METADATA"}
	for _, name := range allow {
		if _, err := safeJoin(dest, name); err != nil {
			t.Errorf("safeJoin(%q) returned unexpected error: %v", name, err)
		}
	}
}

// MCP-2444 bug #1: total DECOMPRESSED bytes must be bounded across SKIPPED tar
// members — a gzip bomb made of oversized (therefore skipped) members must still
// abort at the decompressed-byte cap rather than being decompressed in full.
func TestExtractTarballGz_GzipBombSkippedMembers(t *testing.T) {
	const maxFileBytes = 1 << 10 // 1 KiB: every member below is oversized -> skipped
	const maxTotalBytes = 8 << 10
	var items []tarItem
	for i := 0; i < 16; i++ {
		// 4 KiB body (> maxFileBytes => skipped) of highly-compressible data;
		// 16 * 4 KiB = 64 KiB decompressed >> 8 KiB cap.
		items = append(items, tarItem{name: "package/big" + string(rune('a'+i)) + ".bin", body: strings.Repeat("A", 4<<10), typeflag: tar.TypeReg})
	}
	data := writeTarGzOrdered(t, items)
	dest := t.TempDir()
	if err := extractTarballGz(bytes.NewReader(data), dest, 100000, maxTotalBytes, maxFileBytes); err == nil {
		t.Fatal("expected decompression-bomb abort across skipped members, got nil")
	}
}

// MCP-2444 bug #3: oversized files must be CHARGED to the file-count cap (a
// truncated-but-large file still counts), not silently skipped without charge.
func TestExtractTarballGz_OversizedFileCharged(t *testing.T) {
	const maxFileBytes = 10
	items := []tarItem{
		{name: "package/a.bin", body: strings.Repeat("A", 100), typeflag: tar.TypeReg},
		{name: "package/b.bin", body: strings.Repeat("A", 100), typeflag: tar.TypeReg},
		{name: "package/c.bin", body: strings.Repeat("A", 100), typeflag: tar.TypeReg},
	}
	data := writeTarGzOrdered(t, items)
	dest := t.TempDir()
	// maxFiles=2; three oversized files must trip the count cap on the third.
	if err := extractTarballGz(bytes.NewReader(data), dest, 2, 10<<20, maxFileBytes); err == nil {
		t.Fatal("expected file-count cap to trip on oversized (charged) files, got nil")
	}
}

// MCP-2444 bug #4: directory creation must be capped (charged toward the entry
// limit) so an all-directories archive cannot bypass the file-count limit.
func TestExtractTarballGz_DirCap(t *testing.T) {
	var items []tarItem
	for i := 0; i < 8; i++ {
		items = append(items, tarItem{name: "package/d" + string(rune('a'+i)) + "/", typeflag: tar.TypeDir})
	}
	data := writeTarGzOrdered(t, items)
	dest := t.TempDir()
	if err := extractTarballGz(bytes.NewReader(data), dest, 3, 10<<20, fetchMaxFileBytes); err == nil {
		t.Fatal("expected directory-count cap to trip, got nil")
	}
}

// writeZipOrdered builds an in-memory .zip with ordered entries (dir entries use
// a trailing slash, matching the zip convention).
func writeZipOrdered(t *testing.T, names []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		if strings.HasSuffix(name, "/") {
			if _, err := zw.Create(name); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// MCP-2444 bug #4 (zip path): directory entries must be capped too.
func TestExtractZip_DirCap(t *testing.T) {
	names := []string{"da/", "db/", "dc/", "dd/", "de/"}
	zipPath := filepath.Join(t.TempDir(), "dirs.whl")
	if err := os.WriteFile(zipPath, writeZipOrdered(t, names), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractZip(zipPath, dest, 3, 10<<20, fetchMaxFileBytes); err == nil {
		t.Fatal("expected directory-count cap to trip in zip path, got nil")
	}
}

// writeZip builds an in-memory .zip (wheel) from a map of relative path -> contents.
func writeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZip_Normal(t *testing.T) {
	data := writeZip(t, map[string]string{
		"flights/__init__.py":              "import os",
		"flights/server.py":                "def main(): pass",
		"flights-0.2.4.dist-info/METADATA": "Name: flights",
	})
	zipPath := filepath.Join(t.TempDir(), "wheel.whl")
	if err := os.WriteFile(zipPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractZip(zipPath, dest, 1000, 10<<20, fetchMaxFileBytes); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "flights", "server.py")); err != nil {
		t.Errorf("expected extracted file: %v", err)
	}
}

func TestExtractZip_RejectsZipSlip(t *testing.T) {
	data := writeZip(t, map[string]string{
		"ok.py":              "ok",
		"../../etc/evil.txt": "pwned",
	})
	zipPath := filepath.Join(t.TempDir(), "evil.whl")
	if err := os.WriteFile(zipPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	_ = extractZip(zipPath, dest, 1000, 10<<20, fetchMaxFileBytes)
	escaped := filepath.Join(filepath.Dir(filepath.Dir(dest)), "etc", "evil.txt")
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("zip-slip: file escaped to %s", escaped)
	}
	// Reject, don't rewrite: the in-dest rewritten path must not exist either.
	if _, err := os.Stat(filepath.Join(dest, "etc", "evil.txt")); err == nil {
		t.Fatalf("traversal entry was rewritten into dest instead of being rejected")
	}
}

// The crux of the security design: the fetch step must NEVER execute the
// untrusted package's code. These guard tests assert that the constructed
// download command lines contain only download/pack verbs and explicitly opt
// out of lifecycle scripts / building.
func TestNpmPackArgs_NoExecution(t *testing.T) {
	args := npmPackArgs("google-flights-mcp-server@0.2.4", "/tmp/dest")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "pack") {
		t.Errorf("npm command must use 'pack': %v", args)
	}
	if !hasExactArg(args, "--ignore-scripts") {
		t.Errorf("npm pack must pass --ignore-scripts: %v", args)
	}
	for _, forbidden := range []string{"install", "ci", "rebuild", "exec", "run"} {
		if hasExactArg(args, forbidden) {
			t.Errorf("npm command must not contain %q: %v", forbidden, args)
		}
	}
}

func TestUvDownloadArgs_NoExecution(t *testing.T) {
	args := uvDownloadArgs("flights==0.2.4", "/tmp/dest")
	if !hasExactArg(args, "download") {
		t.Errorf("uv command must use 'download': %v", args)
	}
	if !hasExactArg(args, "--no-deps") {
		t.Errorf("uv download must pass --no-deps: %v", args)
	}
	for _, forbidden := range []string{"install", "sync", "build", "run", "setup.py"} {
		if hasExactArg(args, forbidden) {
			t.Errorf("uv command must not contain %q: %v", forbidden, args)
		}
	}
}

func TestPipDownloadArgs_NoExecution(t *testing.T) {
	args := pipDownloadArgs("flights==0.2.4", "/tmp/dest")
	if !hasExactArg(args, "download") {
		t.Errorf("pip command must use 'download': %v", args)
	}
	if !hasExactArg(args, "--no-deps") {
		t.Errorf("pip download must pass --no-deps: %v", args)
	}
	for _, forbidden := range []string{"install", "wheel", "setup.py"} {
		if hasExactArg(args, forbidden) {
			t.Errorf("pip command must not contain %q: %v", forbidden, args)
		}
	}
}

// MCP-2391: a `pip download` / `uv pip download` of an sdist runs the package's
// PEP 517 build backend (setup.py egg_info) to resolve metadata — i.e. it
// EXECUTES code from the package being scanned. --only-binary=:all: forces a
// wheel and makes the download FAIL (no extraction) when only an sdist exists,
// instead of silently building it. These positive assertions guard the flag so
// it cannot be dropped without a red test (TestUv/PipDownloadArgs_NoExecution
// only asserted ABSENCE of verbs, which let the original BLOCKER through).
func TestUvDownloadArgs_OnlyBinary(t *testing.T) {
	args := uvDownloadArgs("flights==0.2.4", "/tmp/dest")
	if !hasExactArg(args, "--only-binary=:all:") {
		t.Errorf("uv download MUST pass --only-binary=:all: to never build an sdist (code execution): %v", args)
	}
}

func TestPipDownloadArgs_OnlyBinary(t *testing.T) {
	args := pipDownloadArgs("flights==0.2.4", "/tmp/dest")
	if !hasExactArg(args, "--only-binary=:all:") {
		t.Errorf("pip download MUST pass --only-binary=:all: to never build an sdist (code execution): %v", args)
	}
}

func hasExactArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestFindDownloadedArchive(t *testing.T) {
	dir := t.TempDir()
	// npm pack writes a .tgz
	tgz := filepath.Join(dir, "pkg-0.2.4.tgz")
	os.WriteFile(tgz, []byte("x"), 0o644)
	got, kind, err := findDownloadedArchive(dir)
	if err != nil {
		t.Fatalf("findDownloadedArchive: %v", err)
	}
	if got != tgz || kind != archiveTarGz {
		t.Errorf("got (%q,%v), want (%q,tgz)", got, kind, tgz)
	}

	// wheel preferred over sdist when both present
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "flights-0.2.4.tar.gz"), []byte("x"), 0o644)
	whl := filepath.Join(dir2, "flights-0.2.4-py3-none-any.whl")
	os.WriteFile(whl, []byte("x"), 0o644)
	got2, kind2, err := findDownloadedArchive(dir2)
	if err != nil {
		t.Fatalf("findDownloadedArchive: %v", err)
	}
	if got2 != whl || kind2 != archiveZip {
		t.Errorf("wheel should be preferred: got (%q,%v)", got2, kind2)
	}
}

// MCP-2442: a `pip download` / `uv pip download` of a NON-REGISTRY spec
// (local path, git+, URL, file:, VCS, ssh) STILL executes the package's
// setup.py / PEP 517 build backend even with --only-binary=:all: — arbitrary
// code execution from untrusted server config on the (static) scan path. The
// fetch must validate the spec is a bare PEP 503 / npm registry name and REFUSE
// anything else, falling back to tool-definitions-only.
func TestValidatePackageSpec(t *testing.T) {
	cases := []struct {
		ecosystem string
		spec      string
		wantOK    bool
	}{
		// Registry names (allowed) — must not regress.
		{"python", "flights", true},
		{"python", "flights==0.2.4", true},
		{"python", "google-flights-mcp-server==0.2.4", true},
		{"python", "Flask", true},
		{"python", "some.dotted.name", true},
		{"npm", "google-flights-mcp-server", true},
		{"npm", "google-flights-mcp-server@0.2.4", true},
		{"npm", "@modelcontextprotocol/server-everything", true},
		{"npm", "@modelcontextprotocol/server-everything@1.0.0", true},
		// Non-registry specs (must be REJECTED → fall back to tool-defs).
		{"python", "./local-pkg", false},
		{"python", "../evil", false},
		{"python", "/abs/path/pkg", false},
		{"python", "~/pkg", false},
		{"python", "file:./pkg", false},
		{"python", "git+https://github.com/user/repo.git", false},
		{"python", "git+ssh://[email protected]/user/repo.git", false},
		{"python", "https://example.com/pkg.tar.gz", false},
		{"python", "[email protected]:user/repo.git", false},
		{"python", "hg+https://example.com/repo", false},
		{"python", `C:\Users\evil\pkg`, false},
		{"python", "", false},
		{"npm", "./local-pkg", false},
		{"npm", "/abs/path", false},
		{"npm", "git+https://github.com/user/repo.git", false},
		{"npm", "file:../evil", false},
		{"npm", "https://example.com/pkg.tgz", false},
		{"npm", "", false},
		// MCP-2442 re-review: PEP 508 / npm direct-reference '@'-tail bypass — the
		// NAME parses as a bare registry name but the tail is a path/URL/VCS, which
		// reaches pip/npm verbatim and STILL executes setup.py. The WHOLE spec
		// (including the tail after '@') must be validated.
		{"npm", "pkg@./local", false},
		{"npm", "pkg@/abs/path", false},
		{"npm", "pkg@~/evil", false},
		{"npm", "pkg@git+https://github.com/user/repo.git", false},
		{"npm", "pkg@file:./evil", false},
		{"npm", "pkg@https://example.com/pkg.tgz", false},
		{"npm", "@scope/pkg@./local", false},
		{"python", "pkg@./local", false},
		{"python", "pkg@/abs/path", false},
		{"python", "pkg@git+https://github.com/user/repo.git", false},
		{"python", "pkg@~/evil", false},
		{"python", "flights @ git+https://example.com/repo.git", false},
		// Bare version pins / dist-tags after '@' stay valid (no regression).
		{"npm", "pkg@1.2.3", true},
		{"npm", "pkg@1.2.3-beta.1", true},
		{"npm", "pkg@latest", true},
		{"npm", "@scope/pkg@2.0.0", true},
	}
	for _, c := range cases {
		err := validatePackageSpec(c.ecosystem, c.spec)
		if c.wantOK && err != nil {
			t.Errorf("validatePackageSpec(%q, %q) = %v, want OK", c.ecosystem, c.spec, err)
		}
		if !c.wantOK && err == nil {
			t.Errorf("validatePackageSpec(%q, %q) = nil, want rejection", c.ecosystem, c.spec)
		}
	}
}

// TestResolveFromPackageFetch_RejectsNonRegistrySpec proves the resolver refuses
// a non-registry uvx spec BEFORE invoking any download command — so the untrusted
// package's setup.py is never executed. We point the spec at a local directory
// containing a setup.py that drops an execution marker; the resolver must return
// an error (caller falls back to tool_definitions_only) and the marker must be
// absent.
func TestResolveFromPackageFetch_RejectsNonRegistrySpec(t *testing.T) {
	pkgDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "EXECUTED.marker")
	setupPy := "import pathlib\npathlib.Path(" + pyQuote(marker) + ").write_text('pwned')\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "setup.py"), []byte(setupPy), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewSourceResolver(zap.NewNop())
	cases := []ServerInfo{
		{Name: "evil-path", Protocol: "stdio", Command: "uvx", Args: []string{"--from", pkgDir, "evil"}},
		{Name: "evil-git", Protocol: "stdio", Command: "uvx", Args: []string{"--from", "git+https://example.com/repo.git", "evil"}},
		{Name: "evil-npm-path", Protocol: "stdio", Command: "npx", Args: []string{"-y", "./local-evil"}},
		// MCP-2442 re-review: PEP 508 / npm direct-reference '@'-tail bypass.
		{Name: "evil-uvx-directref", Protocol: "stdio", Command: "uvx", Args: []string{"--from", "evilpkg@" + pkgDir, "evil"}},
		{Name: "evil-npm-directref", Protocol: "stdio", Command: "npx", Args: []string{"-y", "evilpkg@./local-evil"}},
	}
	for _, info := range cases {
		res, err := r.resolveFromPackageFetch(context.Background(), info)
		if err == nil {
			if res != nil && res.Cleanup != nil {
				res.Cleanup()
			}
			t.Errorf("%s: expected error (fallback to tool-defs), got nil", info.Name)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("setup.py was EXECUTED — marker present at %s", marker)
	}
}

func pyQuote(s string) string {
	return "r'" + s + "'"
}
