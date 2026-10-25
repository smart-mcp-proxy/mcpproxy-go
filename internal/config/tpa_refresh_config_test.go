package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 087 FR-013/014/016 + Spec 101 US3 B1/B2: the refresh keys under
// `security`. These tests pin the defaults, the env override (read inside the
// accessor, never persisted), and the write-time-only shape checks.

func TestTPARefreshAccessors_Defaults(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	sc := &SecurityConfig{}
	assert.Equal(t, 24*time.Hour, sc.EffectiveTPARefreshInterval())
	assert.False(t, sc.IsTPAFetchEnabled(), "fetch is opt-in")
	assert.Equal(t, "", sc.EffectiveTPAFetchURL(), "no working default URL (B3)")
	assert.Equal(t, 30, sc.EffectiveTPAStaleAfterDays())
}

func TestTPARefreshAccessors_NilSafe(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	var sc *SecurityConfig
	assert.Equal(t, 24*time.Hour, sc.EffectiveTPARefreshInterval())
	assert.False(t, sc.IsTPAFetchEnabled())
	assert.Equal(t, "", sc.EffectiveTPAFetchURL())
	assert.Equal(t, 30, sc.EffectiveTPAStaleAfterDays())
}

func TestTPARefreshAccessors_ExplicitValues(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	zero, ninety := 0, 90
	sc := &SecurityConfig{
		TPARefreshInterval: Duration(6 * time.Hour),
		TPAFetchEnabled:    true,
		TPAFetchURL:        "https://mirror.example.com/tpa/scanner-bundle.json",
		TPAStaleAfterDays:  &ninety,
	}
	assert.Equal(t, 6*time.Hour, sc.EffectiveTPARefreshInterval())
	assert.True(t, sc.IsTPAFetchEnabled())
	assert.Equal(t, "https://mirror.example.com/tpa/scanner-bundle.json", sc.EffectiveTPAFetchURL())
	assert.Equal(t, 90, sc.EffectiveTPAStaleAfterDays())

	sc.TPAStaleAfterDays = &zero
	assert.Equal(t, 0, sc.EffectiveTPAStaleAfterDays(), "an explicit 0 disables the stale flag, it is not absent")
}

func TestEffectiveTPARefreshInterval_ClampsToOneHour(t *testing.T) {
	tests := []struct {
		name string
		in   Duration
		want time.Duration
	}{
		{"absent", 0, 24 * time.Hour},
		{"negative is the default", Duration(-time.Hour), 24 * time.Hour},
		{"below the floor is clamped", Duration(time.Second), time.Hour},
		{"just below the floor is clamped", Duration(59 * time.Minute), time.Hour},
		{"exactly the floor", Duration(time.Hour), time.Hour},
		{"above the floor is kept", Duration(36 * time.Hour), 36 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := &SecurityConfig{TPARefreshInterval: tc.in}
			assert.Equal(t, tc.want, sc.EffectiveTPARefreshInterval())
		})
	}
}

// TestIsTPAFetchEnabled_EnvOverridesBothWays: MCPPROXY_TPA_FETCH_ENABLED
// outranks the file value on every path because the accessor reads it
// (bidirectional, like MCPPROXY_AUTO_BASELINE_SCAN).
func TestIsTPAFetchEnabled_EnvOverridesBothWays(t *testing.T) {
	for _, on := range []string{"true", "1"} {
		t.Run("env "+on+" forces on", func(t *testing.T) {
			t.Setenv(EnvTPAFetchEnabled, on)
			assert.True(t, (&SecurityConfig{TPAFetchEnabled: false}).IsTPAFetchEnabled())
			var nilSec *SecurityConfig
			assert.True(t, nilSec.IsTPAFetchEnabled(), "a config with no security block still honours the env")
		})
	}
	for _, off := range []string{"false", "0"} {
		t.Run("env "+off+" forces off", func(t *testing.T) {
			t.Setenv(EnvTPAFetchEnabled, off)
			assert.False(t, (&SecurityConfig{TPAFetchEnabled: true}).IsTPAFetchEnabled())
		})
	}
	t.Run("unrecognised env value defers to config", func(t *testing.T) {
		t.Setenv(EnvTPAFetchEnabled, "maybe")
		assert.True(t, (&SecurityConfig{TPAFetchEnabled: true}).IsTPAFetchEnabled())
		assert.False(t, (&SecurityConfig{TPAFetchEnabled: false}).IsTPAFetchEnabled())
	})
}

// TestTPAFetchEnvOverride_IsNotPersisted: the env is read inside the accessor
// and never mirrored into the config struct, so a save round-trip cannot write
// a one-run environment choice into mcp_config.json.
func TestTPAFetchEnvOverride_IsNotPersisted(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "true")
	cfg := DefaultConfig()
	cfg.Security = &SecurityConfig{TPAFetchEnabled: false}
	require.True(t, cfg.Security.IsTPAFetchEnabled(), "precondition: the env forces fetch on")

	path := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, SaveConfig(cfg, path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var onDisk struct {
		Security map[string]any `json:"security"`
	}
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	assert.Equal(t, false, onDisk.Security["tpa_fetch_enabled"], "the env override must not reach the file")
	assert.False(t, cfg.Security.TPAFetchEnabled, "the struct is not mutated by the accessor")
}

func tpaRefreshCfg(mut func(*SecurityConfig)) *Config {
	sc := &SecurityConfig{}
	if mut != nil {
		mut(sc)
	}
	return tpaValidConfig(sc)
}

func TestValidateDetailed_TPARefresh(t *testing.T) {
	neg, ok := -1, 30
	tests := []struct {
		name       string
		mut        func(*SecurityConfig)
		wantFields []string
	}{
		{name: "defaults", mut: nil},
		{name: "interval absent", mut: func(sc *SecurityConfig) { sc.TPARefreshInterval = 0 }},
		{name: "interval exactly 1h", mut: func(sc *SecurityConfig) { sc.TPARefreshInterval = Duration(time.Hour) }},
		{name: "interval 24h", mut: func(sc *SecurityConfig) { sc.TPARefreshInterval = Duration(24 * time.Hour) }},
		{name: "interval 59m", mut: func(sc *SecurityConfig) { sc.TPARefreshInterval = Duration(59 * time.Minute) }, wantFields: []string{"security.tpa_refresh_interval"}},
		{name: "interval 1s", mut: func(sc *SecurityConfig) { sc.TPARefreshInterval = Duration(time.Second) }, wantFields: []string{"security.tpa_refresh_interval"}},
		{name: "interval negative", mut: func(sc *SecurityConfig) { sc.TPARefreshInterval = Duration(-time.Hour) }, wantFields: []string{"security.tpa_refresh_interval"}},
		{name: "https url", mut: func(sc *SecurityConfig) {
			sc.TPAFetchURL = "https://api.github.com/repos/smart-mcp-proxy/tpa-db/releases/latest"
		}},
		{name: "https url, fetch enabled", mut: func(sc *SecurityConfig) {
			sc.TPAFetchEnabled = true
			sc.TPAFetchURL = "https://mirror.example.com/scanner-bundle.json"
		}},
		{name: "http loopback literal", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "http://127.0.0.1:8099/scanner-bundle.json" }},
		{name: "http ipv6 loopback literal", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "http://[::1]:8099/scanner-bundle.json" }},
		{name: "http localhost", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "http://localhost:8099/scanner-bundle.json" }},
		{name: "http non-loopback host", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "http://mirror.example.com/scanner-bundle.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "http RFC1918 literal is not loopback", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "http://192.168.1.10/scanner-bundle.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "http host that merely starts with localhost", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "http://localhost.evil.example/b.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "ftp scheme", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "ftp://mirror.example.com/b.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "no scheme", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "mirror.example.com/b.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "no host", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "https:///b.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "userinfo", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "https://user:pw@mirror.example.com/b.json" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "query", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "https://mirror.example.com/b.json?token=abc" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "empty query marker", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "https://mirror.example.com/b.json?" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "fragment", mut: func(sc *SecurityConfig) { sc.TPAFetchURL = "https://mirror.example.com/b.json#frag" }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "fetch enabled with empty url", mut: func(sc *SecurityConfig) { sc.TPAFetchEnabled = true }, wantFields: []string{"security.tpa_fetch_url"}},
		{name: "fetch disabled with empty url", mut: func(sc *SecurityConfig) { sc.TPAFetchEnabled = false }},
		{name: "fetch enabled, operator root, signers", mut: func(sc *SecurityConfig) {
			sc.TPAFetchEnabled = true
			sc.TPAFetchURL = "https://mirror.example.com/b.json"
			sc.TPAActiveRoot = TPARootOperator
			sc.TPAOperatorSigners = []string{tpaTestSigner(1)}
		}},
		{name: "fetch enabled, operator root, no signers", mut: func(sc *SecurityConfig) {
			sc.TPAFetchEnabled = true
			sc.TPAFetchURL = "https://mirror.example.com/b.json"
			sc.TPAActiveRoot = TPARootOperator
		}, wantFields: []string{"security.tpa_fetch_enabled", "security.tpa_active_root"}},
		{name: "stale days positive", mut: func(sc *SecurityConfig) { sc.TPAStaleAfterDays = &ok }},
		{name: "stale days negative", mut: func(sc *SecurityConfig) { sc.TPAStaleAfterDays = &neg }, wantFields: []string{"security.tpa_stale_after_days"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := tpaRefreshCfg(tc.mut).ValidateDetailed()
			assert.ElementsMatch(t, tc.wantFields, tpaErrorFields(errs), "errors: %v", errs)
		})
	}
}

// TestValidateDetailed_TPARefreshNeverEchoesTheURL: a fetch URL may carry a
// credential-bearing host, path or query. Validation messages describe the
// rule, never the value.
func TestValidateDetailed_TPARefreshNeverEchoesTheURL(t *testing.T) {
	for _, raw := range []string{
		"https://user:hunter2@secret-host.example.com/private/path?token=abc123#frag",
		"http://secret-host.example.com/private/path",
		"ftp://secret-host.example.com/private/path",
		"https://secret-host.example.com/private/path?token=abc123",
	} {
		errs := tpaRefreshCfg(func(sc *SecurityConfig) { sc.TPAFetchURL = raw }).ValidateDetailed()
		require.NotEmpty(t, errs, raw)
		for _, e := range errs {
			for _, leak := range []string{"hunter2", "secret-host", "private", "abc123", "frag"} {
				assert.NotContains(t, strings.ToLower(e.Message), leak, "message echoes the URL: %q", e.Message)
			}
		}
	}
}

// TestValidate_TPARefreshDoesNotBrickBoot: like the trust-root keys, the
// refresh shape checks are write-time only. A hand-edited bad value on disk
// must not stop the daemon from loading (the runtime treats it as "fetch
// off").
func TestValidate_TPARefreshDoesNotBrickBoot(t *testing.T) {
	neg := -5
	cfg := tpaRefreshCfg(func(sc *SecurityConfig) {
		sc.TPARefreshInterval = Duration(time.Second)
		sc.TPAFetchEnabled = true
		sc.TPAFetchURL = "http://user:pw@mirror.example.com/b.json?x=1#y"
		sc.TPAActiveRoot = TPARootOperator
		sc.TPAStaleAfterDays = &neg
	})
	require.NoError(t, cfg.Validate())
	assert.Empty(t, cfg.validateDetailedCore())
	assert.NotEmpty(t, cfg.tpaRefreshErrors(), "the same config is rejected on the write path")
}

func TestSecurityConfig_TPARefreshJSONRoundTrip(t *testing.T) {
	t.Run("explicit zero stale days survives", func(t *testing.T) {
		zero := 0
		raw, err := json.Marshal(SecurityConfig{TPAStaleAfterDays: &zero})
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"tpa_stale_after_days":0`)

		var out SecurityConfig
		require.NoError(t, json.Unmarshal(raw, &out))
		require.NotNil(t, out.TPAStaleAfterDays)
		assert.Equal(t, 0, *out.TPAStaleAfterDays)
		assert.Equal(t, 0, out.EffectiveTPAStaleAfterDays())
	})

	t.Run("absence survives", func(t *testing.T) {
		raw, err := json.Marshal(SecurityConfig{})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "tpa_stale_after_days")
		assert.NotContains(t, string(raw), "tpa_refresh_interval")
		assert.NotContains(t, string(raw), "tpa_fetch_url")

		var out SecurityConfig
		require.NoError(t, json.Unmarshal(raw, &out))
		assert.Nil(t, out.TPAStaleAfterDays)
		assert.Equal(t, 30, out.EffectiveTPAStaleAfterDays())
	})

	t.Run("values round-trip", func(t *testing.T) {
		in := SecurityConfig{
			TPARefreshInterval: Duration(12 * time.Hour),
			TPAFetchEnabled:    true,
			TPAFetchURL:        "https://mirror.example.com/b.json",
		}
		raw, err := json.Marshal(in)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"tpa_refresh_interval":"12h0m0s"`)
		assert.Contains(t, string(raw), `"tpa_fetch_enabled":true`)

		var out SecurityConfig
		require.NoError(t, json.Unmarshal(raw, &out))
		assert.Equal(t, in, out)
	})

	t.Run("tpa_fetch_enabled always serialised", func(t *testing.T) {
		raw, err := json.Marshal(SecurityConfig{})
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"tpa_fetch_enabled":false`)
	})
}
