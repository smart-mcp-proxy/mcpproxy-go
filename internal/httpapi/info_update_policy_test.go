package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// policyController serves a fixed policy so the handler wiring can be asserted
// without standing up a real update checker.
type policyController struct {
	MockServerController
	policy updatecheck.Policy
}

func (c *policyController) UpdatePolicy() updatecheck.Policy { return c.policy }

// Spec 092 FR-015: the effective update policy must be an EXPLICIT contract.
// Absence of the "update" object cannot carry the information, because it is
// absent both when checking is disabled and when no check has run yet.
func TestInfoEndpointAlwaysReportsUpdatePolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy updatecheck.Policy
	}{
		{
			name:   "enabled stable",
			policy: updatecheck.Policy{Enabled: true, Channel: updatecheck.PolicyChannelStable},
		},
		{
			name:   "rc channel",
			policy: updatecheck.Policy{Enabled: true, Channel: updatecheck.PolicyChannelRC},
		},
		{
			// The interesting case: every field is the zero value, so an
			// omitempty encoding would erase the whole object and the tray
			// would read "no policy" as "no opinion" and check anyway.
			name:   "kill switch on",
			policy: updatecheck.Policy{Enabled: false, Channel: updatecheck.PolicyChannelStable},
		},
		{
			name:   "nudges suppressed in CI",
			policy: updatecheck.Policy{Enabled: true, Channel: updatecheck.PolicyChannelStable, NudgesSuppressed: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t).Sugar()
			server := NewServer(&policyController{policy: tt.policy}, logger, nil)

			req := httptest.NewRequest("GET", "/api/v1/info", http.NoBody)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			var response contracts.APIResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			data, ok := response.Data.(map[string]interface{})
			require.True(t, ok)

			require.Contains(t, data, "update_policy", "update_policy must always be present")
			policy, ok := data["update_policy"].(map[string]interface{})
			require.True(t, ok, "update_policy must be an object")

			// All three keys present regardless of value — no omitempty.
			require.Contains(t, policy, "enabled")
			require.Contains(t, policy, "channel")
			require.Contains(t, policy, "nudges_suppressed")

			assert.Equal(t, tt.policy.Enabled, policy["enabled"])
			assert.Equal(t, tt.policy.Channel, policy["channel"])
			assert.Equal(t, tt.policy.NudgesSuppressed, policy["nudges_suppressed"])
		})
	}
}
