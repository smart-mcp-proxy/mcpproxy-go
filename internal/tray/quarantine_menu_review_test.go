//go:build !nogui && !headless && !linux

package tray

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type reviewWebUIRecorder struct {
	paths []string
}

func (r *reviewWebUIRecorder) OpenWebUI() error { return nil }
func (r *reviewWebUIRecorder) OpenWebUIPath(path string) error {
	r.paths = append(r.paths, path)
	return nil
}

// T078a / X12: a quarantine-menu click is represented by the review action.
// It opens the interim review location and never reaches a core unquarantine
// method (ServerInterface no longer exposes one).
func TestQuarantineMenuReviewActionOpensEscapedReviewPath(t *testing.T) {
	web := &reviewWebUIRecorder{}
	server := NewMockServer()
	server.AddServer("team / filesystem", "https://example.test/mcp", true, true)
	app := &App{server: server, apiClient: web, logger: zap.NewNop().Sugar()}

	app.handleServerAction("team / filesystem", "review")

	require.Equal(t, []string{"/review/team%20%2F%20filesystem"}, web.paths)
	quarantined, err := server.GetQuarantinedServers()
	require.NoError(t, err)
	require.Len(t, quarantined, 1, "review must not unquarantine the server")
	require.Equal(t, "team / filesystem", quarantined[0]["name"])
}
