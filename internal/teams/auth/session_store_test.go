//go:build teams

package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func setupTestSessionManager(t *testing.T, ttl time.Duration, secure bool) *SessionManager {
	t.Helper()
	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	return NewSessionManager(store, ttl, secure)
}

func TestSessionManager_CreateSession(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("User-Agent", "TestBrowser/1.0")
	req.RemoteAddr = "192.168.1.100:54321"

	session, err := mgr.CreateSession("user-123", req)
	require.NoError(t, err)
	require.NotNil(t, session)

	assert.Equal(t, "user-123", session.UserID)
	assert.Equal(t, "TestBrowser/1.0", session.UserAgent)
	assert.Equal(t, "192.168.1.100", session.IPAddress)
	assert.NotEmpty(t, session.ID)
	assert.False(t, session.ExpiresAt.IsZero())
	assert.WithinDuration(t, time.Now().Add(time.Hour), session.ExpiresAt, 5*time.Second)
}

func TestSessionManager_CreateSession_XForwardedFor(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	req.RemoteAddr = "127.0.0.1:8080"

	session, err := mgr.CreateSession("user-xff", req)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", session.IPAddress)
}

func TestSessionManager_CreateSession_XRealIP(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "172.16.0.5")
	req.RemoteAddr = "127.0.0.1:8080"

	session, err := mgr.CreateSession("user-xri", req)
	require.NoError(t, err)
	assert.Equal(t, "172.16.0.5", session.IPAddress)
}

func TestSessionManager_SetAndGetSessionCookie(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	// Create a session
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := mgr.CreateSession("user-cookie", req)
	require.NoError(t, err)

	// Set cookie on response
	recorder := httptest.NewRecorder()
	mgr.SetSessionCookie(recorder, session)

	// Extract cookies from response and build a new request with them
	resp := recorder.Result()
	defer resp.Body.Close()
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, SessionCookieName, cookies[0].Name)
	assert.Equal(t, session.ID, cookies[0].Value)

	// Build a new request with the session cookie
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookies[0])

	// Retrieve session from the new request
	got, err := mgr.GetSessionFromRequest(req2)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, session.ID, got.ID)
	assert.Equal(t, "user-cookie", got.UserID)
}

func TestSessionManager_GetSession_NoCookie(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No cookie set on request

	session, err := mgr.GetSessionFromRequest(req)
	require.NoError(t, err)
	assert.Nil(t, session)
}

func TestSessionManager_GetSession_InvalidSession(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "nonexistent-session-id",
	})

	session, err := mgr.GetSessionFromRequest(req)
	require.NoError(t, err)
	assert.Nil(t, session)
}

func TestSessionManager_GetSession_Expired(t *testing.T) {
	mgr := setupTestSessionManager(t, 1*time.Millisecond, false)

	// Create a session with very short TTL
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := mgr.CreateSession("user-expired", req)
	require.NoError(t, err)
	require.NotNil(t, session)

	// Wait for it to expire
	time.Sleep(10 * time.Millisecond)

	// Try to retrieve it
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})

	got, err := mgr.GetSessionFromRequest(req2)
	require.NoError(t, err)
	assert.Nil(t, got, "expired session should return nil")
}

func TestSessionManager_RevokeSession(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	// Create a session
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := mgr.CreateSession("user-revoke", req)
	require.NoError(t, err)

	// Verify it's retrievable
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})

	got, err := mgr.GetSessionFromRequest(req2)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Revoke it
	err = mgr.RevokeSession(session.ID)
	require.NoError(t, err)

	// Should no longer be retrievable
	got, err = mgr.GetSessionFromRequest(req2)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionManager_RevokeUserSessions(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	// Create multiple sessions for the same user
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	s1, err := mgr.CreateSession("user-multi", req)
	require.NoError(t, err)
	s2, err := mgr.CreateSession("user-multi", req)
	require.NoError(t, err)

	// Create a session for a different user
	s3, err := mgr.CreateSession("user-other", req)
	require.NoError(t, err)

	// Revoke all sessions for user-multi
	err = mgr.RevokeUserSessions("user-multi")
	require.NoError(t, err)

	// Both user-multi sessions should be gone
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.AddCookie(&http.Cookie{Name: SessionCookieName, Value: s1.ID})
	got, err := mgr.GetSessionFromRequest(req1)
	require.NoError(t, err)
	assert.Nil(t, got, "s1 should be revoked")

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: SessionCookieName, Value: s2.ID})
	got, err = mgr.GetSessionFromRequest(req2)
	require.NoError(t, err)
	assert.Nil(t, got, "s2 should be revoked")

	// user-other session should still exist
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.AddCookie(&http.Cookie{Name: SessionCookieName, Value: s3.ID})
	got, err = mgr.GetSessionFromRequest(req3)
	require.NoError(t, err)
	require.NotNil(t, got, "s3 should still exist")
	assert.Equal(t, "user-other", got.UserID)
}

func TestSessionManager_ClearSessionCookie(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	recorder := httptest.NewRecorder()
	mgr.ClearSessionCookie(recorder)

	resp := recorder.Result()
	defer resp.Body.Close()
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)

	cookie := cookies[0]
	assert.Equal(t, SessionCookieName, cookie.Name)
	assert.Equal(t, "", cookie.Value)
	assert.Equal(t, -1, cookie.MaxAge)
}

func TestSessionManager_CookieAttributes(t *testing.T) {
	t.Run("insecure mode", func(t *testing.T) {
		mgr := setupTestSessionManager(t, 2*time.Hour, false)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		session, err := mgr.CreateSession("user-attrs", req)
		require.NoError(t, err)

		recorder := httptest.NewRecorder()
		mgr.SetSessionCookie(recorder, session)

		resp := recorder.Result()
		defer resp.Body.Close()
		cookies := resp.Cookies()
		require.Len(t, cookies, 1)

		cookie := cookies[0]
		assert.Equal(t, SessionCookieName, cookie.Name)
		assert.Equal(t, "/", cookie.Path)
		assert.True(t, cookie.HttpOnly, "cookie should be HttpOnly")
		assert.False(t, cookie.Secure, "cookie should not be Secure in insecure mode")
		assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
		assert.Equal(t, int(2*time.Hour/time.Second), cookie.MaxAge)
	})

	t.Run("secure mode", func(t *testing.T) {
		mgr := setupTestSessionManager(t, time.Hour, true)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		session, err := mgr.CreateSession("user-secure", req)
		require.NoError(t, err)

		recorder := httptest.NewRecorder()
		mgr.SetSessionCookie(recorder, session)

		resp := recorder.Result()
		defer resp.Body.Close()
		cookies := resp.Cookies()
		require.Len(t, cookies, 1)

		cookie := cookies[0]
		assert.True(t, cookie.HttpOnly, "cookie should be HttpOnly")
		assert.True(t, cookie.Secure, "cookie should be Secure in secure mode")
		assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	})
}

func TestSessionManager_CleanupExpired(t *testing.T) {
	mgr := setupTestSessionManager(t, 1*time.Millisecond, false)

	// Create sessions that will expire quickly
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := mgr.CreateSession("user-cleanup-1", req)
	require.NoError(t, err)
	_, err = mgr.CreateSession("user-cleanup-2", req)
	require.NoError(t, err)

	// Wait for them to expire
	time.Sleep(10 * time.Millisecond)

	// Create one active session with a fresh manager (longer TTL)
	mgr2 := NewSessionManager(mgr.store, time.Hour, false)
	_, err = mgr2.CreateSession("user-active", req)
	require.NoError(t, err)

	// Cleanup should remove the two expired sessions
	count, err := mgr.CleanupExpired()
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Second cleanup should find nothing
	count, err = mgr.CleanupExpired()
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestSessionManager_GetSession_EmptyCookieValue(t *testing.T) {
	mgr := setupTestSessionManager(t, time.Hour, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: "",
	})

	session, err := mgr.GetSessionFromRequest(req)
	require.NoError(t, err)
	assert.Nil(t, session)
}
