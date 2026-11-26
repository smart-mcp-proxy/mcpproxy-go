package shutdown

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimitsNest(t *testing.T) {
	// background is what ShutdownAll gives its goroutines before it disconnects the servers.
	const background = 3 * time.Second
	// docker is room for the docker commands around one stop, on a slow machine.
	const docker = 5 * time.Second

	require.Greater(t, HTTPServer, CallGrace, "a cancelled handler needs time to return")
	require.GreaterOrEqual(t, Disconnect, StdinClose+ContainerStop+docker, "one stubborn container must not time the phase out")
	require.GreaterOrEqual(t, Upstreams, background+Disconnect+Sweep+SweepKill, "the sweep must be able to kill what it could not stop")
	require.Less(t, HTTPServer+ActionDrain+Upstreams, Core, "the core must finish before it gives up, with time left to close its storage")
	require.Less(t, Core, Tray, "the tray must not kill a core that is still cleaning up")
}
