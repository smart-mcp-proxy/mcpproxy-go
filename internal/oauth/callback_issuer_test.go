package oauth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

// Spec 135 FR-003/FR-005 (plan D2): the issuer decision runs in the callback
// handler, for the flow the state selects, before the page is chosen. A
// refusal reaches the flow as CallbackResult.Err (so nothing is exchanged),
// is recorded as an OAuth failure with the issuer-mismatch code, and the
// browser never sees a success page or the provider's error.
func TestCallback_DecisionBeforePage(t *testing.T) {
	const issuer = "https://as.example.com"
	cases := []struct {
		name       string
		query      string
		advertised bool
		skip       bool
		wantErr    bool
		wantStatus int
	}{
		{"CorrectIss", "code=c&state=STATE&iss=" + url.QueryEscape(issuer), true, false, false, http.StatusOK},
		{"WrongIss", "code=c&state=STATE&iss=https%3A%2F%2Fevil.example", false, false, true, http.StatusBadRequest},
		{"AbsentAdvertised", "code=c&state=STATE", true, false, true, http.StatusBadRequest},
		{"AbsentNotAdvertised", "code=c&state=STATE", false, false, false, http.StatusOK},
		{"EmptyIss", "code=c&state=STATE&iss=", false, false, true, http.StatusBadRequest},
		// FR-005: an error response with a wrong iss is reported as the
		// issuer mismatch, not as the provider's error.
		{"ErrorWithWrongIss", "error=access_denied&state=STATE&iss=https%3A%2F%2Fevil.example", false, false, true, http.StatusBadRequest},
		// FR-014: the opt-out accepts the wrong iss.
		{"WrongIssSkip", "code=c&state=STATE&iss=https%3A%2F%2Fevil.example", true, true, false, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverName := "iss-decision-" + strings.ToLower(tc.name)
			manager := GetGlobalCallbackManager()
			cs, err := manager.StartCallbackServer(serverName, 0)
			require.NoError(t, err)
			t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

			failures := make(chan error, 4)
			tm := GetTokenStoreManager()
			tm.SetOAuthFailureCallback(func(server string, failure error) {
				if server == serverName {
					failures <- failure
				}
			})
			t.Cleanup(func() { tm.SetOAuthFailureCallback(nil) })

			state := "state-" + strings.ToLower(tc.name)
			ch := cs.RegisterState(state)
			require.True(t, cs.ArmIssuerCheck(state, IssuerExpectation{
				Expected: ExpectedIssuer{Issuer: issuer, Source: IssuerSourceDiscovered, IssSupported: tc.advertised},
				Skip:     tc.skip,
			}))

			target := fmt.Sprintf("http://%s:%d%s?", cs.BindHost, cs.Port, cs.Path) + strings.Replace(tc.query, "STATE", state, 1)
			resp, err := http.Get(target) //nolint:gosec // loopback test listener
			require.NoError(t, err)
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			body := string(raw)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)

			got, ok := recvParams(ch, 2*time.Second)
			require.True(t, ok, "the flow always learns the outcome")
			if !tc.wantErr {
				assert.NoError(t, got.Err)
				return
			}
			var mm *IssuerMismatchError
			require.True(t, errors.As(got.Err, &mm), "flow gets the typed refusal, got %v", got.Err)
			assert.NotContains(t, body, callbackTitleSuccess)
			assert.NotContains(t, body, "access_denied", "the provider error is not reported before the issuer passes")
			assert.NotContains(t, body, "evil", "the received iss is never rendered")
			assert.Contains(t, body, "expected authorization server")
			assert.Equal(t, diagnostics.OAuthIssuerMismatch, diagnostics.Classify(got.Err, diagnostics.ClassifierHints{}))
			// The owning flow records the refusal, once; the handler does not.
			select {
			case f := <-failures:
				t.Fatalf("handler recorded the refusal itself: %v", f)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

// RegisterArmedState creates the waiter and its check in one step.
func TestCallback_RegisterArmedState(t *testing.T) {
	serverName := "iss-register-armed"
	manager := GetGlobalCallbackManager()
	cs, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })
	e := IssuerExpectation{Expected: ExpectedIssuer{Issuer: "https://a.example", Source: IssuerSourceDiscovered}}
	ch := cs.RegisterArmedState("s1", e)
	require.True(t, cs.HasState("s1"))
	got, exp, ok := cs.take("s1")
	require.True(t, ok)
	assert.Equal(t, e, exp)
	assert.Equal(t, (<-chan CallbackResult)(got), ch)
}

// Arming needs a registered waiter: an expectation for an unknown state is
// not stored, so it cannot later attach to a flow that registers it.
func TestCallback_ArmUnknownStateRefused(t *testing.T) {
	serverName := "iss-arm-unknown"
	manager := GetGlobalCallbackManager()
	cs, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })
	assert.False(t, cs.ArmIssuerCheck("nope", IssuerExpectation{Expected: ExpectedIssuer{Issuer: "https://a", Source: IssuerSourceDiscovered}}))
}
