package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// The servers.changed coalescer and the attention subscriber start in New and
// read the management service from their own goroutines; the server installs
// it later, on the startup goroutine, and a test (or a config update that
// publishes before the wiring step) can have a publish in flight at that
// moment. CI caught the plain-field version as a data race on Windows
// (TestArgumentErrorModeRESTManagement). This pins the atomic swap: under
// -race it fails with the old field and passes with atomic.Pointer.
func TestManagementService_SwapRacesWithCoalescerRead(t *testing.T) {
	rt := newPayloadTestRuntime(t, nil)
	lister := &fakeServersLister{
		servers: []*contracts.Server{{Name: "s1"}},
		stats:   &contracts.ServerStats{},
	}

	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if i%2 == 0 {
				rt.SetManagementService(lister)
			} else {
				rt.SetManagementService(nil)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			evt := rt.buildServersChangedPayload(context.Background(), "test", nil)
			assert.Equal(t, EventTypeServersChanged, evt.Type)
		}
	}()
	wg.Wait()

	// Last swap in the loop above installs nil: the getter reports no service
	// and the payload falls back to notify-only rather than panicking on a
	// typed-nil assertion.
	require.Nil(t, rt.GetManagementService())
	evt := rt.buildServersChangedPayload(context.Background(), "after", nil)
	_, hasServers := evt.Payload["servers"]
	require.False(t, hasServers)

	rt.SetManagementService(lister)
	require.NotNil(t, rt.GetManagementService())
	evt = rt.buildServersChangedPayload(context.Background(), "after", nil)
	_, hasServers = evt.Payload["servers"]
	require.True(t, hasServers)
}
