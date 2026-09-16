package config

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 FR-027 (T047, red until T050): a top-level, edition-neutral
// `trusted_proxies` list of CIDRs or IP addresses gates EVERY use of
// X-Forwarded-For / X-Real-IP / X-Forwarded-Proto / X-Forwarded-Host in the
// process. Default is empty = trust nobody. The single reader is
// config.ForwardedHeaders(r, trusted) → {Scheme, Host, ClientIP}; the
// client IP is the right-most hop of X-Forwarded-For that is NOT itself a
// trusted proxy.
//
// Contract these tests pin (implementer: internal/config/trusted_proxies.go):
//
//	type ForwardedInfo struct{ Scheme, Host, ClientIP string }
//	func ForwardedHeaders(r *http.Request, trusted []string) ForwardedInfo
//	func IsTrustedProxy(remoteAddr string, trusted []string) bool
//	func validateTrustedProxies(cfg *Config) []ValidationError
//	Config.TrustedProxies []string `json:"trusted_proxies,omitempty"`
//	env MCPPROXY_TRUSTED_PROXIES (comma list) in applyTLSEnvOverrides
//
// Message text is fixed by contracts/config-keys.md so boot, PATCH and
// /config/apply say the same thing (FR-039).

func fwdRequest(t *testing.T, remoteAddr string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://origin.example:8080/api/v1/auth/login", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// TestIsTrustedProxyParsing covers CIDR and bare-IP entries, IPv4 and IPv6,
// the host:port / bracketed forms RemoteAddr takes, and the two failure
// shapes (unparsable entry, unparsable peer) that must never grant trust.
func TestIsTrustedProxyParsing(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		trusted    []string
		want       bool
	}{
		{"empty list trusts nobody", "10.0.0.1:1234", nil, false},
		{"explicitly empty list trusts nobody", "10.0.0.1:1234", []string{}, false},
		{"IPv4 CIDR match", "10.1.2.3:1234", []string{"10.0.0.0/8"}, true},
		{"IPv4 CIDR miss", "11.1.2.3:1234", []string{"10.0.0.0/8"}, false},
		{"bare IPv4 entry is a /32", "192.168.1.5:9", []string{"192.168.1.5"}, true},
		{"bare IPv4 entry does not cover neighbours", "192.168.1.6:9", []string{"192.168.1.5"}, false},
		{"IPv6 CIDR match, bracketed peer", "[fd00::1]:443", []string{"fd00::/8"}, true},
		{"bare IPv6 loopback entry", "[::1]:5000", []string{"::1"}, true},
		{"IPv6 entry does not match IPv4 peer", "127.0.0.1:5000", []string{"::1"}, false},
		{"peer without a port still parses", "10.9.9.9", []string{"10.0.0.0/8"}, true},
		{"second entry matches", "172.16.5.5:1", []string{"10.0.0.0/8", "172.16.0.0/12"}, true},
		{"whitespace around an entry is tolerated", "10.9.9.9:1", []string{" 10.0.0.0/8 "}, true},
		{"unparsable entry never grants trust", "10.9.9.9:1", []string{"not-a-cidr"}, false},
		{"unparsable entry beside a valid one does not break the valid one", "10.9.9.9:1", []string{"garbage", "10.0.0.0/8"}, true},
		{"unparsable peer (unix socket) is never trusted", "@", []string{"0.0.0.0/0", "::/0"}, false},
		{"empty peer is never trusted", "", []string{"0.0.0.0/0", "::/0"}, false},
		{"hostname peer is never trusted", "proxy.internal:80", []string{"0.0.0.0/0"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsTrustedProxy(tc.remoteAddr, tc.trusted))
		})
	}
}

// TestForwardedHeadersUntrustedPeerIgnoresEverything: a direct client cannot
// spoof the session IP, the callback scheme, the Secure decision or the
// audit IP (FR-027 invariant). With an untrusted peer every X-Forwarded-*
// and X-Real-IP header is ignored and RemoteAddr / TLS state / Host are used.
func TestForwardedHeadersUntrustedPeerIgnoresEverything(t *testing.T) {
	spoof := map[string]string{
		"X-Forwarded-For":   "1.2.3.4",
		"X-Real-IP":         "5.6.7.8",
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "evil.example",
	}

	t.Run("nil list = trust nobody", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "203.0.113.7:4321", spoof), nil)
		assert.Equal(t, "203.0.113.7", got.ClientIP, "RemoteAddr host, port stripped")
		assert.Equal(t, "http", got.Scheme, "plain listener, XFP ignored")
		assert.Equal(t, "origin.example:8080", got.Host, "r.Host, XFH ignored")
	})

	t.Run("peer outside the list", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "203.0.113.7:4321", spoof), []string{"10.0.0.0/8"})
		assert.Equal(t, "203.0.113.7", got.ClientIP)
		assert.Equal(t, "http", got.Scheme)
		assert.Equal(t, "origin.example:8080", got.Host)
	})

	t.Run("in-process TLS decides the scheme when untrusted", func(t *testing.T) {
		r := fwdRequest(t, "203.0.113.7:4321", map[string]string{"X-Forwarded-Proto": "http"})
		r.TLS = &tls.ConnectionState{}
		got := ForwardedHeaders(r, nil)
		assert.Equal(t, "https", got.Scheme, "r.TLS != nil wins; the untrusted downgrade header is ignored")
	})

	t.Run("unparsable RemoteAddr falls back to the raw value", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "@", spoof), []string{"0.0.0.0/0"})
		assert.Equal(t, "@", got.ClientIP, "socket peers keep today's raw fallback and are never trusted")
		assert.Equal(t, "http", got.Scheme)
		assert.Equal(t, "origin.example:8080", got.Host)
	})
}

// TestForwardedHeadersTrustedPeer: from a trusted peer the four headers are
// honoured, with the right-most UNTRUSTED X-Forwarded-For hop as the client.
func TestForwardedHeadersTrustedPeer(t *testing.T) {
	trusted := []string{"10.0.0.0/8", "172.16.0.0/12"}

	t.Run("single XFF hop", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{
			"X-Forwarded-For":   "198.51.100.9",
			"X-Forwarded-Proto": "https",
			"X-Forwarded-Host":  "sso.example.com",
		}), trusted)
		assert.Equal(t, "198.51.100.9", got.ClientIP)
		assert.Equal(t, "https", got.Scheme)
		assert.Equal(t, "sso.example.com", got.Host)
	})

	t.Run("right-most untrusted hop wins, not the left-most", func(t *testing.T) {
		// client → (spoofed prefix) → untrusted hop 203.0.113.50 → trusted 10.0.0.9 → trusted 10.0.0.2 (peer)
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{
			"X-Forwarded-For": "1.1.1.1, 203.0.113.50, 10.0.0.9",
		}), trusted)
		assert.Equal(t, "203.0.113.50", got.ClientIP,
			"a client-supplied left-most entry (1.1.1.1) must not win; walk right-to-left and stop at the first untrusted hop")
	})

	t.Run("whitespace and mixed families in XFF", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "[fd00::2]:5555", map[string]string{
			"X-Forwarded-For": " 2001:db8::5 ,  172.16.1.1 ",
		}), append(trusted, "fd00::/8"))
		assert.Equal(t, "2001:db8::5", got.ClientIP)
	})

	t.Run("every XFF hop trusted → left-most hop", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{
			"X-Forwarded-For": "10.0.0.7, 10.0.0.8",
		}), trusted)
		assert.Equal(t, "10.0.0.7", got.ClientIP,
			"when the chain is entirely trusted there is no untrusted hop; the left-most entry is the best available client address")
	})

	t.Run("X-Real-IP honoured when XFF is absent", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{
			"X-Real-IP": " 198.51.100.77 ",
		}), trusted)
		assert.Equal(t, "198.51.100.77", got.ClientIP)
	})

	t.Run("XFF takes precedence over X-Real-IP", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{
			"X-Forwarded-For": "198.51.100.1",
			"X-Real-IP":       "198.51.100.2",
		}), trusted)
		assert.Equal(t, "198.51.100.1", got.ClientIP)
	})

	t.Run("no forwarding headers from a trusted peer → RemoteAddr / TLS / Host", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", nil), trusted)
		assert.Equal(t, "10.0.0.2", got.ClientIP)
		assert.Equal(t, "http", got.Scheme)
		assert.Equal(t, "origin.example:8080", got.Host)
	})

	t.Run("XFP is normalised and restricted to http/https", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{"X-Forwarded-Proto": "HTTPS"}), trusted)
		assert.Equal(t, "https", got.Scheme, "case-insensitive")

		r := fwdRequest(t, "10.0.0.2:5555", map[string]string{"X-Forwarded-Proto": "javascript"})
		r.TLS = &tls.ConnectionState{}
		got = ForwardedHeaders(r, trusted)
		assert.Equal(t, "https", got.Scheme, "an unknown scheme value is ignored; the listener's own TLS state decides")
	})

	t.Run("XFP downgrade from a trusted peer is honoured", func(t *testing.T) {
		r := fwdRequest(t, "10.0.0.2:5555", map[string]string{"X-Forwarded-Proto": "http"})
		r.TLS = &tls.ConnectionState{}
		got := ForwardedHeaders(r, trusted)
		assert.Equal(t, "http", got.Scheme, "the trusted ingress is authoritative about the client-facing scheme")
	})

	t.Run("blank XFH falls back to r.Host", func(t *testing.T) {
		got := ForwardedHeaders(fwdRequest(t, "10.0.0.2:5555", map[string]string{"X-Forwarded-Host": "   "}), trusted)
		assert.Equal(t, "origin.example:8080", got.Host)
	})
}

// TestTrustedProxiesConfigField pins the wire name, the default and the
// JSON round trip of the edition-neutral top-level key.
func TestTrustedProxiesConfigField(t *testing.T) {
	t.Run("default is empty (trust nobody)", func(t *testing.T) {
		assert.Empty(t, DefaultConfig().TrustedProxies)
		cfg := &Config{}
		require.NoError(t, cfg.Validate())
		assert.Empty(t, cfg.TrustedProxies, "Validate must not materialise a default")
	})

	t.Run("json tag is trusted_proxies and is omitted when empty", func(t *testing.T) {
		raw, err := json.Marshal(&Config{TrustedProxies: []string{"10.0.0.0/8", "::1"}})
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"trusted_proxies":["10.0.0.0/8","::1"]`)

		raw, err = json.Marshal(&Config{})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "trusted_proxies")

		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(`{"trusted_proxies":["192.168.0.0/16"]}`), &cfg))
		assert.Equal(t, []string{"192.168.0.0/16"}, cfg.TrustedProxies)
	})
}

// TestValidateTrustedProxiesAllDoors: FR-039 — the same rule with the same
// message text is reached from Config.Validate() (boot) and
// ValidateDetailed() (PATCH / apply), and from the boot loader itself.
func TestValidateTrustedProxiesAllDoors(t *testing.T) {
	const wantMsg = `trusted_proxies[1] "10.0.0.0/33" is not a valid CIDR or IP address`
	bad := []string{"10.0.0.0/8", "10.0.0.0/33"}

	t.Run("validateTrustedProxies reports each bad entry with the contract text", func(t *testing.T) {
		errs := validateTrustedProxies(&Config{TrustedProxies: []string{"10.0.0.0/8", "10.0.0.0/33", "nope"}})
		require.Len(t, errs, 2)
		assert.True(t, strings.HasPrefix(errs[0].Field, "trusted_proxies"), "field=%q", errs[0].Field)
		assert.Equal(t, wantMsg, errs[0].Message)
		assert.Equal(t, `trusted_proxies[2] "nope" is not a valid CIDR or IP address`, errs[1].Message)
	})

	t.Run("valid entries produce no error", func(t *testing.T) {
		cfg := &Config{TrustedProxies: []string{"10.0.0.0/8", "192.168.1.5", "fd00::/8", "::1", " 172.16.0.0/12 "}}
		assert.Empty(t, validateTrustedProxies(cfg))
		require.NoError(t, cfg.Validate())
		for _, e := range cfg.ValidateDetailed() {
			assert.False(t, strings.HasPrefix(e.Field, "trusted_proxies"), "unexpected %v", e)
		}
	})

	t.Run("Config.Validate (boot door) refuses", func(t *testing.T) {
		cfg := &Config{TrustedProxies: bad}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), wantMsg)
	})

	t.Run("ValidateDetailed (PATCH / apply door) refuses with the same text", func(t *testing.T) {
		cfg := &Config{TrustedProxies: bad}
		errs := cfg.ValidateDetailed()
		var found bool
		for _, e := range errs {
			if strings.HasPrefix(e.Field, "trusted_proxies") {
				found = true
				assert.Equal(t, wantMsg, e.Message)
			}
		}
		assert.True(t, found, "ValidateDetailed must carry the trusted_proxies error: %v", errs)
	})

	t.Run("LoadFromFile refuses a broken file value", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "mcp_config.json")
		raw, err := json.Marshal(map[string]any{
			"listen":          "127.0.0.1:0",
			"data_dir":        tmp,
			"trusted_proxies": bad,
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
		_, err = LoadFromFile(cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), wantMsg)
	})
}

// TestTrustedProxiesEnvOverride: MCPPROXY_TRUSTED_PROXIES (comma list) wins
// over the file value, an unset/empty variable leaves the file value alone,
// entries are trimmed and empty items dropped, and an invalid env value is
// refused by the same validation as the file (the override runs before
// Validate in LoadFromFile).
func TestTrustedProxiesEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	raw, err := json.Marshal(map[string]any{
		"listen":          "127.0.0.1:0",
		"data_dir":        tmp,
		"trusted_proxies": []string{"10.0.0.0/8"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))

	t.Run("file value loads", func(t *testing.T) {
		t.Setenv("MCPPROXY_TRUSTED_PROXIES", "")
		cfg, err := LoadFromFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"10.0.0.0/8"}, cfg.TrustedProxies)
	})

	t.Run("env wins over file, trimmed, empties dropped", func(t *testing.T) {
		t.Setenv("MCPPROXY_TRUSTED_PROXIES", " 172.16.0.0/12, 192.168.1.5 ,, fd00::/8 ,")
		cfg, err := LoadFromFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, []string{"172.16.0.0/12", "192.168.1.5", "fd00::/8"}, cfg.TrustedProxies)
	})

	t.Run("applyTLSEnvOverrides is the seam", func(t *testing.T) {
		t.Setenv("MCPPROXY_TRUSTED_PROXIES", "10.1.0.0/16")
		cfg := &Config{TrustedProxies: []string{"10.0.0.0/8"}}
		applyTLSEnvOverrides(cfg)
		assert.Equal(t, []string{"10.1.0.0/16"}, cfg.TrustedProxies)

		t.Setenv("MCPPROXY_TRUSTED_PROXIES", "")
		cfg = &Config{TrustedProxies: []string{"10.0.0.0/8"}}
		applyTLSEnvOverrides(cfg)
		assert.Equal(t, []string{"10.0.0.0/8"}, cfg.TrustedProxies, "empty variable leaves the file value")
	})

	t.Run("invalid env value is refused at boot", func(t *testing.T) {
		t.Setenv("MCPPROXY_TRUSTED_PROXIES", "10.0.0.0/8,bogus")
		_, err := LoadFromFile(cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `trusted_proxies[1] "bogus" is not a valid CIDR or IP address`)
	})
}
