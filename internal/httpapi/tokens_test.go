package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// --- Mock token store ---

type mockTokenStore struct {
	tokens     map[string]auth.AgentToken
	createErr  error
	revokeErr  error
	regenToken *auth.AgentToken
	regenErr   error
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{
		tokens: make(map[string]auth.AgentToken),
	}
}

func (m *mockTokenStore) CreateAgentToken(token auth.AgentToken, _ string, _ []byte) error {
	if m.createErr != nil {
		return m.createErr
	}
	if _, exists := m.tokens[token.Name]; exists {
		return fmt.Errorf("agent token with name %q already exists", token.Name)
	}
	token.TokenPrefix = "mcp_agt_test"
	m.tokens[token.Name] = token
	return nil
}

func (m *mockTokenStore) ListAgentTokens() ([]auth.AgentToken, error) {
	result := make([]auth.AgentToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		result = append(result, t)
	}
	return result, nil
}

func (m *mockTokenStore) GetAgentTokenByName(name string) (*auth.AgentToken, error) {
	t, ok := m.tokens[name]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (m *mockTokenStore) RevokeAgentToken(name string) error {
	if m.revokeErr != nil {
		return m.revokeErr
	}
	t, ok := m.tokens[name]
	if !ok {
		return fmt.Errorf("agent token %q not found", name)
	}
	t.Revoked = true
	m.tokens[name] = t
	return nil
}

func (m *mockTokenStore) ValidateAgentToken(rawToken string, _ []byte) (*auth.AgentToken, error) {
	// Return a valid agent token for any mcp_agt_ prefixed token
	if auth.ValidateTokenFormat(rawToken) {
		return &auth.AgentToken{
			Name:           "test-agent",
			TokenPrefix:    auth.TokenPrefix(rawToken),
			AllowedServers: []string{"*"},
			Permissions:    []string{"read"},
			ExpiresAt:      time.Now().Add(24 * time.Hour),
			CreatedAt:      time.Now(),
		}, nil
	}
	return nil, fmt.Errorf("invalid token format")
}

func (m *mockTokenStore) UpdateAgentTokenLastUsed(_ string) error {
	return nil
}

func (m *mockTokenStore) RegenerateAgentToken(name string, _ string, _ []byte) (*auth.AgentToken, error) {
	if m.regenErr != nil {
		return nil, m.regenErr
	}
	t, ok := m.tokens[name]
	if !ok {
		return nil, fmt.Errorf("agent token %q not found", name)
	}
	t.Revoked = false
	t.TokenPrefix = "mcp_agt_newt"
	m.tokens[name] = t
	if m.regenToken != nil {
		return m.regenToken, nil
	}
	return &t, nil
}

// --- Mock controller for token tests ---

type mockTokenController struct {
	baseController
	apiKey  string
	servers []string
}

func (m *mockTokenController) GetCurrentConfig() interface{} {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockTokenController) GetAllServers() ([]map[string]interface{}, error) {
	result := make([]map[string]interface{}, 0, len(m.servers))
	for _, name := range m.servers {
		result = append(result, map[string]interface{}{
			"name": name,
			"id":   name,
		})
	}
	return result, nil
}

// --- Helper to create a test server with token store ---

func newTestTokenServer(t *testing.T, store *mockTokenStore, servers []string) *Server {
	t.Helper()
	logger := zap.NewNop().Sugar()
	ctrl := &mockTokenController{
		apiKey:  "test-api-key",
		servers: servers,
	}
	srv := NewServer(ctrl, logger, nil)

	// Use a temp dir for HMAC key
	dataDir := t.TempDir()
	srv.SetTokenStore(store, dataDir)
	return srv
}

func doRequest(t *testing.T, srv *Server, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(data)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-api-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// decodeSuccess unwraps the {success, data} envelope and decodes data into target.
func decodeSuccess(t *testing.T, w *httptest.ResponseRecorder, target interface{}) {
	t.Helper()
	var envelope contracts.APIResponse
	err := json.NewDecoder(w.Body).Decode(&envelope)
	require.NoError(t, err)
	require.True(t, envelope.Success, "expected success=true, got error: %s", envelope.Error)
	// Re-encode Data and decode into target
	data, err := json.Marshal(envelope.Data)
	require.NoError(t, err)
	err = json.Unmarshal(data, target)
	require.NoError(t, err)
}

// --- Tests ---

func TestCreateToken_Success(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, []string{"server1", "server2"})

	body := createTokenRequest{
		Name:           "my-agent",
		AllowedServers: []string{"server1"},
		Permissions:    []string{"read", "write"},
		ExpiresIn:      "30d",
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)

	assert.Equal(t, http.StatusCreated, w.Code, "Expected 201 Created")

	var resp createTokenResponse
	decodeSuccess(t, w, &resp)

	assert.Equal(t, "my-agent", resp.Name)
	assert.NotEmpty(t, resp.Token, "Token secret should be returned")
	assert.True(t, auth.ValidateTokenFormat(resp.Token), "Token should have valid format")
	assert.Equal(t, []string{"server1"}, resp.AllowedServers)
	assert.Equal(t, []string{"read", "write"}, resp.Permissions)
	assert.False(t, resp.ExpiresAt.IsZero(), "ExpiresAt should be set")
	assert.False(t, resp.CreatedAt.IsZero(), "CreatedAt should be set")

	// Verify token was stored
	stored, err := store.GetAgentTokenByName("my-agent")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "my-agent", stored.Name)
}

func TestCreateToken_DefaultPermissions(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	body := createTokenRequest{
		Name: "default-perms",
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp createTokenResponse
	decodeSuccess(t, w, &resp)

	assert.Equal(t, []string{"read"}, resp.Permissions)
	assert.Equal(t, []string{"*"}, resp.AllowedServers)
}

func TestCreateToken_DefaultExpiry(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	body := createTokenRequest{
		Name:        "default-expiry",
		Permissions: []string{"read"},
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp createTokenResponse
	decodeSuccess(t, w, &resp)

	// Should be approximately 30 days from now
	expectedExpiry := time.Now().UTC().Add(30 * 24 * time.Hour)
	assert.WithinDuration(t, expectedExpiry, resp.ExpiresAt, 5*time.Second)
}

func TestCreateToken_DuplicateName(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	body := createTokenRequest{
		Name:        "duplicate",
		Permissions: []string{"read"},
	}

	// First create should succeed
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusCreated, w.Code)

	// Second create with same name should fail
	w = doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusConflict, w.Code, "Expected 409 Conflict for duplicate name")
}

func TestCreateToken_InvalidName(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	tests := []struct {
		name string
		desc string
	}{
		{"", "empty name"},
		{"_invalid", "starts with underscore"},
		{"-invalid", "starts with hyphen"},
		{"has spaces", "contains spaces"},
		{"has!special", "contains special chars"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaX", "65 chars (over limit)"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			body := createTokenRequest{
				Name:        tt.name,
				Permissions: []string{"read"},
			}
			w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 for %s", tt.desc)
		})
	}
}

func TestCreateToken_ValidNames(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	validNames := []string{
		"a",
		"agent1",
		"my-agent",
		"my_agent",
		"Agent-Token_123",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 64 chars exactly
	}

	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			body := createTokenRequest{
				Name:        name,
				Permissions: []string{"read"},
			}
			w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
			assert.Equal(t, http.StatusCreated, w.Code, "Expected 201 for valid name %q", name)
		})
	}
}

func TestCreateToken_MissingRead(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	body := createTokenRequest{
		Name:        "no-read",
		Permissions: []string{"write"},
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 for permissions without read")
}

func TestCreateToken_InvalidPermission(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	body := createTokenRequest{
		Name:        "bad-perm",
		Permissions: []string{"read", "admin"},
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 for invalid permission")
}

func TestCreateToken_InvalidExpiry(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	tests := []struct {
		expiresIn string
		desc      string
	}{
		{"366d", "over 365 days"},
		{"9000h", "over 365 days in hours"},
		{"-1d", "negative days"},
		{"abc", "non-numeric"},
		{"0d", "zero days"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			body := createTokenRequest{
				Name:        "expiry-test-" + tt.expiresIn,
				Permissions: []string{"read"},
				ExpiresIn:   tt.expiresIn,
			}
			w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 for expiry %q", tt.expiresIn)
		})
	}
}

func TestCreateToken_ValidExpiry(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	tests := []struct {
		expiresIn      string
		expectedOffset time.Duration
		desc           string
	}{
		{"1d", 24 * time.Hour, "1 day"},
		{"30d", 30 * 24 * time.Hour, "30 days"},
		{"365d", 365 * 24 * time.Hour, "365 days"},
		{"24h", 24 * time.Hour, "24 hours"},
		{"720h", 720 * time.Hour, "720 hours"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			body := createTokenRequest{
				Name:        "expiry-" + tt.expiresIn,
				Permissions: []string{"read"},
				ExpiresIn:   tt.expiresIn,
			}
			w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
			assert.Equal(t, http.StatusCreated, w.Code, "Expected 201 for expiry %q", tt.expiresIn)

			var resp createTokenResponse
			decodeSuccess(t, w, &resp)

			expectedExpiry := time.Now().UTC().Add(tt.expectedOffset)
			assert.WithinDuration(t, expectedExpiry, resp.ExpiresAt, 5*time.Second)
		})
	}
}

func TestCreateToken_InvalidAllowedServers(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, []string{"server1", "server2"})

	body := createTokenRequest{
		Name:           "bad-server",
		Permissions:    []string{"read"},
		AllowedServers: []string{"nonexistent-server"},
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 for unknown server")
}

func TestCreateToken_WildcardAllowedServers(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, []string{"server1"})

	body := createTokenRequest{
		Name:           "wildcard",
		Permissions:    []string{"read"},
		AllowedServers: []string{"*"},
	}

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusCreated, w.Code, "Expected 201 for wildcard server")
}

func TestListTokens(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	// Create two tokens
	for _, name := range []string{"token-a", "token-b"} {
		body := createTokenRequest{
			Name:        name,
			Permissions: []string{"read"},
		}
		w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
		require.Equal(t, http.StatusCreated, w.Code)
	}

	// List tokens
	w := doRequest(t, srv, http.MethodGet, "/api/v1/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var listResp struct {
		Tokens []tokenInfoResponse `json:"tokens"`
	}
	decodeSuccess(t, w, &listResp)
	assert.Len(t, listResp.Tokens, 2)

	// Verify no token secrets are exposed
	for _, token := range listResp.Tokens {
		assert.NotEmpty(t, token.Name)
		assert.NotEmpty(t, token.TokenPrefix)
		// tokenInfoResponse doesn't have a Token field so secrets can't leak
	}
}

func TestListTokens_Empty(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodGet, "/api/v1/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var listResp struct {
		Tokens []tokenInfoResponse `json:"tokens"`
	}
	decodeSuccess(t, w, &listResp)
	assert.Len(t, listResp.Tokens, 0)
}

func TestGetToken(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	// Create a token
	body := createTokenRequest{
		Name:        "get-me",
		Permissions: []string{"read", "write"},
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	require.Equal(t, http.StatusCreated, w.Code)

	// Get it
	w = doRequest(t, srv, http.MethodGet, "/api/v1/tokens/get-me", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var token tokenInfoResponse
	decodeSuccess(t, w, &token)
	assert.Equal(t, "get-me", token.Name)
	assert.Equal(t, []string{"read", "write"}, token.Permissions)
}

func TestGetToken_NotFound(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodGet, "/api/v1/tokens/nonexistent", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevokeToken(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	// Create a token
	body := createTokenRequest{
		Name:        "revoke-me",
		Permissions: []string{"read"},
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	require.Equal(t, http.StatusCreated, w.Code)

	// Revoke it
	w = doRequest(t, srv, http.MethodDelete, "/api/v1/tokens/revoke-me", nil)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify it's revoked
	stored, err := store.GetAgentTokenByName("revoke-me")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.Revoked)
}

func TestRevokeToken_NotFound(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodDelete, "/api/v1/tokens/nonexistent", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRegenerateToken(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	// Create a token
	createBody := createTokenRequest{
		Name:        "regen-me",
		Permissions: []string{"read"},
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", createBody)
	require.Equal(t, http.StatusCreated, w.Code)

	var createResp createTokenResponse
	decodeSuccess(t, w, &createResp)
	oldToken := createResp.Token

	// Regenerate
	w = doRequest(t, srv, http.MethodPost, "/api/v1/tokens/regen-me/regenerate", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var regenResp regenerateTokenResponse
	decodeSuccess(t, w, &regenResp)

	assert.Equal(t, "regen-me", regenResp.Name)
	assert.NotEmpty(t, regenResp.Token)
	assert.True(t, auth.ValidateTokenFormat(regenResp.Token), "New token should have valid format")
	assert.NotEqual(t, oldToken, regenResp.Token, "New token should differ from old")
}

func TestRegenerateToken_NotFound(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens/nonexistent/regenerate", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTokenEndpoints_AgentTokenRejected(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	// Generate a valid agent token to use for authentication
	agentToken, err := auth.GenerateToken()
	require.NoError(t, err)

	endpoints := []struct {
		method string
		path   string
		body   interface{}
	}{
		{http.MethodPost, "/api/v1/tokens", createTokenRequest{Name: "test", Permissions: []string{"read"}}},
		{http.MethodGet, "/api/v1/tokens", nil},
		{http.MethodGet, "/api/v1/tokens/test", nil},
		{http.MethodDelete, "/api/v1/tokens/test", nil},
		{http.MethodPost, "/api/v1/tokens/test/regenerate", nil},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			var reqBody *bytes.Reader
			if ep.body != nil {
				data, jsonErr := json.Marshal(ep.body)
				require.NoError(t, jsonErr)
				reqBody = bytes.NewReader(data)
			} else {
				reqBody = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(ep.method, ep.path, reqBody)
			req.Header.Set("Content-Type", "application/json")
			// Use agent token instead of admin API key
			req.Header.Set("X-API-Key", agentToken)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code,
				"Agent tokens should get 403 on %s %s", ep.method, ep.path)

			// Verify error message
			var errResp map[string]interface{}
			decodeErr := json.NewDecoder(w.Body).Decode(&errResp)
			require.NoError(t, decodeErr)
			assert.Contains(t, errResp["error"], "Agent tokens cannot manage tokens")
		})
	}
}

func TestTokenEndpoints_AdminAllowed(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	// Create via admin API key (default test-api-key matches mockTokenController.apiKey)
	body := createTokenRequest{
		Name:        "admin-created",
		Permissions: []string{"read"},
	}
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	assert.Equal(t, http.StatusCreated, w.Code, "Admin should be able to create tokens")

	// List via admin API key
	w = doRequest(t, srv, http.MethodGet, "/api/v1/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code, "Admin should be able to list tokens")
}

// --- Validation helper tests ---

func TestValidateTokenName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid", false},
		{"a", false},
		{"abc-def", false},
		{"abc_def", false},
		{"A1_b2-c3", false},
		{"", true},
		{"_start", true},
		{"-start", true},
		{"has space", true},
		{"has!bang", true},
		{string(make([]byte, 65)), true}, // too long
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.name), func(t *testing.T) {
			err := validateTokenName(tt.name)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseExpiry(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		desc    string
	}{
		{"", false, "empty defaults to 30d"},
		{"1d", false, "1 day"},
		{"30d", false, "30 days"},
		{"365d", false, "365 days"},
		{"24h", false, "24 hours"},
		{"720h", false, "720 hours"},
		{"366d", true, "over 365 days"},
		{"0d", true, "zero days"},
		{"-1d", true, "negative days"},
		{"abc", true, "non-numeric"},
		{"8761h", true, "over 365 days in hours"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			result, err := parseExpiry(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.True(t, result.After(time.Now()), "Expiry should be in the future")
			}
		})
	}
}

func TestValidateAllowedServers(t *testing.T) {
	ctrl := &mockTokenController{
		servers: []string{"server1", "server2"},
	}

	tests := []struct {
		servers []string
		wantErr bool
		desc    string
	}{
		{nil, false, "nil defaults to wildcard"},
		{[]string{}, false, "empty defaults to wildcard"},
		{[]string{"*"}, false, "wildcard"},
		{[]string{"server1"}, false, "known server"},
		{[]string{"server1", "server2"}, false, "multiple known servers"},
		{[]string{"unknown"}, true, "unknown server"},
		{[]string{"server1", "unknown"}, true, "mix of known and unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			err := validateAllowedServers(tt.servers, ctrl)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateToken_InvalidJSON(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-api-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTokenEndpoints_NoStoreReturns500(t *testing.T) {
	logger := zap.NewNop().Sugar()
	ctrl := &mockTokenController{
		apiKey:  "test-api-key",
		servers: nil,
	}
	srv := NewServer(ctrl, logger, nil)
	// Don't call SetTokenStore

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/tokens"},
		{http.MethodGet, "/api/v1/tokens"},
		{http.MethodGet, "/api/v1/tokens/test"},
		{http.MethodDelete, "/api/v1/tokens/test"},
		{http.MethodPost, "/api/v1/tokens/test/regenerate"},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, bytes.NewReader(nil))
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	}
}
