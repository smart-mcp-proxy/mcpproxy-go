package upstream

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// StartManualOAuth is the asynchronous entry used by Runtime.TriggerOAuthLogin
// and the tray. The undecryptable-row check runs synchronously, so the caller
// gets the coded error instead of a nil return and a log line inside the
// goroutine.
func TestStartManualOAuth_UndecryptableRowReturnsCodedError(t *testing.T) {
	sc := &config.ServerConfig{Name: "sealed-manual", URL: "https://sealed.example.invalid/mcp", Protocol: "http", Enabled: true}
	m, sm, key := undecryptableManager(t, sc)
	before := managerRaw(t, sm, key)

	for _, force := range []bool{false, true} {
		err := m.StartManualOAuth(sc.Name, force)
		require.Error(t, err, "force=%v", force)
		assert.True(t, errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel), "force=%v got %v", force, err)
		assert.Equal(t, diagnostics.Code("MCPX_OAUTH_TOKEN_UNDECRYPTABLE"), diagnostics.Classify(err, diagnostics.ClassifierHints{}))
	}
	assert.Equal(t, before, managerRaw(t, sm, key))
}
