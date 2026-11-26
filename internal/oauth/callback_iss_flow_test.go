package oauth

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// Spec 135 PR-1 (FR-001, groundwork for FR-002–FR-005): against the in-repo
// OAuth test server, the authorization response reaches the waiting flow
// with iss exactly as the server sent it (present, absent or empty), so the
// issuer decision PR-2 adds sees the real query; a repeated iss is refused
// before delivery and the code is never handed to the flow.
func TestCallbackFlow_IssReachesFlowIntact(t *testing.T) {
	cases := []struct {
		name      string
		mode      oauthserver.IssMode
		advertise bool
		wantIss   []string // nil: absent; "<issuer>" is replaced by the server's issuer
		rejected  bool
	}{
		{"CorrectAdvertised", oauthserver.IssCorrect, true, []string{"<issuer>"}, false},
		{"WrongNotAdvertised", oauthserver.IssWrong, false, []string{"https://wrong-issuer.example"}, false},
		{"AbsentAdvertised", oauthserver.IssOmit, true, nil, false},
		{"AbsentNotAdvertised", oauthserver.IssOmit, false, nil, false},
		{"Empty", oauthserver.IssEmpty, true, []string{""}, false},
		{"Repeated", oauthserver.IssRepeat, true, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := GetGlobalCallbackManager()
			serverName := "iss-flow-" + strings.ToLower(tc.name)
			cs, err := manager.StartCallbackServer(serverName, 0)
			require.NoError(t, err)
			t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

			clientID := "iss-flow-client"
			provider := oauthserver.Start(t, oauthserver.Options{
				IssMode: tc.mode, AdvertiseIss: tc.advertise,
				Clients: []oauthserver.ClientConfig{{
					ClientID: clientID, RedirectURIs: []string{cs.RedirectURI},
					GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}, Scopes: []string{"read"},
				}},
			})
			t.Cleanup(func() { _ = provider.Shutdown() })

			// The metadata flag is what PR-2's decision reads for an absent iss.
			resp, err := http.Get(provider.IssuerURL + "/.well-known/oauth-authorization-server") //nolint:gosec // test server
			require.NoError(t, err)
			var md map[string]any
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&md))
			_ = resp.Body.Close()
			flag, _ := md["authorization_response_iss_parameter_supported"].(bool)
			assert.Equal(t, tc.advertise, flag)

			state := "state-" + strings.ToLower(tc.name)
			ch := cs.RegisterState(state)
			_, challenge := pkcePair(strings.Repeat("v", 43))
			status, body := approveAuthorization(t, provider, clientID, cs.RedirectURI, state, challenge)

			if tc.rejected {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.NotContains(t, body, "Authorization Successful")
				assert.True(t, cs.HasState(state), "a refused response does not use up the flow")
				_, delivered := recvParams(ch, 200*time.Millisecond)
				assert.False(t, delivered, "the code never reaches the flow")
				return
			}
			assert.Equal(t, http.StatusOK, status)
			got, ok := recvParams(ch, 5*time.Second)
			require.True(t, ok, "callback delivered")
			assert.NotEmpty(t, got.Get("code"))
			want := tc.wantIss
			for i, v := range want {
				if v == "<issuer>" {
					want = append([]string(nil), want...)
					want[i] = provider.IssuerURL
				}
			}
			if want == nil {
				_, present := got.Query["iss"]
				assert.False(t, present, "an absent iss stays absent")
			} else {
				assert.Equal(t, want, got.Query["iss"])
			}
		})
	}
}
