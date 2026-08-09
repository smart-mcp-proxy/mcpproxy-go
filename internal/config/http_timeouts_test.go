package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestResolveHTTPTimeouts covers the tri-state contract of the three global
// HTTP server timeouts (GH #965): nil = built-in default, a pointer to 0 =
// DISABLED (net/http zero value = no deadline), positive = that value.
//
// Note the deliberate asymmetry with ResolveInitTimeout: there a zero maps back
// to the default, because a connect handshake must always have a ceiling. Here
// zero is a first-class, documented, SUPPORTED value for all three keys.
func TestResolveHTTPTimeouts(t *testing.T) {
	cases := []struct {
		name  string
		value *Duration
		read  time.Duration
		write time.Duration
		idle  time.Duration
	}{
		{"unset → built-in defaults", nil, 120 * time.Second, 120 * time.Second, 180 * time.Second},
		{"explicit 0 → disabled (no timeout)", durPtr(0), 0, 0, 0},
		{"positive → that value", durPtr(15 * time.Minute), 15 * time.Minute, 15 * time.Minute, 15 * time.Minute},
		{"negative → built-in default", durPtr(-5 * time.Second), 120 * time.Second, 120 * time.Second, 180 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&Config{HTTPReadTimeout: tc.value}).ResolveHTTPReadTimeout(); got != tc.read {
				t.Errorf("ResolveHTTPReadTimeout = %v, want %v", got, tc.read)
			}
			if got := (&Config{HTTPWriteTimeout: tc.value}).ResolveHTTPWriteTimeout(); got != tc.write {
				t.Errorf("ResolveHTTPWriteTimeout = %v, want %v", got, tc.write)
			}
			if got := (&Config{HTTPIdleTimeout: tc.value}).ResolveHTTPIdleTimeout(); got != tc.idle {
				t.Errorf("ResolveHTTPIdleTimeout = %v, want %v", got, tc.idle)
			}
		})
	}
}

// TestResolveHTTPWriteTimeoutDefaults pins the write deadline's contract after
// the GH #965 fix: the default STAYS at 120s so REST/Web-UI/health endpoints
// keep their slow-reader protection, and the streaming routes (MCP + SSE
// /events) escape it per-request via http.ResponseController rather than by
// disabling it for the whole process. An explicit "0s" still disables it
// globally for operators who want that.
func TestResolveHTTPWriteTimeoutDefaults(t *testing.T) {
	if got := DefaultConfig().ResolveHTTPWriteTimeout(); got != 120*time.Second {
		t.Fatalf("default write timeout = %v, want 120s", got)
	}
	explicitZero := DefaultConfig()
	explicitZero.HTTPWriteTimeout = durPtr(0)
	if got := explicitZero.ResolveHTTPWriteTimeout(); got != 0 {
		t.Fatalf("explicit 0s write timeout = %v, want 0 (disabled)", got)
	}
	// An unset key must stay unset in the struct — defaults live only in the
	// resolvers (same precedent as health_check_interval).
	if c := DefaultConfig(); c.HTTPReadTimeout != nil || c.HTTPWriteTimeout != nil || c.HTTPIdleTimeout != nil {
		t.Fatalf("DefaultConfig must leave the HTTP timeout pointers nil, got %v %v %v",
			c.HTTPReadTimeout, c.HTTPWriteTimeout, c.HTTPIdleTimeout)
	}
}

// TestValidateHTTPTimeoutBounds covers the bounds contract: {0} ∪ [1s, 24h].
// 0 is accepted because it is the documented "no timeout" value.
func TestValidateHTTPTimeoutBounds(t *testing.T) {
	fields := []struct {
		name   string
		assign func(*Config, *Duration)
	}{
		{"http_read_timeout", func(c *Config, d *Duration) { c.HTTPReadTimeout = d }},
		{"http_write_timeout", func(c *Config, d *Duration) { c.HTTPWriteTimeout = d }},
		{"http_idle_timeout", func(c *Config, d *Duration) { c.HTTPIdleTimeout = d }},
	}
	cases := []struct {
		name      string
		value     *Duration
		wantError bool
	}{
		{"unset accepted", nil, false},
		{"0s accepted (disabled)", durPtr(0), false},
		{"1s accepted (min)", durPtr(time.Second), false},
		{"24h accepted (max)", durPtr(24 * time.Hour), false},
		{"500ms rejected (below min)", durPtr(500 * time.Millisecond), true},
		{"25h rejected (above max)", durPtr(25 * time.Hour), true},
		{"negative rejected", durPtr(-1 * time.Second), true},
	}
	for _, f := range fields {
		for _, tc := range cases {
			t.Run(f.name+"/"+tc.name, func(t *testing.T) {
				c := DefaultConfig()
				f.assign(c, tc.value)
				errs := c.ValidateDetailed()
				hasErr := false
				for _, e := range errs {
					if containsAny(e.Field, f.name) {
						hasErr = true
					}
				}
				if hasErr != tc.wantError {
					t.Errorf("validation %s error = %v, want %v (errors: %+v)", f.name, hasErr, tc.wantError, errs)
				}
			})
		}
	}
}

// TestHTTPTimeoutsJSONRoundTrip pins the wire names (underscored JSON) and the
// tri-state through a marshal/unmarshal cycle, including the "explicit 0 must
// survive" case that a plain time.Duration + omitempty would silently drop.
func TestHTTPTimeoutsJSONRoundTrip(t *testing.T) {
	raw := `{"http_read_timeout":"90s","http_write_timeout":"0s","http_idle_timeout":"10m"}`
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.HTTPReadTimeout == nil || c.HTTPReadTimeout.Duration() != 90*time.Second {
		t.Fatalf("http_read_timeout = %v, want 90s", c.HTTPReadTimeout)
	}
	if c.HTTPWriteTimeout == nil || c.HTTPWriteTimeout.Duration() != 0 {
		t.Fatalf("http_write_timeout = %v, want an explicit 0", c.HTTPWriteTimeout)
	}
	if c.HTTPIdleTimeout == nil || c.HTTPIdleTimeout.Duration() != 10*time.Minute {
		t.Fatalf("http_idle_timeout = %v, want 10m", c.HTTPIdleTimeout)
	}
	if got := c.ResolveHTTPWriteTimeout(); got != 0 {
		t.Fatalf("explicit 0 must resolve to disabled, got %v", got)
	}

	out, err := json.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"http_read_timeout":"1m30s"`, `"http_write_timeout":"0s"`, `"http_idle_timeout":"10m0s"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("marshalled config missing %s: %s", want, out)
		}
	}

	// Omitted keys must not appear at all (nil = inherit the default).
	empty, err := json.Marshal(&Config{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	for _, unwanted := range []string{"http_read_timeout", "http_write_timeout", "http_idle_timeout"} {
		if strings.Contains(string(empty), unwanted) {
			t.Errorf("unset %s must be omitted from JSON: %s", unwanted, empty)
		}
	}
}

// TestHTTPTimeoutEnvOverrides covers the MCPPROXY_HTTP_*_TIMEOUT env sink
// (GH #965) — the escape hatch for operators who cannot edit the config file.
// An explicit 0 is meaningful (it disables the deadline), so the value is
// materialized as a pointer; malformed values are warned about and ignored.
func TestHTTPTimeoutEnvOverrides(t *testing.T) {
	t.Run("all three applied", func(t *testing.T) {
		t.Setenv("MCPPROXY_HTTP_READ_TIMEOUT", "5m")
		t.Setenv("MCPPROXY_HTTP_WRITE_TIMEOUT", "0s")
		t.Setenv("MCPPROXY_HTTP_IDLE_TIMEOUT", "10m")

		cfg := DefaultConfig()
		applyTLSEnvOverrides(cfg)

		if got := cfg.ResolveHTTPReadTimeout(); got != 5*time.Minute {
			t.Errorf("read = %v, want 5m", got)
		}
		if cfg.HTTPWriteTimeout == nil || cfg.ResolveHTTPWriteTimeout() != 0 {
			t.Errorf("write = %v, want an explicit 0", cfg.HTTPWriteTimeout)
		}
		if got := cfg.ResolveHTTPIdleTimeout(); got != 10*time.Minute {
			t.Errorf("idle = %v, want 10m", got)
		}
	})

	t.Run("env wins over the file value", func(t *testing.T) {
		t.Setenv("MCPPROXY_HTTP_WRITE_TIMEOUT", "30s")
		cfg := DefaultConfig()
		cfg.HTTPWriteTimeout = durPtr(0)
		applyTLSEnvOverrides(cfg)
		if cfg.ResolveHTTPWriteTimeout() != 30*time.Second {
			t.Fatalf("write = %v, want 30s", cfg.HTTPWriteTimeout)
		}
	})

	t.Run("unset env leaves the config untouched", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.HTTPReadTimeout = durPtr(7 * time.Second)
		applyTLSEnvOverrides(cfg)
		if cfg.HTTPReadTimeout == nil || cfg.HTTPReadTimeout.Duration() != 7*time.Second {
			t.Fatalf("read = %v, want 7s", cfg.HTTPReadTimeout)
		}
		if cfg.HTTPWriteTimeout != nil || cfg.HTTPIdleTimeout != nil {
			t.Fatalf("unset env must not materialize values: %v %v", cfg.HTTPWriteTimeout, cfg.HTTPIdleTimeout)
		}
	})

	t.Run("malformed values are ignored", func(t *testing.T) {
		t.Setenv("MCPPROXY_HTTP_READ_TIMEOUT", "soon")
		t.Setenv("MCPPROXY_HTTP_WRITE_TIMEOUT", "120")
		t.Setenv("MCPPROXY_HTTP_IDLE_TIMEOUT", "-3s")
		cfg := DefaultConfig()
		applyTLSEnvOverrides(cfg)
		if cfg.HTTPReadTimeout != nil || cfg.HTTPWriteTimeout != nil || cfg.HTTPIdleTimeout != nil {
			t.Fatalf("malformed env must be ignored: %v %v %v",
				cfg.HTTPReadTimeout, cfg.HTTPWriteTimeout, cfg.HTTPIdleTimeout)
		}
	})
}
