package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// set_profile must report the servers the session can actually reach after the
// switch. For a SWITCHABLE client credential the bound profile is only the base
// (profile.SourceBinding): the session selection outranks it, so the reported
// list is the selected profile's reach, not the binding's.
func TestSetProfileV3SwitchableClientReportsSelectedProfileServers(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)

	setProfile := func(t *testing.T, sid, bindingMode, slug string) (active string, servers []string) {
		t.Helper()
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", bindingMode), sid)
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": slug}
		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
		var payload struct {
			Active  string   `json:"active_profile"`
			Servers []string `json:"servers"`
		}
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
		return payload.Active, payload.Servers
	}

	t.Run("switching to a wider profile reports the wider reach", func(t *testing.T) {
		active, servers := setProfile(t, "sw-wide", "switchable", "work-full")
		require.Equal(t, "work-full", active)
		require.ElementsMatch(t, []string{"github", "notion", "filesystem"}, servers)
	})

	t.Run("re-selecting the bound profile reports the binding's reach", func(t *testing.T) {
		active, servers := setProfile(t, "sw-base", "switchable", "work-readonly")
		require.Equal(t, "work-readonly", active)
		require.ElementsMatch(t, []string{"github", "notion"}, servers)
	})

	t.Run("clearing the selection falls back to the binding", func(t *testing.T) {
		active, servers := setProfile(t, "sw-clear", "switchable", "")
		require.Equal(t, "", active)
		require.ElementsMatch(t, []string{"github", "notion"}, servers)
	})
}
