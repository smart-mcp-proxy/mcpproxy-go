package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The Web UI drawer renders `response_truncation_notice` on BOTH truncation
// badges instead of composing a tooltip per badge. That is the fix for the
// blocking finding: a per-badge tooltip can only see one flag, so a reader
// hovering "Truncated" on a both-flags record was told the stored body is the
// agent's own copy — definitionally false, with no correction anywhere in that
// tooltip.
//
// The precondition for that fix is that the backend actually resolves the
// sentence and puts it on the payload, for every STAMP and flag combination.
// This is the wiring test; the meaning of each cell is pinned in
// internal/contracts/activity_truncation_test.go.
func TestStorageToContractActivity_CarriesTheResolvedTruncationNotice(t *testing.T) {
	for _, cut := range []contracts.ResponseCut{
		contracts.CutNone,
		contracts.CutShortenedAgentAndRecord,
		contracts.CutShortenedAgentOnly,
		contracts.CutShortenedRecordOnly,
	} {
		for _, forward := range []bool{false, true} {
			for _, storageCut := range []bool{false, true} {
				rec := &storage.ActivityRecord{
					ID:                       "01ABC",
					Type:                     storage.ActivityTypeToolCall,
					Response:                 "body",
					ResponseTruncated:        forward,
					ResponseTruncationCut:    cut,
					ResponseStorageTruncated: storageCut,
					ResponseBytes:            200039,
				}

				got := storageToContractActivity(rec)
				want := contracts.ResolveResponseTruncation(
					cut, forward, storageCut).NoticeWithBytes(200039)

				assert.Equal(t, want, got.ResponseTruncationNotice,
					"cut=%q forward=%v storage=%v", cut, forward, storageCut)
				assert.Equal(t, cut, got.ResponseTruncationCut,
					"the stamp itself must reach the payload, not only the prose")
			}
		}
	}
}

// The round-4 regression, asserted on the payload the Web UI actually receives.
//
// A code-execution sub-call and an ordinary upstream dispatch are BOTH
// Type=tool_call records with response_truncated=true, and their cuts run
// opposite ways. Any renderer that reads a direction out of the type — as four
// rounds of them did — is definitionally wrong about one of these two.
func TestTwoToolCallRecordsWithOppositeCutsGetOppositeNotices(t *testing.T) {
	base := func(cut contracts.ResponseCut) *storage.ActivityRecord {
		return &storage.ActivityRecord{
			Type:                  storage.ActivityTypeToolCall,
			Response:              "body",
			ResponseTruncated:     true,
			ResponseTruncationCut: cut,
			ResponseBytes:         200039,
		}
	}

	ordinary := storageToContractActivity(base(contracts.CutShortenedAgentAndRecord))
	subCall := storageToContractActivity(base(contracts.CutShortenedRecordOnly))

	require.NotEmpty(t, ordinary.ResponseTruncationNotice)
	require.NotEmpty(t, subCall.ResponseTruncationNotice)
	require.NotEqual(t, ordinary.ResponseTruncationNotice, subCall.ResponseTruncationNotice,
		"two tool_call records whose cuts point opposite ways must not read the same")

	assert.Contains(t, ordinary.ResponseTruncationNotice, "agent's own copy")
	assert.Contains(t, subCall.ResponseTruncationNotice, "MORE")
	assert.NotContains(t, subCall.ResponseTruncationNotice, "agent's own copy")
	// A sub-call's cut is subCallActivityResponseLimit, not tool_response_limit.
	// Naming the wrong knob sends an operator to a setting that cannot fix it.
	assert.NotContains(t, subCall.ResponseTruncationNotice, "tool_response_limit")
}

// The cell rounds 2 and 3 got wrong, asserted end-to-end: activity_service.go
// cuts the already-forwarded text again, so the stored body is STRICTLY SHORTER
// than what the agent received.
func TestBothFlagsForwardCutNoticeDoesNotClaimItIsTheAgentsCopy(t *testing.T) {
	got := storageToContractActivity(&storage.ActivityRecord{
		Type:                     storage.ActivityTypeToolCall,
		Response:                 "body",
		ResponseTruncated:        true,
		ResponseTruncationCut:    contracts.CutShortenedAgentAndRecord,
		ResponseStorageTruncated: true,
		ResponseBytes:            200039,
	})

	require.NotEmpty(t, got.ResponseTruncationNotice)
	assert.NotContains(t, got.ResponseTruncationNotice, "agent's own copy")
	assert.Contains(t, got.ResponseTruncationNotice, "MORE")
	assert.Contains(t, got.ResponseTruncationNotice, "tool_response_limit")
	assert.Contains(t, got.ResponseTruncationNotice, "activity_max_response_size")
}

// A record from an older core carries the flag and no stamp. The payload must
// say a cut happened and claim NO direction — the alternative is to borrow one
// emitter's direction for a record that may have come from another, which is
// the whole history of this bug.
func TestLegacyUnstampedRecordGetsADirectionFreeNotice(t *testing.T) {
	// unstamped-legacy-record-on-purpose: this is what an older core wrote.
	got := storageToContractActivity(&storage.ActivityRecord{
		Type:              storage.ActivityTypeToolCall,
		Response:          "body",
		ResponseTruncated: true,
		ResponseBytes:     200039,
	})

	require.NotEmpty(t, got.ResponseTruncationNotice)
	assert.NotContains(t, got.ResponseTruncationNotice, "agent's own copy")
	assert.NotContains(t, got.ResponseTruncationNotice, "LESS")
	assert.NotContains(t, got.ResponseTruncationNotice, "MORE")
	assert.Contains(t, got.ResponseTruncationNotice, "older mcpproxy")
}

// An untruncated record must not carry a sentence: the badges it would annotate
// are not rendered, and a stray notice would be the only claim on the payload.
func TestUntruncatedRecordCarriesNoNotice(t *testing.T) {
	got := storageToContractActivity(&storage.ActivityRecord{
		Type:          storage.ActivityTypeToolCall,
		Response:      "body",
		ResponseBytes: 120,
	})
	assert.Empty(t, got.ResponseTruncationNotice)
}
