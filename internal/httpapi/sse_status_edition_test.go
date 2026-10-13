package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestSSE_StatusFramesCarryEdition (Spec 109 FR-056 / T008, T020): the Web UI
// gates the Settings "Server Edition" tab on `systemStore.status.edition ===
// 'server'` rather than on whether the config happens to have a
// `server_edition` key (a stale/imported config can carry that key on a
// personal-edition instance). `GET /api/v1/status` already reports `edition`;
// the SSE `status` stream — what actually populates systemStore.status on a
// running Web UI — must carry the same field on its initial frame (the one
// used at mount time), so the two can never disagree.
func TestSSE_StatusFramesCarryEdition(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	body, closeConn := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer closeConn()

	deadline := time.Now().Add(5 * time.Second)
	initial := readSSEUntil(t, body, "status", deadline)
	edition, ok := initial.Data["edition"]
	if !ok {
		t.Fatalf("initial SSE status frame is missing \"edition\": %#v", initial.Data)
	}
	if edition != "personal" {
		t.Fatalf("expected edition %q on the initial frame, got %q", "personal", edition)
	}
}
