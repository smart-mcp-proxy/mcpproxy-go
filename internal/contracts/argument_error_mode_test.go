package contracts

import (
	"encoding/json"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
)

// ADMIN-MODE-01: both conversion doors must carry the setting onto the REST
// wire, including a generic map that crossed a JSON boundary.
func TestArgumentErrorModeServerConversions(t *testing.T) {
	for _, mode := range []string{"", "detailed", "redacted"} {
		t.Run(mode, func(t *testing.T) {
			direct := ConvertServerConfig(&config.ServerConfig{Name: "upstream", ToolArgumentErrorMode: mode}, nil, "disabled", false, 0, false)
			require.Equal(t, mode, direct.ToolArgumentErrorMode)
			encoded, err := json.Marshal([]*Server{direct})
			require.NoError(t, err)
			var maps []map[string]interface{}
			require.NoError(t, json.Unmarshal(encoded, &maps))
			if mode == "" {
				require.NotContains(t, maps[0], "tool_argument_error_mode")
			} else {
				require.Equal(t, mode, maps[0]["tool_argument_error_mode"])
			}
			converted := ConvertGenericServersToTyped(maps)
			require.Len(t, converted, 1)
			require.Equal(t, mode, converted[0].ToolArgumentErrorMode)
		})
	}
}
