package main

import (
	"errors"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestRecordStartupOutcomeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil → success", nil, "success"},
		{"port conflict text", errors.New("listen tcp: bind: address already in use"), "port_conflict"},
		{"db locked text", errors.New("database is locked"), "db_locked"},
		{"config error text", errors.New("invalid configuration: bad field"), "config_error"},
		{"unrelated error", errors.New("kaboom"), "other_error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyStartupError(c.err)
			if got != c.want {
				t.Errorf("classifyStartupError(%q) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestRecordStartupOutcomePersists(t *testing.T) {
	cfg := &config.Config{}
	recordStartupOutcome(cfg, "", "success")

	if cfg.Telemetry == nil {
		t.Fatal("Telemetry should be initialized")
	}
	if cfg.Telemetry.LastStartupOutcome != "success" {
		t.Errorf("LastStartupOutcome = %q", cfg.Telemetry.LastStartupOutcome)
	}

	// Idempotent: second call with same outcome leaves the field unchanged.
	recordStartupOutcome(cfg, "", "success")
	if cfg.Telemetry.LastStartupOutcome != "success" {
		t.Errorf("LastStartupOutcome changed: %q", cfg.Telemetry.LastStartupOutcome)
	}
}
