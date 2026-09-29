package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestSessionStore_ClientIdentityAndProfile pins T039: TokenName/ClientID
// set at initialize, Profile/ProfileSource updated per call, and
// SessionsForToken fan-out for the FR-026/FR-027 notification seam.
func TestSessionStore_ClientIdentityAndProfile(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("sess-1", "cursor-app", "1.0", false, false, nil)
	store.SetSession("sess-2", "cursor-app", "1.0", false, false, nil)
	store.SetSession("sess-3", "other-app", "1.0", false, false, nil)

	store.SetSessionIdentity("sess-1", "client-cursor", "cursor")
	store.SetSessionIdentity("sess-2", "client-cursor", "cursor")
	store.SetSessionIdentity("sess-3", "client-laptop", "laptop")

	info := store.GetSession("sess-1")
	assert.Equal(t, "client-cursor", info.TokenName)
	assert.Equal(t, "cursor", info.ClientID)

	store.UpdateSessionProfile("sess-1", "work-readonly", "pin")
	info = store.GetSession("sess-1")
	assert.Equal(t, "work-readonly", info.Profile)
	assert.Equal(t, "pin", info.ProfileSource)

	// Unrelated session must be unaffected.
	other := store.GetSession("sess-3")
	assert.Empty(t, other.Profile)

	ids := store.SessionsForToken("client-cursor")
	assert.ElementsMatch(t, []string{"sess-1", "sess-2"}, ids)
	assert.Empty(t, store.SessionsForToken("no-such-token"))
	assert.Empty(t, store.SessionsForToken(""))
}
