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
// sentence and puts it on the payload, for every type and flag combination.
// This is the wiring test; the meaning of each cell is pinned in
// internal/contracts/activity_truncation_test.go.
func TestStorageToContractActivity_CarriesTheResolvedTruncationNotice(t *testing.T) {
	for _, recordType := range []storage.ActivityType{
		storage.ActivityTypeToolCall,
		storage.ActivityTypeInternalToolCall,
		storage.ActivityTypePromptGet,
	} {
		for _, forward := range []bool{false, true} {
			for _, storageCut := range []bool{false, true} {
				rec := &storage.ActivityRecord{
					ID:                       "01ABC",
					Type:                     recordType,
					Response:                 "body",
					ResponseTruncated:        forward,
					ResponseStorageTruncated: storageCut,
					ResponseBytes:            200039,
				}

				got := storageToContractActivity(rec)
				want := contracts.ResolveResponseTruncation(
					string(recordType), forward, storageCut).NoticeWithBytes(200039)

				assert.Equal(t, want, got.ResponseTruncationNotice,
					"%s forward=%v storage=%v", recordType, forward, storageCut)
			}
		}
	}
}

// The cell four review rounds got wrong, asserted end-to-end on the payload the
// Web UI actually receives: activity_service.go cuts the already-forwarded text
// again, so the stored body is STRICTLY SHORTER than what the agent received.
func TestBothFlagsToolCallNoticeDoesNotClaimItIsTheAgentsCopy(t *testing.T) {
	got := storageToContractActivity(&storage.ActivityRecord{
		Type:                     storage.ActivityTypeToolCall,
		Response:                 "body",
		ResponseTruncated:        true,
		ResponseStorageTruncated: true,
		ResponseBytes:            200039,
	})

	require.NotEmpty(t, got.ResponseTruncationNotice)
	assert.NotContains(t, got.ResponseTruncationNotice, "agent's own copy")
	assert.Contains(t, got.ResponseTruncationNotice, "MORE")
	assert.Contains(t, got.ResponseTruncationNotice, "tool_response_limit")
	assert.Contains(t, got.ResponseTruncationNotice, "activity_max_response_size")
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
