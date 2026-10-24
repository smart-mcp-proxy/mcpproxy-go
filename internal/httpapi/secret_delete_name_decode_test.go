package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// recordingKeyring is a keyring provider that records deleted names.
type recordingKeyring struct{ deleted []string }

func (p *recordingKeyring) CanResolve(t string) bool { return t == secretTypeKeyring }
func (p *recordingKeyring) Resolve(context.Context, secret.Ref) (string, error) {
	return "", nil
}
func (p *recordingKeyring) Store(context.Context, secret.Ref, string) error { return nil }
func (p *recordingKeyring) Delete(_ context.Context, ref secret.Ref) error {
	p.deleted = append(p.deleted, ref.Name)
	return nil
}
func (p *recordingKeyring) List(context.Context) ([]secret.Ref, error) { return nil, nil }
func (p *recordingKeyring) IsAvailable() bool                          { return true }

// Secret names may contain any character but '}' (${keyring:openai:prod} is a
// valid reference). The Web UI encodes the name with encodeURIComponent and chi
// routes on RawPath, so {name} arrives encoded and must be decoded before the
// keyring delete, otherwise the wrong (nonexistent) entry is targeted.
func TestDeleteSecret_DecodesNameParam(t *testing.T) {
	kr := &recordingKeyring{}
	res := secret.NewResolver()
	res.RegisterProvider(secretTypeKeyring, kr)
	ctrl := &secretResolverController{MockServerController: &MockServerController{}, resolver: res}
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/secrets/openai%3Aprod?type=keyring", http.NoBody)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, []string{"openai:prod"}, kr.deleted)
}
