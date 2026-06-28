package config

import (
	"testing"
	"time"
)

// TestResolveInitTimeout covers the per-server → global → default precedence
// (MCP-3322 / GH #760). Unlike the discovery intervals, a resolved value <= 0
// (including a pointer-to-zero override) maps to the 30s default rather than
// "disabled" — we never want a hang-forever connect deadline.
func TestResolveInitTimeout(t *testing.T) {
	cases := []struct {
		name   string
		global *Duration
		server *Duration
		want   time.Duration
	}{
		{"both unset → default", nil, nil, 30 * time.Second},
		{"global set, server unset → global", durPtr(120 * time.Second), nil, 120 * time.Second},
		{"server override wins", durPtr(120 * time.Second), durPtr(90 * time.Second), 90 * time.Second},
		{"server override wins over default when global unset", nil, durPtr(45 * time.Second), 45 * time.Second},
		{"global zero → default (never disabled)", durPtr(0), nil, 30 * time.Second},
		{"server zero → default (never disabled)", durPtr(120 * time.Second), durPtr(0), 30 * time.Second},
		{"server negative → default", nil, durPtr(-5 * time.Second), 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{InitTimeout: tc.global}
			sc := &ServerConfig{InitTimeout: tc.server}
			got := c.ResolveInitTimeout(sc)
			if got != tc.want {
				t.Errorf("ResolveInitTimeout = %v, want %v", got, tc.want)
			}
		})
	}

	// A nil server config must still resolve via global → default.
	c := &Config{InitTimeout: durPtr(120 * time.Second)}
	if got := c.ResolveInitTimeout(nil); got != 120*time.Second {
		t.Errorf("nil server: got %v, want 120s", got)
	}
}

// TestValidateInitTimeoutBounds covers the bounds contract: 0s accepted (means
// "use default"), in-range [1s, 30m] accepted, out-of-range rejected — for both
// the global and per-server pointers.
func TestValidateInitTimeoutBounds(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Config)
		wantError bool
	}{
		{"0s accepted (default)", func(c *Config) { c.InitTimeout = durPtr(0) }, false},
		{"1s accepted (min)", func(c *Config) { c.InitTimeout = durPtr(time.Second) }, false},
		{"30m accepted (max)", func(c *Config) { c.InitTimeout = durPtr(30 * time.Minute) }, false},
		{"500ms rejected (below min)", func(c *Config) { c.InitTimeout = durPtr(500 * time.Millisecond) }, true},
		{"31m rejected (above max)", func(c *Config) { c.InitTimeout = durPtr(31 * time.Minute) }, true},
		{"negative rejected", func(c *Config) { c.InitTimeout = durPtr(-1 * time.Second) }, true},
		{"per-server 500ms rejected", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s", InitTimeout: durPtr(500 * time.Millisecond)}}
		}, true},
		{"per-server 120s accepted", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s", InitTimeout: durPtr(120 * time.Second)}}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(c)
			errs := c.ValidateDetailed()
			hasErr := false
			for _, e := range errs {
				if containsAny(e.Field, "init_timeout") {
					hasErr = true
				}
			}
			if hasErr != tc.wantError {
				t.Errorf("validation init_timeout error = %v, want %v (errors: %+v)", hasErr, tc.wantError, errs)
			}
		})
	}
}
