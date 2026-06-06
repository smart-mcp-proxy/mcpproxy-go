package config

import (
	"testing"
	"time"
)

func durPtr(d time.Duration) *Duration {
	v := Duration(d)
	return &v
}

// TestResolveHealthCheckInterval covers the per-server → global → default
// precedence and the pointer-to-zero "disabled" semantics (spec 074, FR-006/FR-007).
func TestResolveHealthCheckInterval(t *testing.T) {
	cases := []struct {
		name   string
		global *Duration
		server *Duration
		want   time.Duration
	}{
		{"both unset → default", nil, nil, 30 * time.Second},
		{"global set, server unset → global", durPtr(45 * time.Second), nil, 45 * time.Second},
		{"server override wins", durPtr(45 * time.Second), durPtr(10 * time.Second), 10 * time.Second},
		{"server override wins over default when global unset", nil, durPtr(15 * time.Second), 15 * time.Second},
		{"global disabled (0s)", durPtr(0), nil, 0},
		{"server disabled (0s) wins over global interval", durPtr(45 * time.Second), durPtr(0), 0},
		{"server interval wins over global disabled", durPtr(0), durPtr(20 * time.Second), 20 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{HealthCheckInterval: tc.global}
			sc := &ServerConfig{HealthCheckInterval: tc.server}
			got := c.ResolveHealthCheckInterval(sc)
			if got != tc.want {
				t.Errorf("ResolveHealthCheckInterval = %v, want %v", got, tc.want)
			}
		})
	}

	// A nil server config must still resolve via global → default.
	c := &Config{HealthCheckInterval: durPtr(45 * time.Second)}
	if got := c.ResolveHealthCheckInterval(nil); got != 45*time.Second {
		t.Errorf("nil server: got %v, want 45s", got)
	}
}

// TestResolveToolDiscoveryInterval mirrors the health-check resolver with the
// 5m default.
func TestResolveToolDiscoveryInterval(t *testing.T) {
	cases := []struct {
		name   string
		global *Duration
		server *Duration
		want   time.Duration
	}{
		{"both unset → default", nil, nil, 5 * time.Minute},
		{"global set", durPtr(10 * time.Minute), nil, 10 * time.Minute},
		{"server override wins", durPtr(10 * time.Minute), durPtr(2 * time.Minute), 2 * time.Minute},
		{"global disabled", durPtr(0), nil, 0},
		{"server disabled wins", durPtr(10 * time.Minute), durPtr(0), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{ToolDiscoveryInterval: tc.global}
			sc := &ServerConfig{ToolDiscoveryInterval: tc.server}
			got := c.ResolveToolDiscoveryInterval(sc)
			if got != tc.want {
				t.Errorf("ResolveToolDiscoveryInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateDiscoveryIntervalBounds covers FR-008: 0s accepted (disabled),
// in-range accepted, out-of-range rejected — for both the global and per-server
// pointers, across both keys.
func TestValidateDiscoveryIntervalBounds(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Config)
		wantError bool
	}{
		// Health-check: 0s or [5s, 1h]
		{"health 0s accepted (disabled)", func(c *Config) { c.HealthCheckInterval = durPtr(0) }, false},
		{"health 5s accepted (min)", func(c *Config) { c.HealthCheckInterval = durPtr(5 * time.Second) }, false},
		{"health 1h accepted (max)", func(c *Config) { c.HealthCheckInterval = durPtr(time.Hour) }, false},
		{"health 2s rejected (below min)", func(c *Config) { c.HealthCheckInterval = durPtr(2 * time.Second) }, true},
		{"health 2h rejected (above max)", func(c *Config) { c.HealthCheckInterval = durPtr(2 * time.Hour) }, true},
		{"health negative rejected", func(c *Config) { c.HealthCheckInterval = durPtr(-1 * time.Second) }, true},
		// Tool-discovery: 0s or [30s, 24h]
		{"discovery 0s accepted (disabled)", func(c *Config) { c.ToolDiscoveryInterval = durPtr(0) }, false},
		{"discovery 30s accepted (min)", func(c *Config) { c.ToolDiscoveryInterval = durPtr(30 * time.Second) }, false},
		{"discovery 24h accepted (max)", func(c *Config) { c.ToolDiscoveryInterval = durPtr(24 * time.Hour) }, false},
		{"discovery 10s rejected (below min)", func(c *Config) { c.ToolDiscoveryInterval = durPtr(10 * time.Second) }, true},
		{"discovery 48h rejected (above max)", func(c *Config) { c.ToolDiscoveryInterval = durPtr(48 * time.Hour) }, true},
		// Per-server pointers validated too.
		{"per-server health 2s rejected", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s", HealthCheckInterval: durPtr(2 * time.Second)}}
		}, true},
		{"per-server discovery 30s accepted", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s", ToolDiscoveryInterval: durPtr(30 * time.Second)}}
		}, false},
		{"per-server discovery 5s rejected", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s", ToolDiscoveryInterval: durPtr(5 * time.Second)}}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(c)
			errs := c.ValidateDetailed()
			hasIntervalErr := false
			for _, e := range errs {
				if containsAny(e.Field, "health_check_interval", "tool_discovery_interval") {
					hasIntervalErr = true
				}
			}
			if hasIntervalErr != tc.wantError {
				t.Errorf("validation interval error = %v, want %v (errors: %+v)", hasIntervalErr, tc.wantError, errs)
			}
		})
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
