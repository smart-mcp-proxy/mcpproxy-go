package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// These tests exercise the real download → verify → swap path (FR-021)
// against an httptest release server and real files. The "binary" shipped in
// the fixture archive is a tiny shell script so `--version` genuinely runs,
// which is what FR-021 defines success as.

func requirePOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture binaries are POSIX shell scripts")
	}
}

// makeTarGz builds a .tar.gz containing one regular file.
func makeTarGz(t *testing.T, memberName, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     memberName,
		Mode:     0o755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// releaseFixture serves an archive, its checksums manifest and a cosign
// bundle, and reports which paths were requested.
type releaseFixture struct {
	server      *httptest.Server
	release     *updatecheck.GitHubRelease
	archive     []byte
	assetName   string
	requested   map[string]int
	corruptFile bool
}

func newReleaseFixture(t *testing.T, tag, binaryScript string, opts ...func(*releaseFixture)) *releaseFixture {
	t.Helper()

	version := strings.TrimPrefix(tag, "v")
	assetName := archiveAssetName(version, runtime.GOOS, runtime.GOARCH)
	archive := makeTarGz(t, coreBinaryName(), binaryScript)

	f := &releaseFixture{
		archive:   archive,
		assetName: assetName,
		requested: map[string]int{},
	}
	for _, opt := range opts {
		opt(f)
	}

	// The manifest always describes the pristine archive; corruption is
	// injected on the wire so the checksum check is the thing under test.
	checksums := fmt.Sprintf("%s  %s\n%s  some-other-artifact.dmg\n",
		sha256Hex(archive), assetName, sha256Hex([]byte("unrelated")))

	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		f.requested[assetName]++
		body := archive
		if f.corruptFile {
			body = append(append([]byte{}, archive...), 'x')
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/"+checksumsAssetName, func(w http.ResponseWriter, _ *http.Request) {
		f.requested[checksumsAssetName]++
		_, _ = w.Write([]byte(checksums))
	})
	mux.HandleFunc("/"+cosignBundleName, func(w http.ResponseWriter, _ *http.Request) {
		f.requested[cosignBundleName]++
		_, _ = w.Write([]byte(`{"fixture":"bundle"}`))
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	f.release = &updatecheck.GitHubRelease{
		TagName: tag,
		HTMLURL: "https://example.invalid/releases/" + tag,
		Assets: []updatecheck.Asset{
			{Name: assetName, BrowserDownloadURL: f.server.URL + "/" + assetName},
			{Name: checksumsAssetName, BrowserDownloadURL: f.server.URL + "/" + checksumsAssetName},
			{Name: cosignBundleName, BrowserDownloadURL: f.server.URL + "/" + cosignBundleName},
		},
	}
	return f
}

// withoutAsset drops a trust artifact from the published release.
func withoutAsset(name string) func(*updatecheck.GitHubRelease) {
	return func(rel *updatecheck.GitHubRelease) {
		kept := rel.Assets[:0]
		for _, a := range rel.Assets {
			if a.Name != name {
				kept = append(kept, a)
			}
		}
		rel.Assets = kept
	}
}

const newBinaryScript = "#!/bin/sh\necho \"MCPProxy v9.9.9 (personal) test\"\n"

// installTarget writes a stand-in "currently installed" binary.
func installTarget(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	old := "#!/bin/sh\necho \"MCPProxy v0.50.0 (personal) test\"\n"
	if err := os.WriteFile(target, []byte(old), mode); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Chmod(target, mode); err != nil {
		t.Fatalf("chmod target: %v", err)
	}
	return target
}

func selfUpdateRunner(t *testing.T, target string, fixture *releaseFixture, flags updateFlags) (*updateRunner, *bytes.Buffer) {
	t.Helper()
	t.Setenv("CI", "")
	errOut := &bytes.Buffer{}
	return &updateRunner{
		out:             &bytes.Buffer{},
		errOut:          errOut,
		format:          "json",
		currentVersion:  "v0.50.0",
		channel:         updatecheck.ChannelTarball,
		execPath:        target,
		flags:           flags,
		releases:        &fakeReleaseSource{latest: fixture.release},
		httpClient:      fixture.server.Client(),
		cosignAvailable: func() bool { return true },
		cosignVerify:    func(_, _ string) error { return nil },
		verifyInstalled: verifyInstalledVersion,
	}, errOut
}

// The happy path: download, verify, swap, prove the new binary runs, preserve
// mode, and leave no .old or staging file behind (FR-021).
func TestSelfUpdate_HappyPath(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o750)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)
	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})

	if err := runner.selfUpdate(fixture.release); err != nil {
		t.Fatalf("selfUpdate() error = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != newBinaryScript {
		t.Errorf("target was not replaced; content = %q", string(got))
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if fi.Mode().Perm() != 0o750 {
		t.Errorf("mode = %o, want 0750 (FR-021 preserves permissions)", fi.Mode().Perm())
	}

	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old backup must be removed once the new binary verified")
	}
	assertNoStagingLeftovers(t, filepath.Dir(target))

	// All three artifacts must have been fetched: the archive is worthless
	// without the manifest, and the manifest is worthless without its
	// signature bundle.
	for _, name := range []string{fixture.assetName, checksumsAssetName, cosignBundleName} {
		if fixture.requested[name] == 0 {
			t.Errorf("%s was never downloaded", name)
		}
	}
}

// SC-004: a tampered artifact is never installed.
func TestSelfUpdate_ChecksumMismatchInstallsNothing(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	before, _ := os.ReadFile(target)

	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript, func(f *releaseFixture) { f.corruptFile = true })
	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})

	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want a checksum mismatch", err)
	}

	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Errorf("the installed binary must be untouched after a checksum failure")
	}
	assertNoStagingLeftovers(t, filepath.Dir(target))
}

// FR-021: if the new binary does not run and report the expected version, the
// previous one is restored.
func TestSelfUpdate_RestoresPreviousBinaryOnVerifyFailure(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	before, _ := os.ReadFile(target)

	// The "new" binary reports a different version than the release claims.
	fixture := newReleaseFixture(t, "v9.9.9", "#!/bin/sh\necho \"MCPProxy v1.0.0\"\n")
	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})

	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), "previous version restored") {
		t.Fatalf("error = %v, want a restore-on-failure error", err)
	}

	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the previous binary must still exist: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("previous binary was not restored; content = %q", string(after))
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("the .old backup must be gone after a successful restore")
	}
}

// FR-021: signature verification is required. A release without a cosign
// bundle aborts rather than silently degrading to checksum-only trust.
func TestSelfUpdate_MissingSignatureBundleAborts(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)
	withoutAsset(cosignBundleName)(fixture.release)

	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})
	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), cosignBundleName) {
		t.Fatalf("error = %v, want an abort naming the missing bundle", err)
	}
	if fixture.requested[fixture.assetName] != 0 {
		t.Errorf("the archive must not be downloaded once verification is known to be impossible")
	}
}

// …and the opt-out is explicit and loud.
func TestSelfUpdate_AllowUnverifiedSignatureWarnsAndProceeds(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)
	withoutAsset(cosignBundleName)(fixture.release)

	runner, errOut := selfUpdateRunner(t, target, fixture, updateFlags{allowUnverified: true})
	runner.cosignVerify = func(_, _ string) error {
		t.Fatal("cosign must not run when the signature check is waived")
		return nil
	}

	if err := runner.selfUpdate(fixture.release); err != nil {
		t.Fatalf("selfUpdate() error = %v", err)
	}
	if !strings.Contains(errOut.String(), "WARNING") {
		t.Errorf("waiving signature verification must warn loudly; stderr = %q", errOut.String())
	}
	got, _ := os.ReadFile(target)
	if string(got) != newBinaryScript {
		t.Errorf("target was not replaced")
	}
}

// A release with no checksums.txt cannot be verified at all.
func TestSelfUpdate_MissingChecksumsAborts(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)
	withoutAsset(checksumsAssetName)(fixture.release)

	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})
	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), checksumsAssetName) {
		t.Fatalf("error = %v, want an abort naming checksums.txt", err)
	}
}

// A failing signature verification aborts with nothing installed.
func TestSelfUpdate_SignatureVerificationFailureAborts(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	before, _ := os.ReadFile(target)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)

	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})
	runner.cosignVerify = func(_, _ string) error { return fmt.Errorf("certificate identity mismatch") }

	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("error = %v, want a signature verification failure", err)
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Errorf("nothing may be installed when the signature does not verify")
	}
}

// Cosign missing from PATH is a hard stop by default, with an actionable
// message — not a silent downgrade of the trust model.
func TestSelfUpdate_CosignMissingIsActionable(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)

	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})
	runner.cosignAvailable = func() bool { return false }

	err := runner.selfUpdate(fixture.release)
	if err == nil {
		t.Fatal("expected an error when cosign is unavailable")
	}
	for _, want := range []string{"cosign", "--allow-unverified-signature"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// FR-022: a non-writable target fails with a message naming the path and its
// owner, and never suggests sudo.
func TestSelfUpdate_NonWritableTarget(t *testing.T) {
	requirePOSIXShell(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)
	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})

	err := runner.selfUpdate(fixture.release)
	if err == nil {
		t.Fatal("expected a refusal for a non-writable target")
	}
	msg := err.Error()
	if !strings.Contains(msg, target) {
		t.Errorf("error must name the target path; got %q", msg)
	}
	if !strings.Contains(msg, "owner:") {
		t.Errorf("error must name the owner; got %q", msg)
	}
	if strings.Contains(strings.ToLower(msg), "sudo") {
		t.Errorf("error must never suggest privilege escalation; got %q", msg)
	}
	if fixture.requested[fixture.assetName] != 0 {
		t.Errorf("nothing should be downloaded when the target cannot be written")
	}
}

// The release may simply not publish an artifact for this platform.
func TestSelfUpdate_MissingPlatformAsset(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)
	withoutAsset(fixture.assetName)(fixture.release)

	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})
	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), fixture.assetName) {
		t.Fatalf("error = %v, want a message naming the missing asset", err)
	}
}

// An artifact present on the release but absent from the manifest must not be
// installed: an unlisted file is outside the signed trust boundary.
func TestSelfUpdate_AssetNotListedInManifest(t *testing.T) {
	requirePOSIXShell(t)

	target := installTarget(t, 0o755)
	fixture := newReleaseFixture(t, "v9.9.9", newBinaryScript)

	// Re-serve a manifest that omits our asset.
	mux := http.NewServeMux()
	mux.HandleFunc("/"+checksumsAssetName, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  unrelated.tar.gz\n", sha256Hex([]byte("nope")))
	})
	mux.HandleFunc("/"+fixture.assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture.archive)
	})
	mux.HandleFunc("/"+cosignBundleName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for i := range fixture.release.Assets {
		fixture.release.Assets[i].BrowserDownloadURL = srv.URL + "/" + fixture.release.Assets[i].Name
	}

	runner, _ := selfUpdateRunner(t, target, fixture, updateFlags{})
	runner.httpClient = srv.Client()

	err := runner.selfUpdate(fixture.release)
	if err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("error = %v, want a refusal for an unlisted artifact", err)
	}
}

// assertNoStagingLeftovers proves the swap cleaned up after itself.
func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".mcpproxy") || strings.HasSuffix(name, ".old") || strings.Contains(name, ".new-") {
			t.Errorf("leftover staging file: %s", name)
		}
	}
}
