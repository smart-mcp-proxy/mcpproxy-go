package runtime

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// truncatedBuiltinOverstatesDelivery's doc is the reasoning a maintainer uses to
// decide whether a byte figure may enter the usage timeline, and it had the
// tool_call half backwards on both counts: an upstream tool_call is cut on the
// way OUT, not on the way into the log, and its record holds the agent's copy or
// less — never the whole response the agent supposedly received.
//
// It also framed RespBytesSum as "delivered traffic". It is not: it sums
// ActivityRecord.ResponseBytes, the PRE-forward upstream payload size, which for
// a forward-truncated tool_call is larger than both the stored and the delivered
// body — and that record IS summed in (countInTimeBucket). Sum and label
// disagreed, so the label had to go; the sum is correct for what the field
// actually is, a response-VOLUME metric, which is also how the API and the
// "Response Size" UI present it.
func TestUsageAggregateDocsDoNotCallRespBytesSumDeliveredTraffic(t *testing.T) {
	raw, err := os.ReadFile("usage_aggregate.go")
	require.NoError(t, err)
	src := string(raw)

	for _, falsified := range []string{
		// The backwards half.
		"an upstream tool_call is truncated on the way into the LOG while the agent",
		"received the whole response, so the pre-truncation length is honest",
		// The mislabel, in its original phrasing.
		"and so must not be added to\n// delivered traffic",
	} {
		assert.NotContains(t, src, falsified,
			"contradicted by the direction matrix in internal/contracts/activity_truncation.go")
	}

	// The field must carry its real definition where a reader will find it, and
	// the predicate must point at the single authority instead of re-deriving.
	assert.Contains(t, src, "not delivered traffic",
		"RespBytesSum's own doc has to say what it is not, or the label comes back")
	assert.Contains(t, src, "contracts.ResolveResponseTruncation")
	assert.Contains(t, src, "internal/contracts/activity_truncation.go")
}
