package contracts

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 112: forward_headers reaches the API Server type from the config, and
// from both shapes of generic server map (typed slice, JSON-decoded slice).
func TestForwardHeadersProjection(t *testing.T) {
	cfg := &config.ServerConfig{Name: "s", Protocol: "streamable-http", ForwardHeaders: []string{"X-A", "X-B"}}
	srv := ConvertServerConfig(cfg, nil, "ready", true, 1, false)
	assert.Equal(t, []string{"X-A", "X-B"}, srv.ForwardHeaders)
	cfg.ForwardHeaders[0] = "X-Z"
	assert.Equal(t, []string{"X-A", "X-B"}, srv.ForwardHeaders, "projection must not alias the config slice")

	assert.Empty(t, ConvertServerConfig(&config.ServerConfig{Name: "n"}, nil, "ready", true, 0, false).ForwardHeaders)

	typed := ConvertGenericServersToTyped([]map[string]interface{}{{"name": "s", "forward_headers": []string{"X-A"}}})
	require.Len(t, typed, 1)
	assert.Equal(t, []string{"X-A"}, typed[0].ForwardHeaders)

	decoded := ConvertGenericServersToTyped([]map[string]interface{}{{"name": "s", "forward_headers": []interface{}{"X-A", 7, "X-B"}}})
	assert.Equal(t, []string{"X-A", "X-B"}, decoded[0].ForwardHeaders)

	none := ConvertGenericServersToTyped([]map[string]interface{}{{"name": "s"}})
	assert.Nil(t, none[0].ForwardHeaders)

	raw, err := json.Marshal(none[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "forward_headers", "omitted when empty")
}
