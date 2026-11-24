//go:build !server

package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A stopped run leaves a watcher behind that reacts to its cancelled context.
// When it wakes after the next StartServer it must leave the new run alone.
func TestStartServer_WatcherOfAStoppedRunLeavesTheNextRunAlone(t *testing.T) {
	env := NewTestEnvironment(t)
	t.Cleanup(env.Cleanup)
	srv := env.proxyServer

	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	srv.testHookRunWatcher = func() {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-release
	}

	require.NoError(t, srv.StopServer())
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher of the stopped run never woke")
	}

	require.NoError(t, srv.StartServer(context.Background()))
	env.waitForServerReady()

	close(release) // the old watcher continues only now, with the new run up
	time.Sleep(time.Second)
	require.True(t, srv.IsRunning(), "the watcher of the previous run stopped the new run")
	require.True(t, env.testServerConnection())
}
