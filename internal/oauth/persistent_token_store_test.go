package oauth

import (
	"context"
	"testing"
	"time"

	"mcpproxy-go/internal/storage"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"
)

func TestPersistentTokenStore(t *testing.T) {
	// Create a temporary directory for test database
	tmpDir := t.TempDir()

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create storage
	db, err := storage.NewBoltDB(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create BoltDB: %v", err)
	}
	defer db.Close()

	// Create token store
	tokenStore := NewPersistentTokenStore("test-server", "https://test.example.com/mcp", db)

	// Test case 1: Get non-existent token
	_, err = tokenStore.GetToken(context.Background())
	if err == nil {
		t.Error("Expected error when getting non-existent token")
	}

	// Test case 2: Save and retrieve token
	originalToken := &client.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		Scope:        "mcp.read mcp.write",
	}

	err = tokenStore.SaveToken(context.Background(), originalToken)
	if err != nil {
		t.Fatalf("Failed to save token: %v", err)
	}

	// Test case 3: Retrieve the saved token
	retrievedToken, err := tokenStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}

	// Verify token fields
	if retrievedToken.AccessToken != originalToken.AccessToken {
		t.Errorf("AccessToken mismatch: got %s, want %s", retrievedToken.AccessToken, originalToken.AccessToken)
	}
	if retrievedToken.RefreshToken != originalToken.RefreshToken {
		t.Errorf("RefreshToken mismatch: got %s, want %s", retrievedToken.RefreshToken, originalToken.RefreshToken)
	}
	if retrievedToken.TokenType != originalToken.TokenType {
		t.Errorf("TokenType mismatch: got %s, want %s", retrievedToken.TokenType, originalToken.TokenType)
	}
	if retrievedToken.Scope != originalToken.Scope {
		t.Errorf("Scope mismatch: got %s, want %s", retrievedToken.Scope, originalToken.Scope)
	}
	// ExpiresAt should be adjusted by grace period for proactive refresh
	expectedExpiresAt := originalToken.ExpiresAt.Add(-TokenRefreshGracePeriod)
	if retrievedToken.ExpiresAt.Unix() != expectedExpiresAt.Unix() {
		t.Errorf("ExpiresAt mismatch (should be adjusted by grace period): got %v, want %v", retrievedToken.ExpiresAt, expectedExpiresAt)
	}

	// Test case 4: Update token
	updatedToken := &client.Token{
		AccessToken:  "updated-access-token",
		RefreshToken: "updated-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(2 * time.Hour),
		Scope:        "mcp.read mcp.write admin",
	}

	err = tokenStore.SaveToken(context.Background(), updatedToken)
	if err != nil {
		t.Fatalf("Failed to save updated token: %v", err)
	}

	retrievedUpdatedToken, err := tokenStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get updated token: %v", err)
	}

	if retrievedUpdatedToken.AccessToken != updatedToken.AccessToken {
		t.Errorf("Updated AccessToken mismatch: got %s, want %s", retrievedUpdatedToken.AccessToken, updatedToken.AccessToken)
	}

	// Test case 5: Clear token using the direct method
	persistentTokenStore := tokenStore.(*PersistentTokenStore)
	err = persistentTokenStore.ClearToken()
	if err != nil {
		t.Fatalf("Failed to clear token: %v", err)
	}

	_, err = tokenStore.GetToken(context.Background())
	if err == nil {
		t.Error("Expected error when getting cleared token")
	}

	// Test case 6: Expired token detection
	expiredToken := &client.Token{
		AccessToken:  "expired-token",
		RefreshToken: "expired-refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour), // Expired
		Scope:        "mcp.read",
	}

	err = tokenStore.SaveToken(context.Background(), expiredToken)
	if err != nil {
		t.Fatalf("Failed to save expired token: %v", err)
	}

	retrievedExpiredToken, err := tokenStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get expired token: %v", err)
	}

	if retrievedExpiredToken.IsExpired() != true {
		t.Error("Expected token to be expired")
	}
}

func TestPersistentTokenStoreMultipleServers(t *testing.T) {
	// Create a temporary directory for test database
	tmpDir := t.TempDir()

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create storage
	db, err := storage.NewBoltDB(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create BoltDB: %v", err)
	}
	defer db.Close()

	// Create token stores for different servers
	tokenStore1 := NewPersistentTokenStore("server1", "https://server1.example.com/mcp", db)
	tokenStore2 := NewPersistentTokenStore("server2", "https://server2.example.com/mcp", db)

	// Save tokens for both servers
	token1 := &client.Token{
		AccessToken: "server1-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		Scope:       "mcp.read",
	}

	token2 := &client.Token{
		AccessToken: "server2-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		Scope:       "mcp.write",
	}

	err = tokenStore1.SaveToken(context.Background(), token1)
	if err != nil {
		t.Fatalf("Failed to save token1: %v", err)
	}

	err = tokenStore2.SaveToken(context.Background(), token2)
	if err != nil {
		t.Fatalf("Failed to save token2: %v", err)
	}

	// Retrieve tokens and verify they are separate
	retrievedToken1, err := tokenStore1.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get token1: %v", err)
	}

	retrievedToken2, err := tokenStore2.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get token2: %v", err)
	}

	if retrievedToken1.AccessToken != "server1-token" {
		t.Errorf("Server1 token mismatch: got %s, want server1-token", retrievedToken1.AccessToken)
	}

	if retrievedToken2.AccessToken != "server2-token" {
		t.Errorf("Server2 token mismatch: got %s, want server2-token", retrievedToken2.AccessToken)
	}

	if retrievedToken1.Scope != "mcp.read" {
		t.Errorf("Server1 scope mismatch: got %s, want mcp.read", retrievedToken1.Scope)
	}

	if retrievedToken2.Scope != "mcp.write" {
		t.Errorf("Server2 scope mismatch: got %s, want mcp.write", retrievedToken2.Scope)
	}
}

// TestPersistentTokenStoreShortLivedToken verifies that short-lived tokens
// (with lifetime shorter than the grace period) don't get their expiration
// adjusted, preventing them from appearing expired immediately after creation.
func TestPersistentTokenStoreShortLivedToken(t *testing.T) {
	// Create a temporary directory for test database
	tmpDir := t.TempDir()

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create storage
	db, err := storage.NewBoltDB(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create BoltDB: %v", err)
	}
	defer db.Close()

	// Create token store
	tokenStore := NewPersistentTokenStore("test-server", "https://test.example.com/mcp", db)

	// Test case 1: Short-lived token (30 seconds - much less than 5 minute grace period)
	// This token should NOT have grace period applied, as it would make it appear expired
	shortLivedToken := &client.Token{
		AccessToken:  "short-lived-access-token",
		RefreshToken: "short-lived-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(30 * time.Second), // 30 seconds - less than grace period
		Scope:        "mcp.read",
	}

	err = tokenStore.SaveToken(context.Background(), shortLivedToken)
	if err != nil {
		t.Fatalf("Failed to save short-lived token: %v", err)
	}

	retrievedToken, err := tokenStore.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get short-lived token: %v", err)
	}

	// For short-lived tokens, ExpiresAt should NOT be adjusted by grace period
	// It should be the same as the original (within 1 second tolerance for test timing)
	timeDiff := retrievedToken.ExpiresAt.Sub(shortLivedToken.ExpiresAt).Abs()
	if timeDiff > 1*time.Second {
		t.Errorf("Short-lived token ExpiresAt should not be adjusted: got %v, want approximately %v (diff: %v)",
			retrievedToken.ExpiresAt, shortLivedToken.ExpiresAt, timeDiff)
	}

	// Most importantly, the token should NOT appear expired immediately
	if retrievedToken.IsExpired() {
		t.Error("Short-lived token should NOT appear expired immediately after creation")
	}

	// Test case 2: Long-lived token (1 hour - more than 5 minute grace period)
	// This token SHOULD have grace period applied for proactive refresh
	longLivedToken := &client.Token{
		AccessToken:  "long-lived-access-token",
		RefreshToken: "long-lived-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour), // 1 hour - more than grace period
		Scope:        "mcp.read",
	}

	// Need a separate store to avoid key collision
	tokenStore2 := NewPersistentTokenStore("test-server-long", "https://test.example.com/mcp", db)

	err = tokenStore2.SaveToken(context.Background(), longLivedToken)
	if err != nil {
		t.Fatalf("Failed to save long-lived token: %v", err)
	}

	retrievedLongToken, err := tokenStore2.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get long-lived token: %v", err)
	}

	// For long-lived tokens, ExpiresAt SHOULD be adjusted by grace period
	expectedAdjustedExpiresAt := longLivedToken.ExpiresAt.Add(-TokenRefreshGracePeriod)
	timeDiff = retrievedLongToken.ExpiresAt.Sub(expectedAdjustedExpiresAt).Abs()
	if timeDiff > 1*time.Second {
		t.Errorf("Long-lived token ExpiresAt should be adjusted by grace period: got %v, want approximately %v (diff: %v)",
			retrievedLongToken.ExpiresAt, expectedAdjustedExpiresAt, timeDiff)
	}

	// Test case 3: Token with exactly grace period lifetime
	// Edge case: token with exactly 5 minutes should NOT get adjusted (would make it appear expired)
	edgeCaseToken := &client.Token{
		AccessToken:  "edge-case-token",
		RefreshToken: "edge-case-refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(TokenRefreshGracePeriod), // Exactly 5 minutes
		Scope:        "mcp.read",
	}

	tokenStore3 := NewPersistentTokenStore("test-server-edge", "https://test.example.com/mcp", db)

	err = tokenStore3.SaveToken(context.Background(), edgeCaseToken)
	if err != nil {
		t.Fatalf("Failed to save edge case token: %v", err)
	}

	retrievedEdgeToken, err := tokenStore3.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get edge case token: %v", err)
	}

	// Edge case token should NOT be adjusted (timeUntilExpiry <= grace period)
	timeDiff = retrievedEdgeToken.ExpiresAt.Sub(edgeCaseToken.ExpiresAt).Abs()
	if timeDiff > 1*time.Second {
		t.Errorf("Edge case token ExpiresAt should not be adjusted: got %v, want approximately %v (diff: %v)",
			retrievedEdgeToken.ExpiresAt, edgeCaseToken.ExpiresAt, timeDiff)
	}

	// The edge case token should NOT appear expired
	if retrievedEdgeToken.IsExpired() {
		t.Error("Edge case token should NOT appear expired")
	}
}

func TestPersistentTokenStoreSameNameDifferentURL(t *testing.T) {
	// Create a temporary directory for test database
	tmpDir := t.TempDir()

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create storage
	db, err := storage.NewBoltDB(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create BoltDB: %v", err)
	}
	defer db.Close()

	// Create token stores for servers with same name but different URLs
	tokenStore1 := NewPersistentTokenStore("myserver", "https://server1.example.com/mcp", db)
	tokenStore2 := NewPersistentTokenStore("myserver", "https://server2.example.com/mcp", db)

	// Save tokens for both servers
	token1 := &client.Token{
		AccessToken: "token-for-server1-url",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		Scope:       "mcp.read",
	}

	token2 := &client.Token{
		AccessToken: "token-for-server2-url",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		Scope:       "mcp.write",
	}

	err = tokenStore1.SaveToken(context.Background(), token1)
	if err != nil {
		t.Fatalf("Failed to save token1: %v", err)
	}

	err = tokenStore2.SaveToken(context.Background(), token2)
	if err != nil {
		t.Fatalf("Failed to save token2: %v", err)
	}

	// Retrieve tokens and verify they are separate despite same server name
	retrievedToken1, err := tokenStore1.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get token1: %v", err)
	}

	retrievedToken2, err := tokenStore2.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get token2: %v", err)
	}

	// Verify tokens are different
	if retrievedToken1.AccessToken != "token-for-server1-url" {
		t.Errorf("Server1 token mismatch: got %s, want token-for-server1-url", retrievedToken1.AccessToken)
	}

	if retrievedToken2.AccessToken != "token-for-server2-url" {
		t.Errorf("Server2 token mismatch: got %s, want token-for-server2-url", retrievedToken2.AccessToken)
	}

	if retrievedToken1.Scope != "mcp.read" {
		t.Errorf("Server1 scope mismatch: got %s, want mcp.read", retrievedToken1.Scope)
	}

	if retrievedToken2.Scope != "mcp.write" {
		t.Errorf("Server2 scope mismatch: got %s, want mcp.write", retrievedToken2.Scope)
	}

	// Verify clearing one doesn't affect the other
	persistentTokenStore1 := tokenStore1.(*PersistentTokenStore)
	err = persistentTokenStore1.ClearToken()
	if err != nil {
		t.Fatalf("Failed to clear token1: %v", err)
	}

	// Token 1 should be gone
	_, err = tokenStore1.GetToken(context.Background())
	if err == nil {
		t.Error("Expected error when getting cleared token1")
	}

	// Token 2 should still exist
	retrievedToken2Again, err := tokenStore2.GetToken(context.Background())
	if err != nil {
		t.Fatalf("Failed to get token2 after clearing token1: %v", err)
	}

	if retrievedToken2Again.AccessToken != "token-for-server2-url" {
		t.Errorf("Server2 token should still exist: got %s, want token-for-server2-url", retrievedToken2Again.AccessToken)
	}
}
