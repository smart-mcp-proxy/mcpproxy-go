//go:build server

package multiuser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// stubActivityProvider is the smallest storage provider GetUserActivity needs.
// It records whether it was consulted at all, which is what distinguishes
// "refused before reading" from "read and then filtered to nothing".
type stubActivityProvider struct {
	records []*storage.ActivityRecord
	calls   int
}

func (s *stubActivityProvider) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, r := range s.records {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

func (s *stubActivityProvider) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	s.calls++
	if filter.Offset >= len(s.records) {
		return nil, len(s.records), nil
	}
	end := filter.Offset + filter.Limit
	if end > len(s.records) {
		end = len(s.records)
	}
	return s.records[filter.Offset:end], len(s.records), nil
}

// TestActivityFilter_AgentTokenIsNotItsOwnersSession is the sweep finding from
// the same root cause as the brokered-connection regression: carrying a new
// field (UserID) through a shared constructor changes every reader of it.
//
// GetUserActivity gated on `ac.UserID != ""`. Before issue #1168 an agent token
// carried no UserID, so that check refused it; afterwards the field is its
// OWNER's id, and the same check would hand a scoped, read-only agent token its
// owner's entire activity log. The gate belongs on the user TIER — the rule this
// branch already applied in getUserID and never propagated here.
//
// Oracle discipline: this does NOT assert "the response omits the records" (an
// empty result is what a working filter and a broken fixture both produce).
// It asserts the request is REFUSED, and pairs that with a positive control on
// the same filter and the same fixture proving the owner's session DOES see the
// records — so an empty agent result cannot be an empty store.
//
// BITES: without the IsUser() gate the agent context returns the owner's record.
func TestActivityFilter_AgentTokenIsNotItsOwnersSession(t *testing.T) {
	provider := &stubActivityProvider{records: []*storage.ActivityRecord{
		{ID: "rec-1", UserID: "alice"},
	}}
	filter := NewActivityFilter(provider)

	// Positive control: the OWNER's own session sees the record, so the fixture
	// really landed and the filter really reads it.
	ownerRecords, ownerTotal, err := filter.GetUserActivity(userCtx("alice"), 10, 0)
	require.NoError(t, err, "positive control: the owner's session must be able to read their activity")
	require.Equal(t, 1, ownerTotal, "positive control: the seeded record must be visible to its owner")
	require.Len(t, ownerRecords, 1)
	require.Equal(t, "rec-1", ownerRecords[0].ID)

	callsBefore := provider.calls

	// The agent token owned by alice must be refused outright.
	tok := &auth.AgentToken{Name: "ci", UserID: "alice", Permissions: []string{auth.PermRead}}
	agentContext := auth.WithAuthContext(context.Background(), tok.AuthContext())

	agentRecords, agentTotal, err := filter.GetUserActivity(agentContext, 10, 0)
	require.Error(t, err, "an agent token is not its owner's session and must not read their activity log")
	assert.Nil(t, agentRecords)
	assert.Zero(t, agentTotal)
	assert.Equal(t, callsBefore, provider.calls,
		"the refusal must happen before storage is consulted, not by filtering afterwards")
}

// TestActivityFilter_EnrichRecordStillAttributesAgentTokens pins the OTHER half
// of the sweep: attribution is exactly what #1168 added the UserID for, and
// hardening the authorization gate must not undo it. EnrichRecord stays
// UserID-based on purpose.
func TestActivityFilter_EnrichRecordStillAttributesAgentTokens(t *testing.T) {
	filter := NewActivityFilter(&stubActivityProvider{})

	tok := &auth.AgentToken{Name: "ci", UserID: "alice", Permissions: []string{auth.PermRead}}
	agentContext := auth.WithAuthContext(context.Background(), tok.AuthContext())

	record := &storage.ActivityRecord{ID: "rec-2"}
	filter.EnrichRecord(agentContext, record)

	assert.Equal(t, "alice", record.UserID,
		"an agent token's activity must still be attributed to its owner — that is what #1168 carried the UserID for")
}
