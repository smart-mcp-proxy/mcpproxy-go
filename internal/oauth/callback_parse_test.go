package oauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Spec 135 FR-001: one strict parse of the authorization response query.
func TestParseCallbackQuery_Strict(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		reject bool
	}{
		{"code and state", "code=abc&state=s1", false},
		{"code state iss", "code=abc&state=s1&iss=https%3A%2F%2Fas.example", false},
		{"provider error", "error=access_denied&state=s1", false},
		{"provider error with description", "error=access_denied&error_description=no&state=s1", false},
		{"empty iss reaches the issuer decision", "code=abc&state=s1&iss=", false},
		{"unknown extra params kept", "code=abc&state=s1&session_state=x", false},

		{"repeated code", "code=a&code=b&state=s1", true},
		{"repeated state", "code=a&state=s1&state=s2", true},
		{"repeated iss", "code=a&state=s1&iss=x&iss=x", true},
		{"repeated error", "error=a&error=b&state=s1", true},
		{"code and error", "code=a&error=b&state=s1", true},
		{"neither code nor error", "state=s1", true},
		{"empty code", "code=&state=s1", true},
		{"empty error", "error=&state=s1", true},
		{"empty code with error", "code=&error=x&state=s1", true},
		{"missing state", "code=abc", true},
		{"empty state", "code=abc&state=", true},
		{"malformed escape", "code=%zz&state=s1", true},
		{"semicolon separator", "code=a;state=s1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := ParseCallbackQuery(tc.query)
			if tc.reject {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrInvalidCallback), "rejections wrap ErrInvalidCallback")
				assert.Nil(t, q)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, q)
			assert.Equal(t, "s1", q.Get("state"))
		})
	}
}

// The multi-valued query reaches the consumer intact: an empty iss is still
// present (len 1), so the later issuer decision can tell it from an absent one.
func TestParseCallbackQuery_KeepsIssPresence(t *testing.T) {
	q, err := ParseCallbackQuery("code=abc&state=s1&iss=")
	require.NoError(t, err)
	assert.Len(t, q["iss"], 1)

	q, err = ParseCallbackQuery("code=abc&state=s1")
	require.NoError(t, err)
	_, present := q["iss"]
	assert.False(t, present)
}

// FR-001 on the loopback listener: a rejected callback renders a failure
// page, does not take the waiter, and the provider's valid callback for the
// same state is still delivered with its full query.
func TestLoopbackCallback_StrictParse_WaiterKept(t *testing.T) {
	m := &CallbackServerManager{servers: make(map[string]*CallbackServer), logger: zap.NewNop()}
	c, err := m.StartCallbackServerOnHost("strict-loopback", CallbackBinding{Logger: zap.NewNop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.StopCallbackServer("strict-loopback") })
	ch := c.RegisterState("s1")

	for _, bad := range []string{"code=a&code=b&state=s1", "code=a&state=s1&iss=x&iss=y", "code=a&error=e&state=s1", "code=&state=s1", "state=s1"} {
		w := httptest.NewRecorder()
		c.handleCallback(w, httptest.NewRequest(http.MethodGet, c.Path+"?"+bad, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, bad)
		assert.Contains(t, w.Body.String(), "Authorization Failed", bad)
		assert.NotContains(t, w.Body.String(), "Authorization Successful", bad)
		assert.True(t, c.HasState("s1"), "%s must not use up the waiter", bad)
	}

	w := httptest.NewRecorder()
	c.handleCallback(w, httptest.NewRequest(http.MethodGet, c.Path+"?code=good&state=s1&iss=https%3A%2F%2Fas.example&extra=1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	got := <-ch
	assert.Equal(t, "good", got.Get("code"))
	assert.Equal(t, []string{"https://as.example"}, got.Query["iss"])
	assert.Equal(t, "1", got.Get("extra"))
}
