package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 FR-024: every skill search, load and read is a skill_access row
// that carries sizes and the hash, and never the skill text.

func TestEmitActivitySkillAccess_PayloadAlwaysNamesTheServerKey(t *testing.T) {
	rt := &Runtime{logger: zap.NewNop(), eventSubs: make(map[chan Event]struct{})}
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.EmitActivitySkillAccess(SkillAccessActivity{
		Action: SkillActionSearch, Tool: "retrieve_skills", Outcome: SkillAccessSuccess,
		Query: "pdf forms", Results: 2, Matches: 7, SessionID: "s1", RequestID: "r1",
	})

	select {
	case evt := <-ch:
		require.Equal(t, EventTypeActivitySkillAccess, evt.Type)
		server, ok := evt.Payload["server_name"]
		require.True(t, ok, "the SSE door drops an unnamed frame only when the key type is identity-bearing; the key must exist even for a search")
		assert.Equal(t, "", server)
		assert.Equal(t, "pdf forms", evt.Payload["query"])
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.skill_access event")
	}
}

func TestActivityService_SkillAccessRowHasSizesAndNoText(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivitySkillAccess,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "hf", "action": "load", "tool": "load_skill", "outcome": "success",
			"skill_id": "hf:hf-cli", "skill": "hf-cli", "hash": "sk1-abc",
			"offset": int64(0), "bytes": int64(120), "total_bytes": int64(900),
			"session_id": "s1", "request_id": "r1", "duration_ms": int64(3),
			"detection_text": "SECRET BODY MUST NOT BE STORED",
			"attribution":    testAttribution().payload(),
		},
	})

	rec := onlyRecord(t, store)
	assert.Equal(t, storage.ActivityTypeSkillAccess, rec.Type)
	assert.Equal(t, "hf", rec.ServerName)
	assert.Equal(t, "success", rec.Status)
	assert.Equal(t, "load", rec.Metadata["action"])
	assert.Equal(t, "sk1-abc", rec.Metadata["hash"])
	assert.EqualValues(t, 120, rec.Metadata["bytes"])
	assert.Equal(t, "hf:hf-cli", rec.Arguments["id"])
	assert.Empty(t, rec.Response, "skill text is never stored")
	assert.NotContains(t, rec.Metadata, "detection_text")
	assert.Equal(t, "client-cursor", rec.TokenName)
}

func TestActivityService_SkillAccessFailureIsAnErrorRowWithTheOutcome(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivitySkillAccess,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "", "action": "load", "tool": "load_skill", "outcome": "not_found",
			"skill_id": "beta:x", "request_id": "r2",
		},
	})
	rec := onlyRecord(t, store)
	assert.Equal(t, "error", rec.Status)
	assert.Equal(t, "not_found", rec.ErrorMessage)
	assert.Equal(t, "", rec.ServerName)
}

func TestSkillAccessIsAValidActivityType(t *testing.T) {
	assert.Contains(t, storage.ValidActivityTypes, string(storage.ActivityTypeSkillAccess))
	assert.True(t, storage.IsAdminOnlyActivityType(storage.ActivityTypeSkillAccess),
		"an access row names the id its caller asked for, which may be a held skill")
}
