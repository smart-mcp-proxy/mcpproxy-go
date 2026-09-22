//go:build !nogui && !headless && !linux

package tray

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inconshreveable/go-update"
	"go.uber.org/zap/zaptest"
)

// archiveMember is one file inside a test archive.
type archiveMember struct {
	name    string
	payload []byte
}

// buildTarGzMembers returns a .tar.gz containing the given members in order.
func buildTarGzMembers(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     m.name,
			Mode:     0o755,
			Size:     int64(len(m.payload)),
		}); err != nil {
			t.Fatalf("tar header %s: %v", m.name, err)
		}
		if _, err := tw.Write(m.payload); err != nil {
			t.Fatalf("tar write %s: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// buildZipMembers returns a .zip containing the given members in order.
func buildZipMembers(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", m.name, err)
		}
		if _, err := w.Write(m.payload); err != nil {
			t.Fatalf("zip write %s: %v", m.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// serveVerifiedAsset stands up a release whose checksums.txt already vouches
// for the archive, so these tests exercise extraction rather than the
// (separately tested) checksum gate.
func serveVerifiedAsset(t *testing.T, assetName string, archive []byte) (*GitHubRelease, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, sha256Hex(archive)+"  "+assetName+"\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &GitHubRelease{
		TagName: "v9.9.9",
		Assets: []ghAsset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}, srv.URL + "/asset"
}

// TestApp_SelfUpdate_InstallsTheTrayBinaryNotTheCore is the regression test for
// the bug this change fixes: a release archive holds BOTH mcpproxy and
// mcpproxy-tray, the self-update runs inside the tray process (only
// cmd/mcpproxy-tray imports this package), and update.Options.TargetPath is
// os.Executable() — the tray. Selecting anything but the tray member therefore
// installs the CORE binary over the TRAY binary and bricks the tray.
//
// The archive deliberately lists mcpproxy first, which is the order
// .github/workflows/release.yml produces, so a "first entry wins" or a
// HasSuffix("mcpproxy") rule picks the wrong file.
func TestApp_SelfUpdate_InstallsTheTrayBinaryNotTheCore(t *testing.T) {
	// Every member carries a payload that NAMES it, so the assertion pins the
	// exact member extracted rather than merely "something tray-shaped": if
	// the selection ever stopped honouring runtime.GOOS, an archive whose two
	// tray members shared one payload would still pass.
	payloadFor := func(member string) string { return "BINARY:" + member }

	tests := []struct {
		name      string
		assetName string
		archive   func(t *testing.T, members ...archiveMember) []byte
	}{
		{"tar.gz", "mcpproxy-latest-darwin-arm64" + assetTarGzExt, buildTarGzMembers},
		{"zip", "mcpproxy-latest-windows-amd64" + assetZipExt, buildZipMembers},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coreName := "mcpproxy"
			if strings.HasSuffix(tt.assetName, assetZipExt) {
				coreName = "mcpproxy.exe"
			}
			// The core is listed FIRST, as .github/workflows/release.yml
			// produces it, so a "first entry wins" or HasSuffix("mcpproxy")
			// rule picks it. Both tray spellings are present with distinct
			// payloads; trayBinaryName() decides which one this build takes.
			archive := tt.archive(t,
				archiveMember{coreName, []byte(payloadFor(coreName))},
				archiveMember{"mcpproxy-tray", []byte(payloadFor("mcpproxy-tray"))},
				archiveMember{"mcpproxy-tray.exe", []byte(payloadFor("mcpproxy-tray.exe"))},
			)
			release, url := serveVerifiedAsset(t, tt.assetName, archive)

			var applied atomic.Int32
			var gotPayload string
			var gotTarget string
			app := New(NewMockServer(), zaptest.NewLogger(t).Sugar(), "1.0.0", func() {})
			app.applyUpdateFn = func(r io.Reader, opts update.Options) error {
				b, err := io.ReadAll(r)
				if err != nil {
					return err
				}
				gotPayload = string(b)
				gotTarget = opts.TargetPath
				applied.Add(1)
				return nil
			}

			if err := app.downloadAndApplyUpdate(release, tt.assetName, url); err != nil {
				t.Fatalf("downloadAndApplyUpdate: %v", err)
			}
			if got := applied.Load(); got != 1 {
				t.Fatalf("applyUpdate called %d times, want 1", got)
			}
			// The expectation is an independent literal, NOT
			// payloadFor(trayBinaryName()): deriving it from the function
			// under test would make this assertion move with any bug in it.
			wantMember := "mcpproxy-tray"
			if runtime.GOOS == osWindows {
				wantMember = "mcpproxy-tray.exe"
			}
			if want := payloadFor(wantMember); gotPayload != want {
				t.Errorf("installed %q, want %q — this process replaces its own executable, so anything but %s bricks the tray",
					gotPayload, want, wantMember)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatalf("os.Executable: %v", err)
			}
			if gotTarget != exe {
				t.Errorf("TargetPath = %q, want the running executable %q", gotTarget, exe)
			}
		})
	}
}

// TestApp_SelfUpdate_RefusesArchiveWithoutTrayBinary: an archive that does not
// carry the tray binary must fail closed. Before this change the tar.gz path
// happily installed "mcpproxy" and the zip path installed whatever came first,
// so a layout change silently swapped the wrong file into place.
func TestApp_SelfUpdate_RefusesArchiveWithoutTrayBinary(t *testing.T) {
	tests := []struct {
		name      string
		assetName string
		archive   []byte
	}{
		{
			name:      "tar.gz with only the core binary",
			assetName: "mcpproxy-latest-darwin-arm64" + assetTarGzExt,
			archive: buildTarGzMembers(t,
				archiveMember{"mcpproxy", []byte("core")},
				archiveMember{"README.md", []byte("docs")},
			),
		},
		{
			name:      "zip with only the core binary",
			assetName: "mcpproxy-latest-windows-amd64" + assetZipExt,
			archive: buildZipMembers(t,
				archiveMember{"mcpproxy.exe", []byte("core")},
				archiveMember{"README.md", []byte("docs")},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release, url := serveVerifiedAsset(t, tt.assetName, tt.archive)

			var applied atomic.Int32
			app := New(NewMockServer(), zaptest.NewLogger(t).Sugar(), "1.0.0", func() {})
			app.applyUpdateFn = func(r io.Reader, _ update.Options) error {
				_, _ = io.Copy(io.Discard, r)
				applied.Add(1)
				return nil
			}

			err := app.downloadAndApplyUpdate(release, tt.assetName, url)
			if err == nil {
				t.Fatalf("expected a refusal: the archive has no %s member", trayBinaryName())
			}
			if !strings.Contains(err.Error(), trayBinaryName()) {
				t.Errorf("error = %q, want it to name the missing member %q", err.Error(), trayBinaryName())
			}
			if got := applied.Load(); got != 0 {
				t.Errorf("applyUpdate called %d times, want 0 — nothing must be installed when the tray binary is absent", got)
			}
		})
	}
}

// TestApp_SelfUpdate_MatchesNestedMemberOnBaseName: archives currently store
// members at the root, but matching on base name means a future layout that
// nests them under a directory keeps working — and, crucially, that a member
// named "mcpproxy-mcpproxy-tray" does NOT satisfy the match the way the old
// HasSuffix rule did. Both formats are covered: they are separate code paths.
func TestApp_SelfUpdate_MatchesNestedMemberOnBaseName(t *testing.T) {
	const trayPayload = "NESTED TRAY BINARY"

	tests := []struct {
		name      string
		assetName string
		build     func(t *testing.T, members ...archiveMember) []byte
	}{
		{"tar.gz", "mcpproxy-latest-darwin-arm64" + assetTarGzExt, buildTarGzMembers},
		{"zip", "mcpproxy-latest-windows-amd64" + assetZipExt, buildZipMembers},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			member := trayBinaryName()
			archive := tt.build(t,
				archiveMember{"mcpproxy-" + member, []byte("DECOY: suffix-matches but is not the tray binary")},
				archiveMember{"mcpproxy-0.68.0-darwin-arm64/" + member, []byte(trayPayload)},
			)
			release, url := serveVerifiedAsset(t, tt.assetName, archive)

			var gotPayload string
			app := New(NewMockServer(), zaptest.NewLogger(t).Sugar(), "1.0.0", func() {})
			app.applyUpdateFn = func(r io.Reader, _ update.Options) error {
				b, err := io.ReadAll(r)
				if err != nil {
					return err
				}
				gotPayload = string(b)
				return nil
			}

			if err := app.downloadAndApplyUpdate(release, tt.assetName, url); err != nil {
				t.Fatalf("downloadAndApplyUpdate: %v", err)
			}
			if gotPayload != trayPayload {
				t.Errorf("installed %q, want %q (base-name match, not suffix match)", gotPayload, trayPayload)
			}
		})
	}
}

// TestTrayBinaryName pins the member name to the binary the release workflow
// actually ships (release.yml copies mcpproxy-tray / mcpproxy-tray.exe into the
// archive stage), and to the name of the executable this package runs as.
func TestTrayBinaryName(t *testing.T) {
	want := "mcpproxy-tray"
	if runtime.GOOS == osWindows {
		want = "mcpproxy-tray.exe"
	}
	if got := trayBinaryName(); got != want {
		t.Errorf("trayBinaryName() = %q, want %q", got, want)
	}
	if base := filepath.Base(want); base != want {
		t.Errorf("trayBinaryName() must be a bare base name, got %q", want)
	}
}
