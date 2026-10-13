package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// redactedRegistrySummary dereferences its argument, and all three registry
// handlers called it on the SUCCESS path without checking the entry for nil.
// A controller that reports success with no entry therefore panicked inside the
// handler; chi's recoverer turned that into a bare 500 with an empty body, so
// the failure looked like an unrelated server error rather than a nil deref.
//
// This is not hypothetical plumbing: MockServerController already returns
// (nil, nil, nil) from these methods, so every caller exercising these routes
// through it hit the panic — intermittently visible as a Windows CI failure
// where the recovered panic escaped the test process.
func TestRegistryHandlers_NilEntryOnSuccessDoesNotPanic(t *testing.T) {
	const adminKey = "admin-secret"
	body := `{"name":"n","url":"https://example.com","servers_url":"https://example.com/s"}`

	for _, rt := range []struct {
		name, method, path, body string
	}{
		{"add", http.MethodPost, "/api/v1/registries", body},
		{"edit", http.MethodPut, "/api/v1/registries/reg1", body},
		{"remove", http.MethodDelete, "/api/v1/registries/reg1", ""},
	} {
		t.Run(rt.name, func(t *testing.T) {
			ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: adminKey}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			req.Header.Set("X-API-Key", adminKey)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			require.NotPanics(t, func() { srv.ServeHTTP(w, req) },
				"a nil entry on the success path must not deref")

			// A recovered panic renders as a 500 with an EMPTY body. Whatever
			// this route decides to answer, it has to actually answer.
			assert.NotEmpty(t, w.Body.String(),
				"an empty 500 body is the signature of a recovered panic")
		})
	}
}

// The renderer itself must tolerate nil rather than relying on every caller to
// remember — it is called from three places and dereferences seven fields.
func TestRedactedRegistrySummary_NilEntryIsZeroValue(t *testing.T) {
	require.NotPanics(t, func() {
		got := redactedRegistrySummary(nil)
		assert.Empty(t, got.ID, "a nil entry has no identity to report")
		assert.Empty(t, got.URL)
		assert.False(t, got.Trusted, "nothing unknown may be reported as trusted")
	})
}
