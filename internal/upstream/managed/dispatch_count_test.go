package managed

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// Telemetry counts upstream attempts from the call recorder's Dispatches: the
// number of times core handed a tools/call to the transport for one logical
// call. These cells pin the situations a final-error heuristic gets wrong.

func dispatchesOf(rec *proxytransport.CallRecorder) int { return rec.Snapshot().Dispatches }

func TestDispatches_OneRequestOneCount(t *testing.T) {
	mc, _ := newSessionClient(t)
	ctx, rec := proxytransport.WithCallRecorder(tctx(t))
	_, err := mc.CallTool(ctx, "read_thing", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, dispatchesOf(rec))
}

// Session recovery repeats the request on the re-initialized session: two
// requests left for the transport, so two attempts.
func TestDispatches_SessionRecoveryCountsBothRequests(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()
	ctx, rec := proxytransport.WithCallRecorder(tctx(t))
	_, err := mc.CallTool(ctx, "read_thing", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), mc.SessionReinitCount())
	assert.Equal(t, 2, dispatchesOf(rec))
}

// A recovery that is then refused (tool hash changed) still sent the first
// request, even though the final error is ErrConnectionGenerationChanged.
func TestDispatches_RefusedAfterFirstRequestStillCountsIt(t *testing.T) {
	mc, up := newSessionClient(t)
	up.mu.Lock()
	up.readDesc = "reads, but now differently"
	up.mu.Unlock()
	up.forget()

	ctx, rec := proxytransport.WithCallRecorder(tctx(t))
	_, err := mc.CallToolOnEpoch(ctx, "read_thing", nil, mc.ConnectionEpoch())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnectionGenerationChanged), "got %v", err)
	assert.Equal(t, 1, dispatchesOf(rec))
}

// A pinned call on a moved generation never reaches the transport.
func TestDispatches_RefusedBeforeTransportCountsNothing(t *testing.T) {
	mc, _ := newSessionClient(t)
	ctx, rec := proxytransport.WithCallRecorder(tctx(t))
	_, err := mc.CallToolOnEpoch(ctx, "read_thing", nil, mc.ConnectionEpoch()+1000)
	require.Error(t, err)
	assert.Equal(t, 0, dispatchesOf(rec))
}

// The check-then-call window: the managed layer passed its own checks, then
// the connection dropped before core's hand-off. Core refuses "client not
// connected" before the transport, so nothing is counted.
func TestDispatches_CoreRefusalAfterManagedChecksCountsNothing(t *testing.T) {
	mc, _ := newSessionClient(t)
	require.NoError(t, mc.Disconnect())
	ctx, rec := proxytransport.WithCallRecorder(tctx(t))
	_, err := mc.coreClient.CallTool(ctx, "read_thing", nil)
	require.Error(t, err)
	assert.Equal(t, 0, dispatchesOf(rec))
}

func TestDispatches_AbsentRecorderIsSafe(t *testing.T) {
	var nilRec *proxytransport.CallRecorder
	assert.Equal(t, 0, dispatchesOf(nilRec))
	mc, _ := newSessionClient(t)
	_, err := mc.CallTool(tctx(t), "read_thing", nil) // no recorder in the context
	require.NoError(t, err)
}
