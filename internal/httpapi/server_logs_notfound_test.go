package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

type logsErrController struct {
	*MockServerController
	err error
}

func (c *logsErrController) GetServerLogs(_ string, _ int) ([]contracts.LogEntry, error) {
	return nil, c.err
}

// #1466: an unknown server is a 404 (as the swagger comment promises), any
// other failure stays a 500.
func TestGetServerLogs_NotFoundIs404(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", fmt.Errorf("%w: ghost", contracts.ErrServerNotFound), http.StatusNotFound},
		{"other error", fmt.Errorf("failed to read log"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &logsErrController{MockServerController: &MockServerController{}, err: tc.err}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest("GET", "/api/v1/servers/ghost/logs", http.NoBody)
			req.Header.Set("X-API-Key", mockControllerAPIKey)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}
