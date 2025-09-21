package runtime

import (
	"testing"

	"go.uber.org/zap/zaptest"

	"mcpproxy-go/internal/config"
)

func TestUpdateListenAddressValidation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = ":8080"

	rt, err := New(cfg, "", zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}

	if err := rt.UpdateListenAddress("127.0.0.1:9090"); err != nil {
		t.Fatalf("expected UpdateListenAddress to accept valid address: %v", err)
	}

	if got := rt.Config().Listen; got != "127.0.0.1:9090" {
		t.Fatalf("expected runtime config to reflect update, got %s", got)
	}

	if err := rt.UpdateListenAddress("invalid"); err == nil {
		t.Fatalf("expected invalid listen address to return error")
	}
}
