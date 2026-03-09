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

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

func TestIntegration_AdminViewsAllUsersActivity(t *testing.T) {
	now := time.Now().UTC()

	// Create mock activity records from multiple users:
	//   - 3 from user A
	//   - 2 from user B
	//   - 1 from user C
	records := []*storage.ActivityRecord{
		{
			ID:         "act-a1",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-a",
			UserEmail:  "a@example.com",
			ServerName: "github",
			ToolName:   "list_repos",
			Timestamp:  now,
		},
		{
			ID:         "act-a2",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-a",
			UserEmail:  "a@example.com",
			ServerName: "github",
			ToolName:   "create_issue",
			Timestamp:  now.Add(-1 * time.Minute),
		},
		{
			ID:         "act-b1",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-b",
			UserEmail:  "b@example.com",
			ServerName: "gitlab",
			ToolName:   "create_mr",
			Timestamp:  now.Add(-2 * time.Minute),
		},
		{
			ID:         "act-a3",
			Type:       storage.ActivityTypeToolCall,
			Status:     "error",
			UserID:     "user-a",
			UserEmail:  "a@example.com",
			ServerName: "github",
			ToolName:   "delete_repo",
			Timestamp:  now.Add(-3 * time.Minute),
		},
		{
			ID:         "act-b2",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-b",
			UserEmail:  "b@example.com",
			ServerName: "gitlab",
			ToolName:   "list_pipelines",
			Timestamp:  now.Add(-4 * time.Minute),
		},
		{
			ID:         "act-c1",
			Type:       storage.ActivityTypeToolCall,
			Status:     "success",
			UserID:     "user-c",
			UserEmail:  "c@example.com",
			ServerName: "slack",
			ToolName:   "send_message",
			Timestamp:  now.Add(-5 * time.Minute),
		},
	}

	handlers, _ := adminTestSetup(t, records)
	router := adminTestRouter(handlers, adminAuthContext())

	// Admin requests all activity
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 6, resp.Total, "Admin should see all 6 records")

	// Admin filters by user A
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity?user_id=user-a", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respA ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respA))
	assert.Equal(t, 3, respA.Total, "User A has 3 records")

	// Admin filters by user B
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity?user_id=user-b", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respB ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respB))
	assert.Equal(t, 2, respB.Total, "User B has 2 records")

	// Admin filters by user C
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity?user_id=user-c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respC ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respC))
	assert.Equal(t, 1, respC.Total, "User C has 1 record")

	// Admin filters by nonexistent user
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity?user_id=user-nobody", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var respNobody ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respNobody))
	assert.Equal(t, 0, respNobody.Total, "Nonexistent user has 0 records")
}

func TestIntegration_AdminViewsActivity_NonAdminForbidden(t *testing.T) {
	records := []*storage.ActivityRecord{
		{
			ID:        "act-1",
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			UserID:    "user-a",
			Timestamp: time.Now().UTC(),
		},
	}

	handlers, _ := adminTestSetup(t, records)

	// Non-admin user tries to access admin activity
	userCtx := auth.UserContext("user-regular", "regular@example.com", "Regular User", "google")
	router := adminTestRouter(handlers, userCtx)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestIntegration_AdminDisableUserRevokesAccess(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	// Create a user
	user := createTestUser(t, store, "user-to-disable", "disable-me@example.com", "DisableMe", "google")

	// Create a session for the user
	session := users.NewSession(user.ID, 24*time.Hour)
	session.UserAgent = "TestBrowser/1.0"
	session.IPAddress = "10.0.0.1"
	require.NoError(t, store.CreateSession(session))

	// Create a second session for the same user
	session2 := users.NewSession(user.ID, 24*time.Hour)
	session2.UserAgent = "TestBrowser/2.0"
	session2.IPAddress = "10.0.0.2"
	require.NoError(t, store.CreateSession(session2))

	// Verify both sessions exist
	s1, err := store.GetSession(session.ID)
	require.NoError(t, err)
	require.NotNil(t, s1, "session 1 should exist before disable")

	s2, err := store.GetSession(session2.ID)
	require.NoError(t, err)
	require.NotNil(t, s2, "session 2 should exist before disable")

	// Admin disables the user
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/user-to-disable/disable", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify: user is disabled
	updatedUser, err := store.GetUser("user-to-disable")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)
	assert.True(t, updatedUser.Disabled, "user should be disabled")

	// Verify: all sessions revoked
	s1After, err := store.GetSession(session.ID)
	require.NoError(t, err)
	assert.Nil(t, s1After, "session 1 should be deleted after disable")

	s2After, err := store.GetSession(session2.ID)
	require.NoError(t, err)
	assert.Nil(t, s2After, "session 2 should be deleted after disable")

	// Verify: re-enabling the user works
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/user-to-disable/enable", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	enabledUser, err := store.GetUser("user-to-disable")
	require.NoError(t, err)
	require.NotNil(t, enabledUser)
	assert.False(t, enabledUser.Disabled, "user should be re-enabled")

	// Sessions are still gone (re-enabling doesn't restore sessions)
	s1Final, err := store.GetSession(session.ID)
	require.NoError(t, err)
	assert.Nil(t, s1Final, "sessions remain deleted after re-enable")
}

func TestIntegration_AdminDisableUser_OtherUserSessionsUnaffected(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	// Create two users
	userA := createTestUser(t, store, "user-target", "target@example.com", "Target", "google")
	createTestUser(t, store, "user-bystander", "bystander@example.com", "Bystander", "github")

	// Create sessions for both
	sessionA := users.NewSession(userA.ID, 24*time.Hour)
	require.NoError(t, store.CreateSession(sessionA))

	sessionBystander := users.NewSession("user-bystander", 24*time.Hour)
	require.NoError(t, store.CreateSession(sessionBystander))

	// Disable user A
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/user-target/disable", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// User A's session should be revoked
	sA, err := store.GetSession(sessionA.ID)
	require.NoError(t, err)
	assert.Nil(t, sA, "target user's session should be deleted")

	// Bystander's session should still exist
	sBystander, err := store.GetSession(sessionBystander.ID)
	require.NoError(t, err)
	assert.NotNil(t, sBystander, "bystander's session should be unaffected")
}

func TestIntegration_AdminListUsersWithMixedStates(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	// Create users in various states
	createTestUser(t, store, "user-active-1", "active1@example.com", "Active1", "google")
	createTestUser(t, store, "user-active-2", "active2@example.com", "Active2", "github")

	disabledUser := createTestUser(t, store, "user-disabled", "disabled@example.com", "Disabled", "microsoft")
	disabledUser.Disabled = true
	require.NoError(t, store.UpdateUser(disabledUser))

	// List all users
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var profiles []*UserProfileResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &profiles))
	assert.Len(t, profiles, 3)

	// Verify disabled state is reflected
	disabledCount := 0
	enabledCount := 0
	for _, p := range profiles {
		if p.Disabled {
			disabledCount++
			assert.Equal(t, "disabled@example.com", p.Email)
		} else {
			enabledCount++
		}
	}
	assert.Equal(t, 1, disabledCount)
	assert.Equal(t, 2, enabledCount)
}

func TestIntegration_AdminSessionsAcrossUsers(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	// Create users with sessions
	userA := createTestUser(t, store, "sess-user-a", "sess-a@example.com", "SessionA", "google")
	userB := createTestUser(t, store, "sess-user-b", "sess-b@example.com", "SessionB", "github")

	// User A has 2 sessions, User B has 1 session
	sessA1 := users.NewSession(userA.ID, 24*time.Hour)
	sessA1.UserAgent = "Chrome"
	require.NoError(t, store.CreateSession(sessA1))

	sessA2 := users.NewSession(userA.ID, 24*time.Hour)
	sessA2.UserAgent = "Firefox"
	require.NoError(t, store.CreateSession(sessA2))

	sessB1 := users.NewSession(userB.ID, 24*time.Hour)
	sessB1.UserAgent = "Safari"
	require.NoError(t, store.CreateSession(sessB1))

	// List all sessions
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sessions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var sessions []*SessionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sessions))
	assert.Len(t, sessions, 3)

	// Verify user attribution
	userASessions := 0
	userBSessions := 0
	for _, s := range sessions {
		switch s.UserID {
		case userA.ID:
			userASessions++
			assert.Equal(t, "sess-a@example.com", s.UserEmail)
		case userB.ID:
			userBSessions++
			assert.Equal(t, "sess-b@example.com", s.UserEmail)
		}
		assert.False(t, s.Expired)
	}
	assert.Equal(t, 2, userASessions, "User A should have 2 sessions")
	assert.Equal(t, 1, userBSessions, "User B should have 1 session")
}
