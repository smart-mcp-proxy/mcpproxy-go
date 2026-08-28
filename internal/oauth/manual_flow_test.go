package oauth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Issue #975: while the user is completing a manual sign-in, a background
// reconnect must not start a competing OAuth flow for the same server.

func TestManualFlow_ActiveUntilReleased(t *testing.T) {
	server := "manual-flow-release"

	assert.False(t, IsManualFlowActive(server), "no manual flow should be active initially")

	release := BeginManualFlow(server, time.Minute)
	assert.True(t, IsManualFlowActive(server), "manual flow must be active after Begin")

	release()
	assert.False(t, IsManualFlowActive(server), "manual flow must be cleared after release")

	// Releasing twice is safe.
	release()
	assert.False(t, IsManualFlowActive(server))
}

func TestManualFlow_ExpiresWithoutRelease(t *testing.T) {
	server := "manual-flow-expiry"

	release := BeginManualFlow(server, 50*time.Millisecond)
	t.Cleanup(release)
	assert.True(t, IsManualFlowActive(server))

	assert.Eventually(t, func() bool { return !IsManualFlowActive(server) },
		2*time.Second, 10*time.Millisecond,
		"a manual flow whose release is missed must expire instead of suppressing reconnects forever")
}

func TestManualFlow_IsPerServer(t *testing.T) {
	release := BeginManualFlow("manual-flow-server-a", time.Minute)
	t.Cleanup(release)

	assert.True(t, IsManualFlowActive("manual-flow-server-a"))
	assert.False(t, IsManualFlowActive("manual-flow-server-b"),
		"a manual sign-in must only suppress reconnects for its own server")
}

func TestManualFlow_LaterReleaseDoesNotClearNewerFlow(t *testing.T) {
	server := "manual-flow-restart"

	firstRelease := BeginManualFlow(server, time.Minute)
	secondRelease := BeginManualFlow(server, time.Minute)

	// The user restarted the login; releasing the first (superseded) flow must
	// not unsuppress reconnects while the second is still running.
	firstRelease()
	assert.True(t, IsManualFlowActive(server), "the newer manual flow must stay active")

	secondRelease()
	assert.False(t, IsManualFlowActive(server))
}
