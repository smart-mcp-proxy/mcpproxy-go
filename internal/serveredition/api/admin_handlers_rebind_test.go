//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// Spec 107 T038/T045 — the administrator-opened rebind window (FR-023):
// `enable` arms `subject_rebind_armed_at` only on a real disabled→enabled
// transition, `disable` clears it, and GET /admin/users exposes the field
// beside `groups` / `groups_updated_at` (contracts/rest-endpoints.md §4).
//
// Compile-red until T044 adds User.SubjectRebindArmedAt / Groups /
// GroupsUpdatedAt; behaviour-red until T045 wires the handlers.

func rebindArmedAt(t *testing.T, store *users.UserStore, id string) *time.Time {
	t.Helper()
	u, err := store.GetUser(id)
	require.NoError(t, err)
	require.NotNil(t, u)
	return u.SubjectRebindArmedAt
}

func postAdmin(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestEnableUser_ArmsSubjectRebindOnRealTransition(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	user := createTestUser(t, store, "user-rebind", "rebind@example.com", "Rebind Me", "google")
	user.Disabled = true
	require.NoError(t, store.UpdateUser(user))
	require.Nil(t, rebindArmedAt(t, store, user.ID), "precondition: closed")

	before := time.Now().UTC()
	w := postAdmin(t, router, "/api/v1/admin/users/user-rebind/enable")
	require.Equal(t, http.StatusOK, w.Code)

	updated, err := store.GetUser(user.ID)
	require.NoError(t, err)
	assert.False(t, updated.Disabled)
	armed := updated.SubjectRebindArmedAt
	require.NotNil(t, armed, "a real disabled→enabled transition opens the rebind window")
	assert.WithinDuration(t, before, *armed, 5*time.Second)
	assert.Equal(t, time.UTC, armed.Location(), "stored in UTC like every other timestamp on the record")
	assert.Equal(t, "sub-user-rebind", updated.ProviderSubjectID, "enable does not clear the binding — the next login does")

	// A second enable on an already-enabled user is not a transition: it
	// neither re-arms (no fresh timestamp) nor closes the window.
	time.Sleep(5 * time.Millisecond)
	w = postAdmin(t, router, "/api/v1/admin/users/user-rebind/enable")
	require.Equal(t, http.StatusOK, w.Code)
	again := rebindArmedAt(t, store, user.ID)
	require.NotNil(t, again, "a no-op enable must not close the window")
	assert.True(t, again.Equal(*armed), "a no-op enable must not re-arm: %v != %v", again, armed)
}

func TestEnableUser_AlreadyEnabledDoesNotArm(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	user := createTestUser(t, store, "user-noop", "noop@example.com", "No Op", "google")

	w := postAdmin(t, router, "/api/v1/admin/users/user-noop/enable")
	require.Equal(t, http.StatusOK, w.Code)

	assert.Nil(t, rebindArmedAt(t, store, user.ID), "enable on an enabled user is not a transition and must not arm")
}

func TestDisableUser_ClearsSubjectRebindWindow(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	user := createTestUser(t, store, "user-close", "close@example.com", "Close Me", "google")
	armedAt := time.Now().UTC()
	user.SubjectRebindArmedAt = &armedAt
	require.NoError(t, store.UpdateUser(user))
	require.NotNil(t, rebindArmedAt(t, store, user.ID), "precondition: armed")

	w := postAdmin(t, router, "/api/v1/admin/users/user-close/disable")
	require.Equal(t, http.StatusOK, w.Code)

	updated, err := store.GetUser(user.ID)
	require.NoError(t, err)
	assert.True(t, updated.Disabled)
	assert.Nil(t, updated.SubjectRebindArmedAt, "disable closes an open window")
	assert.Equal(t, "sub-user-close", updated.ProviderSubjectID, "disable does not clear the binding (FR-023)")
}

func TestDisableEnableCycle_ArmsExactlyOnce(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	user := createTestUser(t, store, "user-cycle", "cycle@example.com", "Cycle", "google")

	require.Equal(t, http.StatusOK, postAdmin(t, router, "/api/v1/admin/users/user-cycle/disable").Code)
	assert.Nil(t, rebindArmedAt(t, store, user.ID), "disable never arms")

	require.Equal(t, http.StatusOK, postAdmin(t, router, "/api/v1/admin/users/user-cycle/enable").Code)
	first := rebindArmedAt(t, store, user.ID)
	require.NotNil(t, first)

	// disable → enable again: a new real transition re-arms with a new time.
	time.Sleep(5 * time.Millisecond)
	require.Equal(t, http.StatusOK, postAdmin(t, router, "/api/v1/admin/users/user-cycle/disable").Code)
	assert.Nil(t, rebindArmedAt(t, store, user.ID))
	require.Equal(t, http.StatusOK, postAdmin(t, router, "/api/v1/admin/users/user-cycle/enable").Code)
	second := rebindArmedAt(t, store, user.ID)
	require.NotNil(t, second)
	assert.True(t, second.After(*first), "a fresh transition carries a fresh timestamp")
}

// GET /admin/users carries the additive fields of rest-endpoints.md §4:
// `groups: string[]`, `groups_updated_at: string|null`,
// `subject_rebind_armed_at: string|null`. Decoded as raw JSON so the wire
// shape — not a Go field name — is what is pinned.
func TestAdminListUsers_ExposesGroupsAndRebindWindow(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	armedAt := time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC)
	groupsAt := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)

	armed := createTestUser(t, store, "user-armed", "armed@example.com", "Armed", "google")
	armed.Groups = []string{"eng", "ops"}
	armed.GroupsUpdatedAt = groupsAt
	armed.SubjectRebindArmedAt = &armedAt
	require.NoError(t, store.UpdateUser(armed))

	// A record that has never logged in since the upgrade: no groups, no
	// window — decoded from JSON as the zero values.
	createTestUser(t, store, "user-plain", "plain@example.com", "Plain", "github")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.Len(t, rows, 2)

	byID := map[string]map[string]json.RawMessage{}
	for _, row := range rows {
		var id string
		require.NoError(t, json.Unmarshal(row["id"], &id))
		byID[id] = row
	}

	armedRow := byID["user-armed"]
	require.NotNil(t, armedRow)
	for _, key := range []string{"groups", "groups_updated_at", "subject_rebind_armed_at"} {
		require.Contains(t, armedRow, key, "GET /admin/users must carry %q", key)
	}
	var groups []string
	require.NoError(t, json.Unmarshal(armedRow["groups"], &groups))
	assert.Equal(t, []string{"eng", "ops"}, groups)
	var groupsUpdatedAt, rebindArmedAtWire string
	require.NoError(t, json.Unmarshal(armedRow["groups_updated_at"], &groupsUpdatedAt))
	require.NoError(t, json.Unmarshal(armedRow["subject_rebind_armed_at"], &rebindArmedAtWire))
	parsedGroupsAt, err := time.Parse(time.RFC3339, groupsUpdatedAt)
	require.NoError(t, err, "groups_updated_at is RFC 3339: %q", groupsUpdatedAt)
	assert.True(t, parsedGroupsAt.Equal(groupsAt))
	parsedArmedAt, err := time.Parse(time.RFC3339, rebindArmedAtWire)
	require.NoError(t, err, "subject_rebind_armed_at is RFC 3339: %q", rebindArmedAtWire)
	assert.True(t, parsedArmedAt.Equal(armedAt))

	plainRow := byID["user-plain"]
	require.NotNil(t, plainRow)
	for _, key := range []string{"groups", "groups_updated_at", "subject_rebind_armed_at"} {
		require.Contains(t, plainRow, key, "the keys are present on every row, never dropped for zero values")
	}
	var plainGroups []string
	require.NoError(t, json.Unmarshal(plainRow["groups"], &plainGroups))
	assert.NotNil(t, plainGroups, "groups is always an array (`string[]`), never null")
	assert.Empty(t, plainGroups)
	assert.Equal(t, "null", string(plainRow["groups_updated_at"]), "zero groups_updated_at is null on the wire")
	assert.Equal(t, "null", string(plainRow["subject_rebind_armed_at"]), "a closed window is null on the wire")
}
