package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// releaseProcessGroup is the platform hook DisconnectWithContext calls on
// every non-Docker stdio disconnect (after the graceful MCP close), so it
// must be cheap and safe for the "nothing to release" cases on every OS:
// the zero/negative sentinels and a PID that never had a Job registered.
func TestReleaseProcessGroup_NothingToRelease(t *testing.T) {
	logger := zap.NewNop()
	for _, pgid := range []int{0, -1, 999999999} {
		start := time.Now()
		releaseProcessGroup(pgid, nil, logger, "test-server")
		assert.Less(t, time.Since(start), 100*time.Millisecond,
			"releaseProcessGroup(%d) must return immediately when there is nothing to release", pgid)
	}
}
