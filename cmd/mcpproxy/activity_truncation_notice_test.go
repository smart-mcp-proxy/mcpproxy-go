package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// `mcpproxy activity show --include-response` prints the stored body. When that
// body was cut, the reader has to be told WHICH cut happened and which
// DIRECTION it points — and that depends on the record TYPE *and* on whether
// both cuts landed, not on either flag alone.
//
// The CLI used to render one line per flag, which structurally could not get
// this right: the forward line never saw the storage flag, so a record carrying
// both was told the stored body "IS the agent's copy" when it is strictly
// shorter than what the agent received. These tests pin the CLI to
// contracts.ResolveResponseTruncation instead of to any wording of its own; the
// full 3-types x 4-combinations table lives in
// internal/contracts/activity_truncation_test.go.

// The load-bearing test: the CLI must not have a sentence of its own. Whatever
// the resolver says for the cell, verbatim, is what gets printed.
func TestCLIRendersTheResolverVerbatim(t *testing.T) {
	for _, recordType := range []string{"tool_call", "internal_tool_call", "prompt_get", "policy_decision"} {
		for _, forward := range []bool{false, true} {
			for _, storage := range []bool{false, true} {
				for _, bytes := range []int{0, 200039} {
					activity := map[string]interface{}{"type": recordType}
					if forward {
						activity["response_truncated"] = true
					}
					if storage {
						activity["response_storage_truncated"] = true
					}
					if bytes > 0 {
						// JSON numbers decode as float64.
						activity["response_bytes"] = float64(bytes)
					}

					want := contracts.ResolveResponseTruncation(recordType, forward, storage)
					got := activityTruncationNotices(activity)

					if !want.Truncated() {
						assert.Empty(t, got, "%s f=%v s=%v", recordType, forward, storage)
						continue
					}
					require.Len(t, got, 1,
						"one resolved statement, never one line per flag (%s f=%v s=%v)",
						recordType, forward, storage)
					assert.Equal(t, want.NoticeWithBytes(bytes), got[0],
						"%s f=%v s=%v bytes=%d", recordType, forward, storage, bytes)
				}
			}
		}
	}
}

// The blocking finding, stated as an outcome rather than as a string match: a
// both-flags tool_call record is STRICTLY SHORTER than the agent's copy
// (activity_service.go cuts the already-forwarded text again), so the printed
// line must not claim the reader is looking at what the agent received.
func TestBothFlagsToolCallNeverClaimsTheStoredBodyIsTheAgentsCopy(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "tool_call",
		"response_truncated":         true,
		"response_storage_truncated": true,
		"response_bytes":             float64(200039),
	})

	require.Len(t, notices, 1)
	assert.NotContains(t, notices[0], "agent's own copy",
		"the storage cut shortened the forwarded text again; this is not the agent's copy")
	assert.Contains(t, notices[0], "MORE",
		"the agent received more than this")
	// Both settings an operator can change must be named; hiding either leaves
	// the reader unable to act.
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.Contains(t, notices[0], "activity_max_response_size")
	// response_bytes is the PRE-forward upstream size here, larger than both
	// bodies, so it must not be quoted as what was delivered.
	assert.Contains(t, notices[0], "describes neither")
}

// Same shape for the built-in, where the two cuts point OPPOSITE ways: the
// record holds a prefix of the pre-forward text and the agent holds a per-block
// cut of it, under two unrelated limits. The honest answer is that neither is
// known to be larger — asserting either direction is a guess.
func TestBothFlagsInternalCallClaimsNoDirection(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "internal_tool_call",
		"response_truncated":         true,
		"response_storage_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.NotContains(t, notices[0], "LESS")
	assert.NotContains(t, notices[0], "MORE")
	assert.Contains(t, notices[0], "Neither body")
}

func TestForwardTruncatedInternalCallSaysTheLogHoldsMore(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "internal_tool_call",
		"response_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "LESS",
		"a built-in records its full response while the agent gets the cut copy")
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.NotContains(t, notices[0], "activity_max_response_size")
}

func TestForwardTruncatedToolCallDoesNotClaimTheAgentSawLess(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "tool_call",
		"response_truncated": true,
		"response_bytes":     float64(200039), // JSON numbers decode as float64
	})

	require.Len(t, notices, 1)
	// With the storage cut absent the record holds the POST-forward text, so
	// the agent received exactly this — claiming otherwise inverts what
	// happened.
	assert.NotContains(t, notices[0], "LESS",
		"the tool_call record stores the forwarded copy, so the agent received exactly this")
	assert.Contains(t, notices[0], "agent's own copy")
	assert.Contains(t, notices[0], "tool_response_limit")
	// response_bytes is pre-forward, so it describes the UPSTREAM payload, not
	// what was delivered.
	assert.Contains(t, notices[0], "200039")
	assert.Contains(t, notices[0], "upstream")
}

// The two types must not render the same sentence for a forward cut: that
// identity is exactly the universal-claim bug.
func TestForwardTruncationWordingDiffersByRecordType(t *testing.T) {
	internalNotice := activityTruncationNotices(map[string]interface{}{
		"type":               "internal_tool_call",
		"response_truncated": true,
	})
	toolNotice := activityTruncationNotices(map[string]interface{}{
		"type":               "tool_call",
		"response_truncated": true,
	})

	require.Len(t, internalNotice, 1)
	require.Len(t, toolNotice, 1)
	assert.NotEqual(t, internalNotice[0], toolNotice[0])
}

// No other type sets response_truncated today. If one ever does, it gets the
// direction-free statement of fact rather than a borrowed direction.
func TestForwardTruncationOnAnUnknownTypeClaimsNoDirection(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "prompt_get",
		"response_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.NotContains(t, notices[0], "LESS")
	assert.NotContains(t, notices[0], "MORE")
}

func TestStorageTruncatedResponseIsExplained(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "tool_call",
		"response_storage_truncated": true,
		"response_bytes":             float64(200039),
	})

	require.Len(t, notices, 1, "only the storage cut applies to this record")
	assert.Contains(t, notices[0], "activity_max_response_size",
		"the notice must name the setting an operator can change")
	assert.Contains(t, notices[0], "MORE",
		"nothing cut the response on the way out, so the agent saw the whole 200039 bytes")
	assert.Contains(t, notices[0], "200039")
}

// The storage cut is direction-stable across types when it is the ONLY cut:
// response_bytes is what was delivered in every case.
func TestStorageTruncationReadsTheSameForAllTypes(t *testing.T) {
	var seen []string
	for _, recordType := range []string{"tool_call", "internal_tool_call", "prompt_get"} {
		notices := activityTruncationNotices(map[string]interface{}{
			"type":                       recordType,
			"response_storage_truncated": true,
			"response_bytes":             float64(4096),
		})
		require.Len(t, notices, 1, recordType)
		seen = append(seen, notices[0])
	}
	assert.Equal(t, seen[0], seen[1])
	assert.Equal(t, seen[0], seen[2])
}

// A record with neither flag says nothing: the body printed above it is whole.
func TestUntruncatedResponseGetsNoNotice(t *testing.T) {
	assert.Empty(t, activityTruncationNotices(map[string]interface{}{
		"type":           "tool_call",
		"response_bytes": float64(120),
	}))
}

// A legacy record predating the byte counts still gets the explanation; it
// simply cannot quantify it. Printing "0 bytes" would read as an empty
// response, which is the opposite of what happened.
func TestTruncationWithoutByteCountStillExplains(t *testing.T) {
	for _, flag := range []string{"response_storage_truncated", "response_truncated"} {
		notices := activityTruncationNotices(map[string]interface{}{
			"type": "tool_call",
			flag:   true,
		})

		require.Len(t, notices, 1, flag)
		assert.NotContains(t, notices[0], "0 bytes", flag)
		assert.NotContains(t, notices[0], "response_bytes", flag)
	}
}
