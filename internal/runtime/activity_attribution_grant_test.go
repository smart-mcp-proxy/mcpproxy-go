package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// A browser login's grant id rides on the attribution and lands as
// metadata.grant_id on the record (Spec 132 R3): two logins of one client can
// be told apart in the log. The grant id is not a secret.
func TestActivityAttribution_GrantIDRoundTripsToMetadata(t *testing.T) {
	a := testAttribution()
	a.GrantID = "grantAAAAAAAAAAAAAAAAAA"
	assert.Equal(t, "grantAAAAAAAAAAAAAAAAAA", attributionFromPayload(map[string]any{attributionPayloadKey: a.payload()}).GrantID)
	assert.NotContains(t, ActivityAttribution{Profile: "p"}.payload(), "grant_id", "no grant, no key")

	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())
	svc.handleEvent(Event{
		Type: EventTypeActivityToolCallCompleted, Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "tool_name": "list_issues", "session_id": "s1", "request_id": "r1",
			"source": "mcp", "status": "success", "duration_ms": int64(5), "attribution": a.payload(),
		},
	})
	rec := onlyRecord(t, store)
	require.Equal(t, "grantAAAAAAAAAAAAAAAAAA", rec.Metadata["grant_id"])
	// A row with a grant id was authenticated by a browser login, never by an
	// agent token: it is labelled as the OAuth credential kind.
	require.Equal(t, "oauth", rec.Metadata["credential_kind"])
}

func TestActivityAttribution_NoGrantNoCredentialKind(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())
	svc.handleEvent(Event{
		Type: EventTypeActivityToolCallCompleted, Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "tool_name": "list_issues", "session_id": "s1", "request_id": "r1",
			"source": "mcp", "status": "success", "duration_ms": int64(5), "attribution": testAttribution().payload(),
		},
	})
	rec := onlyRecord(t, store)
	require.NotContains(t, rec.Metadata, "credential_kind", "an API-key row is unchanged")
}
