package scanner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestFirstPackageArg(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"-y", "google-flights-mcp-server@0.2.4"}, "google-flights-mcp-server@0.2.4"},
		{[]string{"--from", "pkg-a", "cmd"}, "pkg-a"},
		{[]string{"pkg-only"}, "pkg-only"},
		{[]string{"-y"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		got := firstPackageArg(c.args)
		if got != c.want {
			t.Errorf("firstPackageArg(%v) = %q, want %q", c.args, got, c.want)
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
