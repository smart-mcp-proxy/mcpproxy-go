package oauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GH #1722: when the instance that owns a pending login is retired (restarted,
// replaced, removed or quarantined) the waiting flow invalidates its state. The
// browser callback that arrives afterwards must then say the login was
// invalidated, not "Authorization Successful" followed by a silent exchange
// failure in the log.
func TestCallback_InvalidatedStateRendersFailureWithReason(t *testing.T) {
	const state = "STATEinvalidated1234567890"
	cs, _ := newObservedCallbackServer(t, state)

	cs.InvalidateState(state, "the server was restarted while the sign-in was in progress")
	assert.False(t, cs.HasState(state), "an invalidated state has no waiter")

	req := httptest.NewRequest(http.MethodGet, DefaultRedirectPath+"?code=abc&state="+state, nil)
	rec := httptest.NewRecorder()
	cs.handleCallbackWithFailureNotification(rec, req, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, callbackTitleFailed)
	assert.NotContains(t, body, callbackTitleSuccess)
	assert.Contains(t, body, "the server was restarted while the sign-in was in progress")
	assert.Contains(t, body, "alpha", "the page names the server to log in to again")
}

func TestCallback_InvalidateUnknownStateIsNoop(t *testing.T) {
	cs, _ := newObservedCallbackServer(t, "")
	cs.InvalidateState("never-registered", "reason")

	req := httptest.NewRequest(http.MethodGet, DefaultRedirectPath+"?code=abc&state=never-registered", nil)
	rec := httptest.NewRecorder()
	cs.handleCallbackWithFailureNotification(rec, req, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unknown or expired state",
		"a state that was never waited on keeps the generic message")
}
