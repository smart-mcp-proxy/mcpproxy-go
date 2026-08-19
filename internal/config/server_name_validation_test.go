package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateDetailed_ServerNameColon covers Finding F6: a server name
// containing ':' breaks the "server:prompt" / "server:tool" routing split, so
// it must be rejected at config load rather than silently misrouting.
func TestValidateDetailed_ServerNameColon(t *testing.T) {
	newCfg := func(name string) *Config {
		cfg := DefaultConfig()
		cfg.Servers = []*ServerConfig{{
			Name:     name,
			URL:      "https://example.com/mcp",
			Protocol: "streamable-http",
		}}
		return cfg
	}

	t.Run("colon in name is rejected", func(t *testing.T) {
		errs := newCfg("team:gh").ValidateDetailed()
		var found *ValidationError
		for i := range errs {
			if strings.HasSuffix(errs[i].Field, ".name") {
				found = &errs[i]
				break
			}
		}
		require.NotNil(t, found, "a ':' server name must produce a validation error, got %+v", errs)
		assert.Contains(t, found.Message, "team:gh")
		assert.Contains(t, found.Message, "':'")
	})

	// '__' is intentionally NOT hard-rejected (back-compat: it works in
	// retrieve_tools mode and any residual display collision is logged and
	// handled deterministically at aggregation time).
	for _, name := range []string{"github", "db_server", "my__server", "server-a"} {
		t.Run("valid "+name, func(t *testing.T) {
			for _, e := range newCfg(name).ValidateDetailed() {
				assert.NotContains(t, e.Field, ".name", "valid server name %q must not error: %+v", name, e)
			}
		})
	}
}
