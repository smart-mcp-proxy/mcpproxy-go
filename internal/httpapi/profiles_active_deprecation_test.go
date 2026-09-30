package httpapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-039: GET and PUT /api/v1/profiles/active are deprecated in favour of
// GET /api/v1/profiles. The header is the only change: the body and the
// active_profile.changed emission are exactly what they were.
func TestProfilesActive_DeprecationHeader(t *testing.T) {
	srv := newProfilesTestServer()

	w, _ := doJSON(t, srv, http.MethodGet, "/api/v1/profiles/active", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "true", w.Header().Get("Deprecation"))
	assert.Equal(t, `</api/v1/profiles>; rel="successor-version"`, w.Header().Get("Link"))

	w, _ = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"research"}`))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "true", w.Header().Get("Deprecation"))
	assert.Equal(t, `</api/v1/profiles>; rel="successor-version"`, w.Header().Get("Link"))

	// The listing itself is not deprecated.
	w, _ = doJSON(t, srv, http.MethodGet, "/api/v1/profiles", nil)
	assert.Empty(t, w.Header().Get("Deprecation"))
}

func TestProfilesActive_BehaviourUnchanged(t *testing.T) {
	srv := newProfilesTestServer()

	w, resp := doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"research"}`))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, map[string]interface{}{"active_profile": "research"}, resp["data"])

	_, resp = doJSON(t, srv, http.MethodGet, "/api/v1/profiles/active", nil)
	assert.Equal(t, map[string]interface{}{"active_profile": "research"}, resp["data"])

	w, _ = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"nope"}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
