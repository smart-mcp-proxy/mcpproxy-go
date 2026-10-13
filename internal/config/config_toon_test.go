package config

import "testing"

// TestToonOutputDefaults: the feature is off by default with a 15% savings
// threshold (spec 084, FR-001/FR-002).
func TestToonOutputDefaults(t *testing.T) {
	c := DefaultConfig()
	if c.ToonOutput != "off" {
		t.Errorf("DefaultConfig().ToonOutput = %q, want %q", c.ToonOutput, "off")
	}
	if c.ToonMinSavingsPct != 15 {
		t.Errorf("DefaultConfig().ToonMinSavingsPct = %d, want 15", c.ToonMinSavingsPct)
	}
}

// TestResolveToonOutput covers the FR-001 precedence: per-server non-empty >
// global non-empty > "off". The resolver is string-only — internal/config must
// not import internal/toonenc (the caller parses via toonenc.ParseMode).
func TestResolveToonOutput(t *testing.T) {
	cases := []struct {
		name   string
		global string
		server string
		want   string
	}{
		{"both unset → off", "", "", "off"},
		{"global only → global", "adaptive", "", "adaptive"},
		{"server override wins", "adaptive", "off", "off"},
		{"server enables over global off", "off", "adaptive", "adaptive"},
		{"server always over global adaptive", "adaptive", "always", "always"},
		{"server only → server", "", "always", "always"},
		{"global off explicit → off", "off", "", "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{ToonOutput: tc.global}
			sc := &ServerConfig{Name: "s", ToonOutput: tc.server}
			if got := c.ResolveToonOutput(sc); got != tc.want {
				t.Errorf("ResolveToonOutput = %q, want %q", got, tc.want)
			}
		})
	}

	// A nil server config must still resolve via global → default.
	c := &Config{ToonOutput: "adaptive"}
	if got := c.ResolveToonOutput(nil); got != "adaptive" {
		t.Errorf("nil server: got %q, want %q", got, "adaptive")
	}
	c = &Config{}
	if got := c.ResolveToonOutput(nil); got != "off" {
		t.Errorf("nil server, unset global: got %q, want %q", got, "off")
	}
}

// TestToonOutputValidationRejectsInvalid (spec 084 T019, FR-001): invalid
// toon_output enum values (top-level AND per-server) and out-of-range
// toon_min_savings_pct values must produce a ValidationError with a clear
// Field so a config edit fails loudly instead of silently falling back.
func TestToonOutputValidationRejectsInvalid(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Config)
		wantField string
	}{
		{"global invalid enum", func(c *Config) { c.ToonOutput = "sometimes" }, "toon_output"},
		{"global misspelled", func(c *Config) { c.ToonOutput = "adaptativ" }, "toon_output"},
		{"threshold below range", func(c *Config) { c.ToonMinSavingsPct = -3 }, "toon_min_savings_pct"},
		{"threshold above range", func(c *Config) { c.ToonMinSavingsPct = 91 }, "toon_min_savings_pct"},
		{"per-server invalid enum", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s", ToonOutput: "on"}}
		}, "mcpServers[0].toon_output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(c)
			found := false
			for _, e := range c.ValidateDetailed() {
				if e.Field == tc.wantField {
					found = true
					if e.Message == "" {
						t.Error("validation error must carry a message")
					}
				}
			}
			if !found {
				t.Errorf("expected a ValidationError on field %q, got %+v", tc.wantField, c.ValidateDetailed())
			}
		})
	}
}

// TestToonOutputValidationHappyPath: valid values (and unset) produce no
// toon-related validation errors.
func TestToonOutputValidationHappyPath(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"defaults", func(_ *Config) {}},
		{"global adaptive", func(c *Config) { c.ToonOutput = "adaptive" }},
		{"global always", func(c *Config) { c.ToonOutput = "always" }},
		{"global unset", func(c *Config) { c.ToonOutput = "" }},
		{"threshold min", func(c *Config) { c.ToonMinSavingsPct = 1 }},
		{"threshold max", func(c *Config) { c.ToonMinSavingsPct = 90 }},
		{"threshold unset (0 → default)", func(c *Config) { c.ToonMinSavingsPct = 0 }},
		{"per-server override", func(c *Config) {
			c.ToonOutput = "adaptive"
			c.Servers = []*ServerConfig{{Name: "s", ToonOutput: "off"}}
		}},
		{"per-server unset inherits", func(c *Config) {
			c.Servers = []*ServerConfig{{Name: "s"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(c)
			for _, e := range c.ValidateDetailed() {
				if containsAny(e.Field, "toon") {
					t.Errorf("unexpected toon validation error: %+v", e)
				}
			}
		})
	}
}
