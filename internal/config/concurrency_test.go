package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

// TestResolveGlobalConcurrency covers FR-020(a): the global aggregate limiter
// carries its own three values; absent/0 max disables it; the queue timeout
// falls back to 30s only while the limiter is active.
func TestResolveGlobalConcurrency(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		want  ResolvedConcurrency
		isOff bool
	}{
		{
			name:  "absent → off",
			cfg:   Config{},
			want:  ResolvedConcurrency{},
			isOff: true,
		},
		{
			name:  "explicit 0 → off (queue is meaningless without a limiter)",
			cfg:   Config{MaxConcurrentRequests: intPtr(0), QueueSize: intPtr(4)},
			want:  ResolvedConcurrency{},
			isOff: true,
		},
		{
			name: "max set → default queue timeout applies",
			cfg:  Config{MaxConcurrentRequests: intPtr(10)},
			want: ResolvedConcurrency{MaxConcurrentRequests: 10, QueueSize: 0, QueueTimeout: 30 * time.Second},
		},
		{
			name: "all values explicit",
			cfg:  Config{MaxConcurrentRequests: intPtr(10), QueueSize: intPtr(20), QueueTimeout: durPtr(5 * time.Second)},
			want: ResolvedConcurrency{MaxConcurrentRequests: 10, QueueSize: 20, QueueTimeout: 5 * time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ResolveGlobalConcurrency()
			if got != tc.want {
				t.Fatalf("ResolveGlobalConcurrency = %+v, want %+v", got, tc.want)
			}
			if got.Enabled() == tc.isOff {
				t.Fatalf("Enabled() = %v, want %v", got.Enabled(), !tc.isOff)
			}
		})
	}
}

// TestResolveServerConcurrency covers FR-020(b)+(c): each setting is tri-state
// per server — absent inherits the per-server DEFAULT SET (never the global
// aggregate), explicit 0 disables that setting, positive overrides.
func TestResolveServerConcurrency(t *testing.T) {
	cases := []struct {
		name     string
		defaults *ConcurrencyDefaults
		global   *int
		server   *ServerConfig
		want     ResolvedConcurrency
	}{
		{
			name: "nothing configured → off",
			want: ResolvedConcurrency{},
		},
		{
			name:   "global aggregate is NOT a per-server inheritance source",
			global: intPtr(50),
			server: &ServerConfig{Name: "s"},
			want:   ResolvedConcurrency{},
		},
		{
			name:     "inherits the default set",
			defaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(5), QueueSize: intPtr(10), QueueTimeout: durPtr(3 * time.Second)},
			server:   &ServerConfig{Name: "s"},
			want:     ResolvedConcurrency{MaxConcurrentRequests: 5, QueueSize: 10, QueueTimeout: 3 * time.Second},
		},
		{
			name:     "per-server override wins per setting",
			defaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(5), QueueSize: intPtr(10), QueueTimeout: durPtr(3 * time.Second)},
			server:   &ServerConfig{Name: "s", MaxConcurrentRequests: intPtr(1)},
			want:     ResolvedConcurrency{MaxConcurrentRequests: 1, QueueSize: 10, QueueTimeout: 3 * time.Second},
		},
		{
			name:     "explicit 0 opts the server out of the limit",
			defaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(5), QueueSize: intPtr(10)},
			server:   &ServerConfig{Name: "s", MaxConcurrentRequests: intPtr(0)},
			want:     ResolvedConcurrency{MaxConcurrentRequests: 0, QueueSize: 0, QueueTimeout: 0},
		},
		{
			name:     "explicit queue_size 0 means shed at the cap",
			defaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(5), QueueSize: intPtr(10)},
			server:   &ServerConfig{Name: "s", QueueSize: intPtr(0)},
			want:     ResolvedConcurrency{MaxConcurrentRequests: 5, QueueSize: 0, QueueTimeout: 30 * time.Second},
		},
		{
			name:     "default queue timeout applied when the limit is active",
			defaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(5)},
			server:   &ServerConfig{Name: "s"},
			want:     ResolvedConcurrency{MaxConcurrentRequests: 5, QueueTimeout: 30 * time.Second},
		},
		{
			name:   "server-only configuration (no defaults block)",
			server: &ServerConfig{Name: "s", MaxConcurrentRequests: intPtr(2), QueueSize: intPtr(3), QueueTimeout: durPtr(time.Second)},
			want:   ResolvedConcurrency{MaxConcurrentRequests: 2, QueueSize: 3, QueueTimeout: time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{ServerConcurrencyDefaults: tc.defaults, MaxConcurrentRequests: tc.global}
			got := c.ResolveServerConcurrency(tc.server)
			if got != tc.want {
				t.Fatalf("ResolveServerConcurrency = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveServerConcurrencyNilServer(t *testing.T) {
	c := &Config{ServerConcurrencyDefaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(4)}}
	got := c.ResolveServerConcurrency(nil)
	if got.MaxConcurrentRequests != 4 {
		t.Fatalf("nil server must resolve the default set, got %+v", got)
	}
}

// TestResolveQueueBudget covers FR-004: one absolute deadline spans both tiers,
// so the budget is the smallest positive queue timeout among the ENABLED scopes.
func TestResolveQueueBudget(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		srv  *ServerConfig
		want time.Duration
	}{
		{"no limits → no budget", Config{}, &ServerConfig{Name: "s"}, 0},
		{
			"server only",
			Config{ServerConcurrencyDefaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(2), QueueTimeout: durPtr(4 * time.Second)}},
			&ServerConfig{Name: "s"},
			4 * time.Second,
		},
		{
			"global only",
			Config{MaxConcurrentRequests: intPtr(2), QueueTimeout: durPtr(7 * time.Second)},
			&ServerConfig{Name: "s"},
			7 * time.Second,
		},
		{
			"both enabled → smallest wins",
			Config{
				MaxConcurrentRequests:     intPtr(2),
				QueueTimeout:              durPtr(20 * time.Second),
				ServerConcurrencyDefaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(2), QueueTimeout: durPtr(4 * time.Second)},
			},
			&ServerConfig{Name: "s"},
			4 * time.Second,
		},
		{
			"disabled server scope does not contribute",
			Config{
				MaxConcurrentRequests:     intPtr(2),
				QueueTimeout:              durPtr(20 * time.Second),
				ServerConcurrencyDefaults: &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(2), QueueTimeout: durPtr(4 * time.Second)},
			},
			&ServerConfig{Name: "s", MaxConcurrentRequests: intPtr(0)},
			20 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.ResolveQueueBudget(tc.srv); got != tc.want {
				t.Fatalf("ResolveQueueBudget = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateConcurrency covers FR-023: per resolved scope, reject negatives
// and a positive queue size whose limit resolves to disabled — with an error
// naming the offending scope and field.
func TestValidateConcurrency(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Config)
		wantField string // "" = must validate cleanly
	}{
		{"defaults are valid", func(_ *Config) {}, ""},
		{"global limits valid", func(c *Config) {
			c.MaxConcurrentRequests = intPtr(10)
			c.QueueSize = intPtr(20)
			c.QueueTimeout = durPtr(10 * time.Second)
		}, ""},
		{"negative global max", func(c *Config) { c.MaxConcurrentRequests = intPtr(-1) }, "max_concurrent_requests"},
		{"negative global queue size", func(c *Config) {
			c.MaxConcurrentRequests = intPtr(2)
			c.QueueSize = intPtr(-5)
		}, "queue_size"},
		{"negative global queue timeout", func(c *Config) {
			c.MaxConcurrentRequests = intPtr(2)
			c.QueueTimeout = durPtr(-time.Second)
		}, "queue_timeout"},
		{"global queue without a limit", func(c *Config) { c.QueueSize = intPtr(5) }, "queue_size"},
		{"defaults queue without a limit", func(c *Config) {
			c.ServerConcurrencyDefaults = &ConcurrencyDefaults{QueueSize: intPtr(5)}
		}, "server_concurrency_defaults.queue_size"},
		{"defaults negative max", func(c *Config) {
			c.ServerConcurrencyDefaults = &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(-2)}
		}, "server_concurrency_defaults.max_concurrent_requests"},
		{"per-server negative max", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "db", MaxConcurrentRequests: intPtr(-1)}}
		}, "max_concurrent_requests"},
		{"per-server queue without a resolved limit", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "db", QueueSize: intPtr(3)}}
		}, "queue_size"},
		{"per-server queue inherits a limit from the defaults → valid", func(c *Config) {
			c.ServerConcurrencyDefaults = &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(4)}
			c.Servers = []*ServerConfig{{Name: "db", QueueSize: intPtr(3)}}
		}, ""},
		{"explicit per-server opt-out with inherited queue → valid", func(c *Config) {
			c.ServerConcurrencyDefaults = &ConcurrencyDefaults{MaxConcurrentRequests: intPtr(4), QueueSize: intPtr(8)}
			c.Servers = []*ServerConfig{{Name: "db", MaxConcurrentRequests: intPtr(0)}}
		}, ""},
		{"per-server negative queue timeout", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "db", MaxConcurrentRequests: intPtr(1), QueueTimeout: durPtr(-time.Second)}}
		}, "queue_timeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Servers = nil
			tc.mutate(c)
			errs := c.ValidateDetailed()

			var matched []string
			for _, e := range errs {
				if containsAny(e.Field, "max_concurrent_requests", "queue_size", "queue_timeout") {
					matched = append(matched, e.Field+": "+e.Message)
				}
			}
			if tc.wantField == "" {
				if len(matched) > 0 {
					t.Fatalf("unexpected concurrency validation errors: %v", matched)
				}
				return
			}
			if len(matched) == 0 {
				t.Fatalf("expected a validation error naming %q, got none (all errors: %+v)", tc.wantField, errs)
			}
			found := false
			for _, m := range matched {
				if strings.Contains(m, tc.wantField) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected an error naming %q, got %v", tc.wantField, matched)
			}
		})
	}
}

// TestValidateConcurrencyErrorNamesServer: FR-023 wants actionable messages
// that identify the offending scope, including which server.
func TestValidateConcurrencyErrorNamesServer(t *testing.T) {
	c := DefaultConfig()
	c.Servers = []*ServerConfig{{Name: "fragile-db", QueueSize: intPtr(3)}}
	errs := c.ValidateDetailed()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Field, "queue_size") && strings.Contains(e.Message, "max_concurrent_requests") {
			found = true
			if !strings.Contains(e.Field, "fragile-db") && !strings.Contains(e.Message, "fragile-db") {
				t.Fatalf("error does not identify the server: field=%q msg=%q", e.Field, e.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected a queue_size validation error, got %+v", errs)
	}
}

// TestConcurrencyJSONRoundTripAbsent: an untouched config must serialize
// byte-identically to before the feature (no new keys), and a configured one
// must round-trip.
func TestConcurrencyJSONRoundTripAbsent(t *testing.T) {
	c := &Config{Servers: []*ServerConfig{{Name: "s"}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"max_concurrent_requests", "queue_size", "queue_timeout", "server_concurrency_defaults"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("absent concurrency settings must not be serialized, found %q in %s", key, data)
		}
	}
}

func TestConcurrencyJSONRoundTrip(t *testing.T) {
	raw := `{
      "max_concurrent_requests": 20,
      "queue_size": 40,
      "queue_timeout": "15s",
      "server_concurrency_defaults": {"max_concurrent_requests": 5, "queue_size": 10, "queue_timeout": "10s"},
      "mcpServers": [{"name": "db", "max_concurrent_requests": 1, "queue_size": 2, "queue_timeout": "5s"}]
    }`
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.MaxConcurrentRequests == nil || *c.MaxConcurrentRequests != 20 {
		t.Fatalf("global max = %v", c.MaxConcurrentRequests)
	}
	if c.QueueSize == nil || *c.QueueSize != 40 {
		t.Fatalf("global queue size = %v", c.QueueSize)
	}
	if c.QueueTimeout == nil || c.QueueTimeout.Duration() != 15*time.Second {
		t.Fatalf("global queue timeout = %v", c.QueueTimeout)
	}
	if c.ServerConcurrencyDefaults == nil || *c.ServerConcurrencyDefaults.MaxConcurrentRequests != 5 {
		t.Fatalf("defaults = %+v", c.ServerConcurrencyDefaults)
	}
	got := c.ResolveServerConcurrency(c.Servers[0])
	want := ResolvedConcurrency{MaxConcurrentRequests: 1, QueueSize: 2, QueueTimeout: 5 * time.Second}
	if got != want {
		t.Fatalf("resolved = %+v, want %+v", got, want)
	}

	out, err := json.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Config
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.ResolveServerConcurrency(back.Servers[0]) != want {
		t.Fatalf("round-trip lost per-server concurrency: %+v", back.Servers[0])
	}
	if back.ResolveGlobalConcurrency() != (ResolvedConcurrency{MaxConcurrentRequests: 20, QueueSize: 40, QueueTimeout: 15 * time.Second}) {
		t.Fatalf("round-trip lost global concurrency: %+v", back.ResolveGlobalConcurrency())
	}
}

// TestCopyServerConfigCopiesConcurrencyPointers guards the copy-on-write path:
// the tri-state pointers must be copied by value, not shared.
func TestCopyServerConfigCopiesConcurrencyPointers(t *testing.T) {
	src := &ServerConfig{Name: "s", MaxConcurrentRequests: intPtr(2), QueueSize: intPtr(4), QueueTimeout: durPtr(time.Second)}
	dst := CopyServerConfig(src)
	if dst.MaxConcurrentRequests == nil || *dst.MaxConcurrentRequests != 2 {
		t.Fatalf("max not copied: %+v", dst.MaxConcurrentRequests)
	}
	if dst.QueueSize == nil || *dst.QueueSize != 4 {
		t.Fatalf("queue size not copied: %+v", dst.QueueSize)
	}
	if dst.QueueTimeout == nil || dst.QueueTimeout.Duration() != time.Second {
		t.Fatalf("queue timeout not copied: %+v", dst.QueueTimeout)
	}
	*src.MaxConcurrentRequests = 9
	if *dst.MaxConcurrentRequests == 9 {
		t.Fatal("MaxConcurrentRequests pointer is shared, not copied by value")
	}
	*src.QueueSize = 9
	if *dst.QueueSize == 9 {
		t.Fatal("QueueSize pointer is shared, not copied by value")
	}
	*src.QueueTimeout = Duration(time.Hour)
	if dst.QueueTimeout.Duration() == time.Hour {
		t.Fatal("QueueTimeout pointer is shared, not copied by value")
	}
}

// TestMergeServerConfigConcurrencyPatch: a PATCH that sets the per-server
// limits must survive the merge (and leave them untouched when absent).
func TestMergeServerConfigConcurrencyPatch(t *testing.T) {
	base := &ServerConfig{Name: "s", MaxConcurrentRequests: intPtr(2)}
	patch := &ServerConfig{QueueSize: intPtr(6), QueueTimeout: durPtr(9 * time.Second)}
	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.MaxConcurrentRequests == nil || *merged.MaxConcurrentRequests != 2 {
		t.Fatalf("base max lost: %+v", merged.MaxConcurrentRequests)
	}
	if merged.QueueSize == nil || *merged.QueueSize != 6 {
		t.Fatalf("patched queue size = %+v", merged.QueueSize)
	}
	if merged.QueueTimeout == nil || merged.QueueTimeout.Duration() != 9*time.Second {
		t.Fatalf("patched queue timeout = %+v", merged.QueueTimeout)
	}
}

// TestConcurrencyEnvOverrides covers FR-022: the GLOBAL aggregate limiter's
// three settings are overridable via MCPPROXY_* env vars (the per-server
// default set and per-server overrides are file/API-configured only).
func TestConcurrencyEnvOverrides(t *testing.T) {
	t.Run("all three applied", func(t *testing.T) {
		t.Setenv("MCPPROXY_MAX_CONCURRENT_REQUESTS", "12")
		t.Setenv("MCPPROXY_QUEUE_SIZE", "24")
		t.Setenv("MCPPROXY_QUEUE_TIMEOUT", "45s")

		cfg := DefaultConfig()
		applyTLSEnvOverrides(cfg)

		got := cfg.ResolveGlobalConcurrency()
		want := ResolvedConcurrency{MaxConcurrentRequests: 12, QueueSize: 24, QueueTimeout: 45 * time.Second}
		if got != want {
			t.Fatalf("resolved = %+v, want %+v", got, want)
		}
	})

	t.Run("env wins over the file value", func(t *testing.T) {
		t.Setenv("MCPPROXY_MAX_CONCURRENT_REQUESTS", "3")
		cfg := DefaultConfig()
		cfg.MaxConcurrentRequests = intPtr(50)
		applyTLSEnvOverrides(cfg)
		if cfg.MaxConcurrentRequests == nil || *cfg.MaxConcurrentRequests != 3 {
			t.Fatalf("max = %v, want 3", cfg.MaxConcurrentRequests)
		}
	})

	t.Run("explicit 0 disables the global limiter", func(t *testing.T) {
		t.Setenv("MCPPROXY_MAX_CONCURRENT_REQUESTS", "0")
		cfg := DefaultConfig()
		cfg.MaxConcurrentRequests = intPtr(50)
		applyTLSEnvOverrides(cfg)
		if cfg.ResolveGlobalConcurrency().Enabled() {
			t.Fatal("MCPPROXY_MAX_CONCURRENT_REQUESTS=0 must disable the global limiter")
		}
	})

	t.Run("unset env leaves the config untouched", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.MaxConcurrentRequests = intPtr(7)
		applyTLSEnvOverrides(cfg)
		if cfg.MaxConcurrentRequests == nil || *cfg.MaxConcurrentRequests != 7 {
			t.Fatalf("max = %v, want 7", cfg.MaxConcurrentRequests)
		}
		if cfg.QueueSize != nil || cfg.QueueTimeout != nil {
			t.Fatalf("unset env must not materialize values: %v %v", cfg.QueueSize, cfg.QueueTimeout)
		}
	})

	t.Run("malformed values are ignored", func(t *testing.T) {
		t.Setenv("MCPPROXY_MAX_CONCURRENT_REQUESTS", "lots")
		t.Setenv("MCPPROXY_QUEUE_TIMEOUT", "soon")
		cfg := DefaultConfig()
		applyTLSEnvOverrides(cfg)
		if cfg.MaxConcurrentRequests != nil || cfg.QueueTimeout != nil {
			t.Fatalf("malformed env must be ignored: %v %v", cfg.MaxConcurrentRequests, cfg.QueueTimeout)
		}
	})
}
