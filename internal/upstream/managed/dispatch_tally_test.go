package managed

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tally is telemetry's evidence of how many tools/call requests one
// logical call actually handed to the transport. These cells pin the three
// situations a final-error heuristic gets wrong.

func TestDispatchTally_OneRequestOneCount(t *testing.T) {
	mc, _ := newSessionClient(t)
	ctx, tally := WithDispatchTally(tctx(t))
	_, err := mc.CallTool(ctx, "read_thing", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), tally.Count())
}

// Session recovery repeats the request on the re-initialized session: two
// requests left for the transport, so two attempts.
func TestDispatchTally_SessionRecoveryCountsBothRequests(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()
	ctx, tally := WithDispatchTally(tctx(t))
	_, err := mc.CallTool(ctx, "read_thing", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), mc.SessionReinitCount())
	assert.Equal(t, int64(2), tally.Count())
}

// A recovery that is then refused (tool hash changed) still sent the first
// request, even though the final error is ErrConnectionGenerationChanged.
func TestDispatchTally_RefusedAfterFirstRequestStillCountsIt(t *testing.T) {
	mc, up := newSessionClient(t)
	up.mu.Lock()
	up.readDesc = "reads, but now differently"
	up.mu.Unlock()
	up.forget()

	epoch := mc.ConnectionEpoch()
	ctx, tally := WithDispatchTally(tctx(t))
	_, err := mc.CallToolOnEpoch(ctx, "read_thing", nil, epoch)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnectionGenerationChanged), "got %v", err)
	assert.Equal(t, int64(1), tally.Count())
}

// A pinned call on a moved generation never reaches the transport.
func TestDispatchTally_RefusedBeforeTransportCountsNothing(t *testing.T) {
	mc, _ := newSessionClient(t)
	ctx, tally := WithDispatchTally(tctx(t))
	_, err := mc.CallToolOnEpoch(ctx, "read_thing", nil, mc.ConnectionEpoch()+1000)
	require.Error(t, err)
	assert.Equal(t, int64(0), tally.Count())
}

func TestDispatchTally_NilAndAbsentAreSafe(t *testing.T) {
	var nilTally *DispatchTally
	assert.Equal(t, int64(0), nilTally.Count())
	mc, _ := newSessionClient(t)
	_, err := mc.CallTool(tctx(t), "read_thing", nil) // no tally in the context
	require.NoError(t, err)
}
