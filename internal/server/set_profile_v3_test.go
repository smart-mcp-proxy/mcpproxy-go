package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestSetProfileV3ReportsOnlyTheSessionSelection(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	ctx := sessionProfileCtx(t, proxy, "session-profile-source", "work-full")
	for _, tc := range []struct {
		name, selection, wantSource string
	}{
		{name: "selected profile", selection: "work-full", wantSource: "session"},
		{name: "cleared selection", selection: "", wantSource: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{"profile": tc.selection}
			result, err := proxy.handleSetProfile(ctx, request)
			require.NoError(t, err)
			require.False(t, result.IsError, resultText(t, result))
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
			require.Equal(t, tc.wantSource, payload["profile_source"])
			require.Equal(t, tc.selection, payload["active_profile"])
		})
	}
}
