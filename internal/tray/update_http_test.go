//go:build !nogui && !headless && !linux

package tray

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stalled server must not hang the updater: the API/download client has a
// finite timeout.
func TestUpdateHTTPClientsHaveTimeouts(t *testing.T) {
	if updateAPIClient.Timeout <= 0 {
		t.Fatal("updateAPIClient has no timeout")
	}
	if updateDownloadClient.Timeout <= 0 {
		t.Fatal("updateDownloadClient has no timeout")
	}
}

func TestDownloadToFile_TimesOutOnStalledServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	old := updateDownloadClient
	updateDownloadClient = &http.Client{Timeout: 100 * time.Millisecond}
	defer func() { updateDownloadClient = old }()

	start := time.Now()
	err := downloadToFile(srv.URL, filepath.Join(t.TempDir(), "x"), 1024)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took too long: %v", time.Since(start))
	}
}

// An oversized body must be rejected, not silently truncated.
func TestDownloadToFile_RejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 2048)))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "x")
	err := downloadToFile(srv.URL, dest, 1024)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("oversized download left a file behind")
	}
}

func TestDownloadToFile_AcceptsBodyAtLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 1024)))
	}))
	defer srv.Close()
	if err := downloadToFile(srv.URL, filepath.Join(t.TempDir(), "x"), 1024); err != nil {
		t.Fatalf("body at the limit should pass: %v", err)
	}
}

func TestFetchJSON_BoundsBodyAndStatus(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"` + strings.Repeat("a", 4096) + `"}`))
	}))
	defer big.Close()
	var rel GitHubRelease
	if err := fetchJSON(big.URL, 1024, &rel); err == nil {
		t.Fatal("expected oversized JSON to be rejected")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer bad.Close()
	if err := fetchJSON(bad.URL, 1024, &rel); err == nil {
		t.Fatal("expected non-200 to be an error")
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer ok.Close()
	if err := fetchJSON(ok.URL, 1024, &rel); err != nil || rel.TagName != "v1.2.3" {
		t.Fatalf("fetchJSON ok case: %v %+v", err, rel)
	}
}
