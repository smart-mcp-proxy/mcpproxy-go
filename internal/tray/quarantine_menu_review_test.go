//go:build !nogui && !headless && !linux

package tray

import (
	"testing"
	"time"

	"fyne.io/systray"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type reviewWebUIRecorder struct {
	paths chan string
}

func (r *reviewWebUIRecorder) OpenWebUI() error { return nil }
func (r *reviewWebUIRecorder) OpenWebUIPath(path string) error {
	r.paths <- path
	return nil
}

// T078a / X12: a quarantine-menu click is represented by the review action.
// It opens the interim review location and never reaches a core unquarantine
// method (ServerInterface no longer exposes one).
func TestQuarantineMenuReviewActionOpensEscapedReviewPath(t *testing.T) {
	web := &reviewWebUIRecorder{paths: make(chan string, 1)}
	server := NewMockServer()
	server.AddServer("team / filesystem", "https://example.test/mcp", true, true)
	app := &App{server: server, apiClient: web, logger: zap.NewNop().Sugar()}
	item := &systray.MenuItem{ClickedCh: make(chan struct{})}
	menus := NewMenuManager(nil, nil, nil, zap.NewNop().Sugar())

	menus.wireQuarantineMenuItemReviewClick("team / filesystem", item)
	menus.SetActionCallback(app.handleServerAction)
	item.ClickedCh <- struct{}{}

	select {
	case path := <-web.paths:
		require.Equal(t, "/review/team%20%2F%20filesystem", path)
	case <-time.After(time.Second):
		t.Fatal("quarantine menu click did not open the review page")
	}
	quarantined, err := server.GetQuarantinedServers()
	require.NoError(t, err)
	require.Len(t, quarantined, 1, "review must not unquarantine the server")
	require.Equal(t, "team / filesystem", quarantined[0]["name"])
}
