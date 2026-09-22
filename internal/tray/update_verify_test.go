//go:build !nogui && !headless && !linux

package tray

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inconshreveable/go-update"
	"go.uber.org/zap/zaptest"
)

// ghAsset mirrors the anonymous asset struct inside GitHubRelease so tests can
// build release fixtures without reaching for the network.
type ghAsset = struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

const fakeDigest = "1111111111111111111111111111111111111111111111111111111111111111"

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildTarGz returns a .tar.gz containing one member with the given name.
func buildTarGz(t *testing.T, member string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
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

// buildZip returns a .zip containing one member with the given name.
func buildZip(t *testing.T, member string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(member)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestApp_SelfUpdate_VerifiesTheAssetItActuallySelected walks the real
// selection path: findAsset prefers the "mcpproxy-latest-*" alias over the
// versioned archive, and in a real release those two assets have DIFFERENT
// digests (macOS notarization rewrites the versioned one). So a manifest that
// lists only the versioned name must refuse the alias download rather than
// verify it against the neighbouring entry.
func TestApp_SelfUpdate_VerifiesTheAssetItActuallySelected(t *testing.T) {
	ext := assetTarGzExt
	if runtime.GOOS == osWindows {
		ext = assetZipExt
	}
	aliasName := fmt.Sprintf("mcpproxy-latest-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
	versionedName := fmt.Sprintf("mcpproxy-9.9.9-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	archive := buildTarGz(t, "mcpproxy", []byte("pretend core binary"))
	if ext == assetZipExt {
		archive = buildZip(t, "mcpproxy", []byte("pretend core binary"))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		// Correct digest, but published under the versioned name only.
		_, _ = io.WriteString(w, sha256Hex(archive)+"  "+versionedName+"\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	release := &GitHubRelease{
		TagName: "v9.9.9",
		Assets: []ghAsset{
			{Name: versionedName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: aliasName, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}

	var applied atomic.Int32
	app := New(NewMockServer(), zaptest.NewLogger(t).Sugar(), "1.0.0", func() {})
	app.applyUpdateFn = func(r io.Reader, _ update.Options) error {
		_, _ = io.Copy(io.Discard, r)
		applied.Add(1)
		return nil
	}

	assetName, url, err := app.findAsset(release)
	if err != nil {
		t.Fatalf("findAsset: %v", err)
	}
	if assetName != aliasName {
		t.Fatalf("findAsset chose %q, want the %q alias (this test only means something if the alias wins)", assetName, aliasName)
	}

	err = app.downloadAndApplyUpdate(release, assetName, url)
	if err == nil {
		t.Fatalf("expected refusal: %q is not listed in the manifest", aliasName)
	}
	if !strings.Contains(err.Error(), "not listed") {
		t.Errorf("error = %q, want it to say the asset is not listed", err.Error())
	}
	if got := applied.Load(); got != 0 {
		t.Errorf("applyUpdate called %d times, want 0", got)
	}
}

// TestApp_DownloadAndApplyUpdate_ChecksumGate asserts the tray self-update path
// installs an artifact ONLY when checksums.txt from the same release lists that
// exact asset name with a digest matching the downloaded bytes. Everything else
// must fail closed.
func TestApp_DownloadAndApplyUpdate_ChecksumGate(t *testing.T) {
	tarGz := buildTarGz(t, "mcpproxy", []byte("pretend core binary"))
	zipped := buildZip(t, "mcpproxy", []byte("pretend core binary"))

	tests := []struct {
		name string
		// assetName is the release asset being installed.
		assetName string
		// archive is the bytes the server hands back for that asset.
		archive []byte
		// manifest is the checksums.txt body; empty means "serve a 500".
		manifest string
		// publishChecksums controls whether the release lists checksums.txt.
		publishChecksums bool
		wantApplied      bool
		wantErrContains  string
	}{
		{
			name:             "matching digest installs",
			assetName:        "mcpproxy-latest-darwin-arm64.tar.gz",
			archive:          tarGz,
			manifest:         sha256Hex(tarGz) + "  mcpproxy-latest-darwin-arm64.tar.gz\n",
			publishChecksums: true,
			wantApplied:      true,
		},
		{
			name:             "digest mismatch refuses",
			assetName:        "mcpproxy-latest-darwin-arm64.tar.gz",
			archive:          tarGz,
			manifest:         fakeDigest + "  mcpproxy-latest-darwin-arm64.tar.gz\n",
			publishChecksums: true,
			wantApplied:      false,
			wantErrContains:  "checksum mismatch",
		},
		{
			name:      "asset not listed in manifest refuses",
			assetName: "mcpproxy-latest-darwin-arm64.tar.gz",
			archive:   tarGz,
			// The manifest is well-formed but covers only other assets: the
			// "latest-*" alias and the versioned archive have different digests,
			// so a near-miss name must never satisfy the gate.
			manifest:         sha256Hex(tarGz) + "  mcpproxy-0.68.0-darwin-arm64.tar.gz\n",
			publishChecksums: true,
			wantApplied:      false,
			wantErrContains:  "not listed",
		},
		{
			name:             "release without checksums.txt refuses",
			assetName:        "mcpproxy-latest-darwin-arm64.tar.gz",
			archive:          tarGz,
			manifest:         sha256Hex(tarGz) + "  mcpproxy-latest-darwin-arm64.tar.gz\n",
			publishChecksums: false,
			wantApplied:      false,
			wantErrContains:  "checksums.txt",
		},
		{
			name:             "unreachable manifest refuses",
			assetName:        "mcpproxy-latest-darwin-arm64.tar.gz",
			archive:          tarGz,
			manifest:         "", // server answers 500
			publishChecksums: true,
			wantApplied:      false,
			wantErrContains:  "checksums.txt",
		},
		{
			name:             "zip path matching digest installs",
			assetName:        "mcpproxy-latest-windows-amd64.zip",
			archive:          zipped,
			manifest:         sha256Hex(zipped) + "  mcpproxy-latest-windows-amd64.zip\n",
			publishChecksums: true,
			wantApplied:      true,
		},
		{
			name:             "zip path digest mismatch refuses",
			assetName:        "mcpproxy-latest-windows-amd64.zip",
			archive:          zipped,
			manifest:         fakeDigest + "  mcpproxy-latest-windows-amd64.zip\n",
			publishChecksums: true,
			wantApplied:      false,
			wantErrContains:  "checksum mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(tt.archive)
			})
			mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
				if tt.manifest == "" {
					http.Error(w, "boom", http.StatusInternalServerError)
					return
				}
				_, _ = io.WriteString(w, tt.manifest)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			release := &GitHubRelease{
				TagName: "v9.9.9",
				Assets: []ghAsset{
					{Name: tt.assetName, BrowserDownloadURL: srv.URL + "/asset"},
				},
			}
			if tt.publishChecksums {
				release.Assets = append(release.Assets, ghAsset{
					Name:               "checksums.txt",
					BrowserDownloadURL: srv.URL + "/checksums.txt",
				})
			}

			var applied atomic.Int32
			app := New(NewMockServer(), zaptest.NewLogger(t).Sugar(), "1.0.0", func() {})
			app.applyUpdateFn = func(r io.Reader, _ update.Options) error {
				_, _ = io.Copy(io.Discard, r)
				applied.Add(1)
				return nil
			}

			err := app.downloadAndApplyUpdate(release, tt.assetName, srv.URL+"/asset")

			if tt.wantErrContains == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected the update to be refused, got nil error (applied=%d)", applied.Load())
				}
				if !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantErrContains)
				}
			}

			wantCount := int32(0)
			if tt.wantApplied {
				wantCount = 1
			}
			if got := applied.Load(); got != wantCount {
				t.Errorf("applyUpdate called %d times, want %d (an unverified artifact must never be installed)", got, wantCount)
			}
		})
	}
}
