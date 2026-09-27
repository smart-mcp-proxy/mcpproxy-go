package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"go.uber.org/zap"
)

func configFromJSON(t *testing.T, document string) *config.Config {
	t.Helper()
	var cfg config.Config
	if err := cfg.UnmarshalJSON([]byte(document)); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return &cfg
}

func assertServedEditionHint(t *testing.T, cfg *config.Config, want bool) {
	t.Helper()
	var indexServes int
	h := newWebUIHandler(cfg, zap.NewNop().Sugar(), func() { indexServes++ })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != 200 {
		t.Fatalf("expected index response, got %d", rec.Code)
	}
	wantHint := `name="mcpproxy-server-edition" content="false"`
	if want {
		wantHint = `name="mcpproxy-server-edition" content="true"`
	}
	body := rec.Body.String()
	if !strings.Contains(body, wantHint) {
		t.Fatalf("expected %s in served HTML, got %q", wantHint, body)
	}
	if strings.Contains(body, "operator-internal.example") {
		t.Fatalf("served HTML leaked configuration data: %q", body)
	}
	if indexServes != 1 {
		t.Fatalf("expected index callback once, got %d", indexServes)
	}
}
