package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// Spec 079 US1 (FR-012/FR-013): the update_check config block — nil-safe
// accessors with enabled-by-default semantics and a validated channel enum.

func TestUpdateCheckConfig_IsEnabled(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name string
		cfg  *UpdateCheckConfig
		want bool
	}{
		{"nil block defaults to enabled", nil, true},
		{"absent enabled defaults to enabled", &UpdateCheckConfig{}, true},
		{"explicit true", &UpdateCheckConfig{Enabled: boolPtr(true)}, true},
		{"explicit false", &UpdateCheckConfig{Enabled: boolPtr(false)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsEnabled(); got != tc.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpdateCheckConfig_Channel(t *testing.T) {
	tests := []struct {
		name            string
		cfg             *UpdateCheckConfig
		wantChannel     string
		wantPrereleases bool
	}{
		{"nil block is stable", nil, UpdateChannelStable, false},
		{"empty channel is stable", &UpdateCheckConfig{}, UpdateChannelStable, false},
		{"explicit stable", &UpdateCheckConfig{Channel: UpdateChannelStable}, UpdateChannelStable, false},
		{"rc channel includes prereleases", &UpdateCheckConfig{Channel: UpdateChannelRC}, UpdateChannelRC, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.ResolvedChannel(); got != tc.wantChannel {
				t.Errorf("ResolvedChannel() = %q, want %q", got, tc.wantChannel)
			}
			if got := tc.cfg.IncludePrereleases(); got != tc.wantPrereleases {
				t.Errorf("IncludePrereleases() = %v, want %v", got, tc.wantPrereleases)
			}
		})
	}
}

func TestValidateDetailed_UpdateCheckChannel(t *testing.T) {
	base := func() *Config {
		c := DefaultConfig()
		return c
	}

	t.Run("valid channels pass", func(t *testing.T) {
		for _, ch := range []string{"", UpdateChannelStable, UpdateChannelRC} {
			c := base()
			c.UpdateCheck = &UpdateCheckConfig{Channel: ch}
			for _, e := range c.ValidateDetailed() {
				if e.Field == "update_check.channel" {
					t.Errorf("channel %q: unexpected validation error: %s", ch, e.Message)
				}
			}
		}
	})

	t.Run("unknown channel rejected", func(t *testing.T) {
		c := base()
		c.UpdateCheck = &UpdateCheckConfig{Channel: "nightly"}
		found := false
		for _, e := range c.ValidateDetailed() {
			if e.Field == "update_check.channel" {
				found = true
				if !strings.Contains(e.Message, "nightly") {
					t.Errorf("error message should name the bad value, got: %s", e.Message)
				}
			}
		}
		if !found {
			t.Error("expected a validation error for update_check.channel=nightly")
		}
	})
}

// TestUpdateCheckConfig_JSONRoundTrip guards the serialized shape: the block
// is omitted when absent (byte-compat for existing configs) and round-trips
// its two keys.
func TestUpdateCheckConfig_JSONRoundTrip(t *testing.T) {
	t.Run("omitted when nil", func(t *testing.T) {
		out, err := json.Marshal(&Config{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "update_check") {
			t.Errorf("nil UpdateCheck must not serialize, got: %s", out)
		}
	})

	t.Run("round-trips", func(t *testing.T) {
		in := []byte(`{"update_check":{"enabled":false,"channel":"rc"}}`)
		var c Config
		if err := json.Unmarshal(in, &c); err != nil {
			t.Fatal(err)
		}
		if c.UpdateCheck == nil {
			t.Fatal("UpdateCheck not parsed")
		}
		if c.UpdateCheck.IsEnabled() {
			t.Error("enabled=false not honored")
		}
		if c.UpdateCheck.ResolvedChannel() != UpdateChannelRC {
			t.Errorf("channel = %q, want rc", c.UpdateCheck.ResolvedChannel())
		}
	})
}
