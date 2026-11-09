package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A backend that answers 2xx with {"success":false} rejected the feedback;
// the command must exit non-zero so scripts don't treat it as submitted.
func TestRunFeedback_RejectedSubmissionReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error":"rejected by backend"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	cfgJSON := fmt.Sprintf(`{"data_dir": %q, "telemetry": {"endpoint": %q}}`, dir, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	origConfig, origCategory := configFile, feedbackCategory
	t.Cleanup(func() { configFile, feedbackCategory = origConfig, origCategory })
	configFile, feedbackCategory = cfgPath, "bug"

	if err := runFeedback(nil, []string{"the search results could be more relevant"}); err == nil {
		t.Fatal("runFeedback returned nil for a submission the backend rejected")
	}
}
