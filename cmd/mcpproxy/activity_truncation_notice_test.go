package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// `mcpproxy activity show --include-response` prints the stored body. When that
// body was cut, the reader has to be told WHICH cut happened and which
// DIRECTION it points.
//
// The CLI used to render one line per flag, which structurally could not get
// this right; round 4 replaced that with a resolver keyed on the record TYPE,
// which also could not, because two different emitters write Type=tool_call
// records whose cuts run opposite ways. The direction now arrives on the record
// as `response_truncation_cut`. These tests pin the CLI to
// contracts.ResolveResponseTruncation instead of to any wording of its own; the
// full stamp x flags table lives in
// internal/contracts/activity_truncation_test.go.

// The load-bearing test: the CLI must not have a sentence of its own. Whatever
// the resolver says for the cell, verbatim, is what gets printed — and the
// record TYPE must not change a single character of it.
func TestCLIRendersTheResolverVerbatim(t *testing.T) {
	cuts := []contracts.ResponseCut{
		contracts.CutNone,
		contracts.CutShortenedAgentAndRecord,
		contracts.CutShortenedAgentOnly,
		contracts.CutShortenedRecordOnly,
	}
	for _, recordType := range []string{"tool_call", "internal_tool_call", "prompt_get", "policy_decision"} {
		for _, cut := range cuts {
			for _, forward := range []bool{false, true} {
				for _, storage := range []bool{false, true} {
					for _, bytes := range []int{0, 200039} {
						activity := map[string]interface{}{"type": recordType}
						if cut != contracts.CutNone {
							activity["response_truncation_cut"] = string(cut)
						}
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

						want := contracts.ResolveResponseTruncation(cut, forward, storage)
						got := activityTruncationNotices(activity)

						if !want.Truncated() {
							assert.Empty(t, got, "%s cut=%q f=%v s=%v", recordType, cut, forward, storage)
							continue
						}
						require.Len(t, got, 1,
							"one resolved statement, never one line per flag (%s cut=%q f=%v s=%v)",
							recordType, cut, forward, storage)
						assert.Equal(t, want.NoticeWithBytes(bytes), got[0],
							"%s cut=%q f=%v s=%v bytes=%d", recordType, cut, forward, storage, bytes)
					}
				}
			}
		}
	}
}

// The round-4 regression at the CLI. Two Type=tool_call records, both
// response_truncated, cuts pointing opposite ways: the printed lines must
// differ, and the sub-call's must not describe a forward cut.
func TestTwoToolCallRecordsWithOppositeCutsPrintDifferently(t *testing.T) {
	line := func(cut contracts.ResponseCut) string {
		notices := activityTruncationNotices(map[string]interface{}{
			"type":                    "tool_call",
			"response_truncated":      true,
			"response_truncation_cut": string(cut),
			"response_bytes":          float64(200039),
		})
		require.Len(t, notices, 1)
		return notices[0]
	}

	ordinary := line(contracts.CutShortenedAgentAndRecord)
	subCall := line(contracts.CutShortenedRecordOnly)

	require.NotEqual(t, ordinary, subCall)
	assert.Contains(t, ordinary, "agent's own copy")
	assert.Contains(t, subCall, "MORE")
	assert.NotContains(t, subCall, "agent's own copy")
	assert.NotContains(t, subCall, "tool_response_limit",
		"a sub-call is cut at the activity-log limit; naming tool_response_limit "+
			"sends an operator to a setting that cannot change it")
	// The whole response WAS delivered, so the pre-cut size is the delivered size.
	assert.Contains(t, subCall, "measures the response the agent received")
}

// A both-flags forward-cut record is STRICTLY SHORTER than the agent's copy
// (activity_service.go cuts the already-forwarded text again), so the printed
// line must not claim the reader is looking at what the agent received.
func TestBothFlagsForwardCutNeverClaimsTheStoredBodyIsTheAgentsCopy(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "tool_call",
		"response_truncated":         true,
		"response_truncation_cut":    string(contracts.CutShortenedAgentAndRecord),
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
	// response_bytes is the PRE-cut upstream size here, larger than both
	// bodies, so it must not be quoted as what was delivered.
	assert.Contains(t, notices[0], "describes neither")
}

// Same shape for the built-in, where the two cuts point OPPOSITE ways: the
// record holds a prefix of the pre-cut text and the agent holds a per-block
// cut of it, under two unrelated limits. The honest answer is that neither is
// known to be larger — asserting either direction is a guess.
func TestBothFlagsAgentOnlyCutClaimsNoDirection(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "internal_tool_call",
		"response_truncated":         true,
		"response_truncation_cut":    string(contracts.CutShortenedAgentOnly),
		"response_storage_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.NotContains(t, notices[0], "LESS")
	assert.NotContains(t, notices[0], "MORE")
	assert.Contains(t, notices[0], "Neither body")
}

func TestAgentOnlyCutSaysTheLogHoldsMore(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                    "internal_tool_call",
		"response_truncated":      true,
		"response_truncation_cut": string(contracts.CutShortenedAgentOnly),
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "LESS",
		"a built-in records its full response while the agent gets the cut copy")
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.NotContains(t, notices[0], "activity_max_response_size")
}

func TestAgentAndRecordCutDoesNotClaimTheAgentSawLess(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                    "tool_call",
		"response_truncated":      true,
		"response_truncation_cut": string(contracts.CutShortenedAgentAndRecord),
		"response_bytes":          float64(200039), // JSON numbers decode as float64
	})

	require.Len(t, notices, 1)
	// With the storage cut absent the record holds the POST-forward text, so
	// the agent received exactly this — claiming otherwise inverts what
	// happened.
	assert.NotContains(t, notices[0], "LESS",
		"the record stores the forwarded copy, so the agent received exactly this")
	assert.Contains(t, notices[0], "agent's own copy")
	assert.Contains(t, notices[0], "tool_response_limit")
	// response_bytes is pre-cut, so it describes the UPSTREAM payload, not
	// what was delivered.
	assert.Contains(t, notices[0], "200039")
	assert.Contains(t, notices[0], "upstream")
}

// The three stamps must not render the same sentence for a response cut: that
// identity is exactly the universal-claim bug.
func TestResponseCutWordingDiffersByStamp(t *testing.T) {
	line := func(cut contracts.ResponseCut) string {
		notices := activityTruncationNotices(map[string]interface{}{
			"type":                    "tool_call",
			"response_truncated":      true,
			"response_truncation_cut": string(cut),
		})
		require.Len(t, notices, 1)
		return notices[0]
	}

	seen := map[string]bool{}
	for _, cut := range []contracts.ResponseCut{
		contracts.CutShortenedAgentAndRecord,
		contracts.CutShortenedAgentOnly,
		contracts.CutShortenedRecordOnly,
	} {
		l := line(cut)
		assert.False(t, seen[l], "two stamps produced the same sentence: %q", l)
		seen[l] = true
	}
}

// A record from an older core carries the flag and no stamp. The CLI must state
// what happened and claim no direction — the type must not fill one in, on any
// type, including the two that DO have live emitters.
func TestUnstampedRecordClaimsNoDirectionOnEveryType(t *testing.T) {
	for _, recordType := range []string{"tool_call", "internal_tool_call", "prompt_get", "policy_decision"} {
		notices := activityTruncationNotices(map[string]interface{}{
			"type":               recordType,
			"response_truncated": true,
		})

		require.Len(t, notices, 1, recordType)
		assert.Contains(t, notices[0], "older mcpproxy", recordType)
		assert.NotContains(t, notices[0], "LESS", recordType)
		assert.NotContains(t, notices[0], "MORE", recordType)
		assert.NotContains(t, notices[0], "agent's own copy", recordType)
	}
}

// An unrecognised stamp — a record written by a NEWER core — must be treated as
// unstamped, not mapped onto a neighbouring direction.
func TestUnrecognisedStampClaimsNoDirection(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                    "tool_call",
		"response_truncated":      true,
		"response_truncation_cut": "some_future_cut",
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "older mcpproxy")
	assert.NotContains(t, notices[0], "agent's own copy")
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

// The storage cut is direction-stable when it is the ONLY cut, whatever the
// type and whatever stamp happens to sit on the record: response_bytes is what
// was delivered in every case.
func TestStorageTruncationReadsTheSameForEveryTypeAndStamp(t *testing.T) {
	var seen []string
	for _, recordType := range []string{"tool_call", "internal_tool_call", "prompt_get"} {
		for _, cut := range []contracts.ResponseCut{
			contracts.CutNone,
			contracts.CutShortenedAgentAndRecord,
			contracts.CutShortenedAgentOnly,
			contracts.CutShortenedRecordOnly,
		} {
			activity := map[string]interface{}{
				"type":                       recordType,
				"response_storage_truncated": true,
				"response_bytes":             float64(4096),
			}
			if cut != contracts.CutNone {
				activity["response_truncation_cut"] = string(cut)
			}
			notices := activityTruncationNotices(activity)
			require.Len(t, notices, 1, recordType)
			seen = append(seen, notices[0])
		}
	}
	for i := range seen {
		assert.Equal(t, seen[0], seen[i])
	}
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
