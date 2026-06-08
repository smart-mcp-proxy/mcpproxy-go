package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestValidateProfiles covers the data-model.md validation table (Spec 057):
// fatal slug/reserved/duplicate rules and warn-skip for unknown/empty servers.
func TestValidateProfiles(t *testing.T) {
	mk := func(profiles []ProfileConfig, servers ...string) *Config {
		var sc []*ServerConfig
		for _, s := range servers {
			sc = append(sc, &ServerConfig{Name: s})
		}
		return &Config{Servers: sc, Profiles: profiles}
	}

	t.Run("valid profile passes with no error or warning", func(t *testing.T) {
		warnings, err := ValidateProfiles(mk([]ProfileConfig{{Name: "research", Servers: []string{"fs", "web"}}}, "fs", "web"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) != 0 {
			t.Errorf("expected no warnings, got %v", warnings)
		}
	})

	fatal := []struct {
		name     string
		profiles []ProfileConfig
	}{
		{"uppercase slug", []ProfileConfig{{Name: "Research", Servers: []string{"fs"}}}},
		{"leading dash", []ProfileConfig{{Name: "-x", Servers: []string{"fs"}}}},
		{"contains space", []ProfileConfig{{Name: "a b", Servers: []string{"fs"}}}},
		{"too long (64)", []ProfileConfig{{Name: strings.Repeat("a", 64), Servers: []string{"fs"}}}},
		{"reserved all", []ProfileConfig{{Name: "all", Servers: []string{"fs"}}}},
		{"reserved code", []ProfileConfig{{Name: "code", Servers: []string{"fs"}}}},
		{"reserved call", []ProfileConfig{{Name: "call", Servers: []string{"fs"}}}},
		{"reserved p", []ProfileConfig{{Name: "p", Servers: []string{"fs"}}}},
	}
	for _, tc := range fatal {
		t.Run("fatal: "+tc.name, func(t *testing.T) {
			_, err := ValidateProfiles(mk(tc.profiles, "fs"))
			if err == nil {
				t.Errorf("expected fatal error for %s", tc.name)
			}
		})
	}

	t.Run("boundary: 63-char slug is valid", func(t *testing.T) {
		_, err := ValidateProfiles(mk([]ProfileConfig{{Name: strings.Repeat("a", 63), Servers: []string{"fs"}}}, "fs"))
		if err != nil {
			t.Errorf("63-char slug must be valid, got %v", err)
		}
	})

	t.Run("fatal: duplicate name names the slug", func(t *testing.T) {
		_, err := ValidateProfiles(mk([]ProfileConfig{
			{Name: "dup", Servers: []string{"fs"}},
			{Name: "dup", Servers: []string{"web"}},
		}, "fs", "web"))
		if err == nil || !strings.Contains(err.Error(), "dup") {
			t.Errorf("expected duplicate-name error mentioning 'dup', got %v", err)
		}
	})

	t.Run("warn-skip: unknown server warns, does not fail", func(t *testing.T) {
		warnings, err := ValidateProfiles(mk([]ProfileConfig{{Name: "x", Servers: []string{"fs", "ghost"}}}, "fs"))
		if err != nil {
			t.Fatalf("unknown server must warn-and-skip, not fail: %v", err)
		}
		if !anyContains(warnings, "ghost") {
			t.Errorf("expected a warning naming the unknown server 'ghost', got %v", warnings)
		}
	})

	t.Run("warn: empty servers is a legal deny-all", func(t *testing.T) {
		warnings, err := ValidateProfiles(mk([]ProfileConfig{{Name: "locked", Servers: nil}}))
		if err != nil {
			t.Fatalf("empty servers is legal (deny-all): %v", err)
		}
		if !anyContains(warnings, "locked") {
			t.Errorf("expected a warning naming the empty profile 'locked', got %v", warnings)
		}
	})
}

// TestProfilesRoundTrip covers SC-004: absent profiles serialize away (omitempty)
// so existing configs are byte-identical; present profiles round-trip losslessly.
func TestProfilesRoundTrip(t *testing.T) {
	noProfiles := &Config{Listen: "127.0.0.1:8080"}
	b, err := json.Marshal(noProfiles)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "profiles") {
		t.Errorf("absent profiles must not appear in JSON (omitempty): %s", b)
	}

	withProfiles := &Config{Profiles: []ProfileConfig{{Name: "research", Servers: []string{"fs", "web"}}}}
	b2, err := json.Marshal(withProfiles)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(b2, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Profiles) != 1 || back.Profiles[0].Name != "research" || len(back.Profiles[0].Servers) != 2 {
		t.Errorf("profiles did not round-trip losslessly: %+v", back.Profiles)
	}
}
