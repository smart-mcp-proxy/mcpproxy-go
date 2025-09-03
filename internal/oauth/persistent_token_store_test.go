package oauth

import (
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
	_, err = tokenStore.GetToken()
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

	err = tokenStore.SaveToken(originalToken)
	if err != nil {
		t.Fatalf("Failed to save token: %v", err)
	}

	// Test case 3: Retrieve the saved token
	retrievedToken, err := tokenStore.GetToken()
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
	if retrievedToken.ExpiresAt.Unix() != originalToken.ExpiresAt.Unix() {
		t.Errorf("ExpiresAt mismatch: got %v, want %v", retrievedToken.ExpiresAt, originalToken.ExpiresAt)
	}

	// Test case 4: Update token
	updatedToken := &client.Token{
		AccessToken:  "updated-access-token",
		RefreshToken: "updated-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(2 * time.Hour),
		Scope:        "mcp.read mcp.write admin",
	}

	err = tokenStore.SaveToken(updatedToken)
	if err != nil {
		t.Fatalf("Failed to save updated token: %v", err)
	}

	retrievedUpdatedToken, err := tokenStore.GetToken()
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

	_, err = tokenStore.GetToken()
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

	err = tokenStore.SaveToken(expiredToken)
	if err != nil {
		t.Fatalf("Failed to save expired token: %v", err)
	}

	retrievedExpiredToken, err := tokenStore.GetToken()
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

	err = tokenStore1.SaveToken(token1)
	if err != nil {
		t.Fatalf("Failed to save token1: %v", err)
	}

	err = tokenStore2.SaveToken(token2)
	if err != nil {
		t.Fatalf("Failed to save token2: %v", err)
	}

	// Retrieve tokens and verify they are separate
	retrievedToken1, err := tokenStore1.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token1: %v", err)
	}

	retrievedToken2, err := tokenStore2.GetToken()
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

	err = tokenStore1.SaveToken(token1)
	if err != nil {
		t.Fatalf("Failed to save token1: %v", err)
	}

	err = tokenStore2.SaveToken(token2)
	if err != nil {
		t.Fatalf("Failed to save token2: %v", err)
	}

	// Retrieve tokens and verify they are separate despite same server name
	retrievedToken1, err := tokenStore1.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token1: %v", err)
	}

	retrievedToken2, err := tokenStore2.GetToken()
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
	_, err = tokenStore1.GetToken()
	if err == nil {
		t.Error("Expected error when getting cleared token1")
	}

	// Token 2 should still exist
	retrievedToken2Again, err := tokenStore2.GetToken()
	if err != nil {
		t.Fatalf("Failed to get token2 after clearing token1: %v", err)
	}

	if retrievedToken2Again.AccessToken != "token-for-server2-url" {
		t.Errorf("Server2 token should still exist: got %s, want token-for-server2-url", retrievedToken2Again.AccessToken)
	}
}
