//go:build teams

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetProvider_Google(t *testing.T) {
	p, err := GetProvider("google", "")
	require.NoError(t, err)

	assert.Equal(t, "google", p.Name)
	assert.Equal(t, "https://accounts.google.com/o/oauth2/v2/auth", p.AuthURL)
	assert.Equal(t, "https://oauth2.googleapis.com/token", p.TokenURL)
	assert.Equal(t, "https://openidconnect.googleapis.com/v1/userinfo", p.UserInfoURL)
	assert.Equal(t, []string{"openid", "email", "profile"}, p.Scopes)
	assert.True(t, p.SupportsOIDC)
	assert.True(t, p.SupportsPKCE)
	assert.Empty(t, p.EmailsURL)
}

func TestGetProvider_GitHub(t *testing.T) {
	p, err := GetProvider("github", "")
	require.NoError(t, err)

	assert.Equal(t, "github", p.Name)
	assert.Equal(t, "https://github.com/login/oauth/authorize", p.AuthURL)
	assert.Equal(t, "https://github.com/login/oauth/access_token", p.TokenURL)
	assert.Equal(t, "https://api.github.com/user", p.UserInfoURL)
	assert.Equal(t, "https://api.github.com/user/emails", p.EmailsURL)
	assert.Equal(t, []string{"user:email", "read:user"}, p.Scopes)
	assert.False(t, p.SupportsOIDC)
	assert.False(t, p.SupportsPKCE)
}

func TestGetProvider_Microsoft(t *testing.T) {
	t.Run("with tenant", func(t *testing.T) {
		p, err := GetProvider("microsoft", "my-tenant-id")
		require.NoError(t, err)

		assert.Equal(t, "microsoft", p.Name)
		assert.Equal(t, "https://login.microsoftonline.com/my-tenant-id/oauth2/v2.0/authorize", p.AuthURL)
		assert.Equal(t, "https://login.microsoftonline.com/my-tenant-id/oauth2/v2.0/token", p.TokenURL)
		assert.Equal(t, "https://graph.microsoft.com/v1.0/me", p.UserInfoURL)
		assert.Equal(t, []string{"openid", "email", "profile", "User.Read"}, p.Scopes)
		assert.True(t, p.SupportsOIDC)
		assert.True(t, p.SupportsPKCE)
	})

	t.Run("default tenant", func(t *testing.T) {
		p, err := GetProvider("microsoft", "")
		require.NoError(t, err)

		assert.Contains(t, p.AuthURL, "/common/")
		assert.Contains(t, p.TokenURL, "/common/")
	})
}

func TestGetProvider_Invalid(t *testing.T) {
	_, err := GetProvider("unknown", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown OAuth provider")
	assert.Contains(t, err.Error(), "unknown")
}

func TestGetProvider_CaseInsensitive(t *testing.T) {
	p, err := GetProvider("Google", "")
	require.NoError(t, err)
	assert.Equal(t, "google", p.Name)

	p, err = GetProvider("GITHUB", "")
	require.NoError(t, err)
	assert.Equal(t, "github", p.Name)
}

func TestBuildAuthURL_Google(t *testing.T) {
	p, _ := GetProvider("google", "")

	authURL := p.BuildAuthURL("client123", "http://localhost:8080/callback", "state-abc", "challenge-xyz")

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)

	assert.Equal(t, "accounts.google.com", parsed.Host)
	assert.Equal(t, "/o/oauth2/v2/auth", parsed.Path)

	params := parsed.Query()
	assert.Equal(t, "client123", params.Get("client_id"))
	assert.Equal(t, "http://localhost:8080/callback", params.Get("redirect_uri"))
	assert.Equal(t, "code", params.Get("response_type"))
	assert.Equal(t, "openid email profile", params.Get("scope"))
	assert.Equal(t, "state-abc", params.Get("state"))
	// Google supports PKCE
	assert.Equal(t, "challenge-xyz", params.Get("code_challenge"))
	assert.Equal(t, "S256", params.Get("code_challenge_method"))
}

func TestBuildAuthURL_GitHub(t *testing.T) {
	p, _ := GetProvider("github", "")

	authURL := p.BuildAuthURL("gh-client", "http://localhost:8080/callback", "state-123", "challenge-456")

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)

	assert.Equal(t, "github.com", parsed.Host)
	assert.Equal(t, "/login/oauth/authorize", parsed.Path)

	params := parsed.Query()
	assert.Equal(t, "gh-client", params.Get("client_id"))
	assert.Equal(t, "http://localhost:8080/callback", params.Get("redirect_uri"))
	assert.Equal(t, "code", params.Get("response_type"))
	assert.Equal(t, "user:email read:user", params.Get("scope"))
	assert.Equal(t, "state-123", params.Get("state"))
	// GitHub does NOT support PKCE
	assert.Empty(t, params.Get("code_challenge"))
	assert.Empty(t, params.Get("code_challenge_method"))
}

func TestBuildAuthURL_Microsoft(t *testing.T) {
	p, _ := GetProvider("microsoft", "contoso")

	authURL := p.BuildAuthURL("ms-client", "http://localhost:8080/callback", "state-ms", "challenge-ms")

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)

	assert.Equal(t, "login.microsoftonline.com", parsed.Host)
	assert.Equal(t, "/contoso/oauth2/v2.0/authorize", parsed.Path)

	params := parsed.Query()
	assert.Equal(t, "ms-client", params.Get("client_id"))
	assert.Equal(t, "challenge-ms", params.Get("code_challenge"))
	assert.Equal(t, "S256", params.Get("code_challenge_method"))
}

func TestBuildAuthURL_NoPKCE_WhenEmptyChallenge(t *testing.T) {
	p, _ := GetProvider("google", "")

	authURL := p.BuildAuthURL("client", "http://localhost/cb", "state", "")

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)

	params := parsed.Query()
	assert.Empty(t, params.Get("code_challenge"))
	assert.Empty(t, params.Get("code_challenge_method"))
}

func TestExchangeCode_MockServer(t *testing.T) {
	expectedToken := TokenResponse{
		AccessToken:  "access-token-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "refresh-token-456",
		IDToken:      "id-token-789",
		Scope:        "openid email profile",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		err := r.ParseForm()
		require.NoError(t, err)

		assert.Equal(t, "authorization_code", r.FormValue("grant_type"))
		assert.Equal(t, "auth-code-xyz", r.FormValue("code"))
		assert.Equal(t, "http://localhost/callback", r.FormValue("redirect_uri"))
		assert.Equal(t, "test-client", r.FormValue("client_id"))
		assert.Equal(t, "test-secret", r.FormValue("client_secret"))
		assert.Equal(t, "verifier-abc", r.FormValue("code_verifier"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(expectedToken)
	}))
	defer server.Close()

	// Create a provider pointing to the mock server
	p := &OAuthProvider{
		Name:         "google",
		TokenURL:     server.URL,
		SupportsPKCE: true,
	}

	// Save and restore the original HTTP client
	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tokenResp, err := p.ExchangeCode(ctx, "auth-code-xyz", "http://localhost/callback", "test-client", "test-secret", "verifier-abc")
	require.NoError(t, err)

	assert.Equal(t, expectedToken.AccessToken, tokenResp.AccessToken)
	assert.Equal(t, expectedToken.TokenType, tokenResp.TokenType)
	assert.Equal(t, expectedToken.ExpiresIn, tokenResp.ExpiresIn)
	assert.Equal(t, expectedToken.RefreshToken, tokenResp.RefreshToken)
	assert.Equal(t, expectedToken.IDToken, tokenResp.IDToken)
	assert.Equal(t, expectedToken.Scope, tokenResp.Scope)
}

func TestExchangeCode_NoPKCE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseForm()
		require.NoError(t, err)

		// code_verifier should not be present when PKCE is not supported
		assert.Empty(t, r.FormValue("code_verifier"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{AccessToken: "token"})
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "github",
		TokenURL:     server.URL,
		SupportsPKCE: false,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	_, err := p.ExchangeCode(ctx, "code", "http://localhost/cb", "client", "secret", "verifier")
	require.NoError(t, err)
}

func TestExchangeCode_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Code expired"}`))
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:     "google",
		TokenURL: server.URL,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	_, err := p.ExchangeCode(ctx, "expired-code", "http://localhost/cb", "client", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
	assert.Contains(t, err.Error(), "invalid_grant")
}

func TestFetchUserInfo_Google_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer google-access-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":     "google-user-123",
			"email":   "alice@example.com",
			"name":    "Alice Example",
			"picture": "https://lh3.googleusercontent.com/photo.jpg",
		})
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "google",
		UserInfoURL:  server.URL,
		SupportsOIDC: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "google-access-token")
	require.NoError(t, err)

	assert.Equal(t, "alice@example.com", info.Email)
	assert.Equal(t, "Alice Example", info.DisplayName)
	assert.Equal(t, "google-user-123", info.SubjectID)
	assert.Equal(t, "https://lh3.googleusercontent.com/photo.jpg", info.AvatarURL)
}

func TestFetchUserInfo_Microsoft_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer ms-access-token", r.Header.Get("Authorization"))

		// Microsoft Graph API returns different field names
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":                "ms-user-456",
			"displayName":      "Bob Corp",
			"mail":             "bob@contoso.com",
			"userPrincipalName": "bob@contoso.onmicrosoft.com",
		})
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "microsoft",
		UserInfoURL:  server.URL,
		SupportsOIDC: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "ms-access-token")
	require.NoError(t, err)

	assert.Equal(t, "bob@contoso.com", info.Email)
	assert.Equal(t, "Bob Corp", info.DisplayName)
	assert.Equal(t, "ms-user-456", info.SubjectID)
}

func TestFetchUserInfo_Microsoft_FallbackToUPN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mail is null, should fall back to userPrincipalName
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":                "ms-user-789",
			"displayName":      "Charlie",
			"userPrincipalName": "charlie@contoso.onmicrosoft.com",
		})
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "microsoft",
		UserInfoURL:  server.URL,
		SupportsOIDC: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "token")
	require.NoError(t, err)

	assert.Equal(t, "charlie@contoso.onmicrosoft.com", info.Email)
}

func TestFetchUserInfo_GitHub_MockServer(t *testing.T) {
	mux := http.NewServeMux()

	// /user endpoint
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer gh-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         42,
			"login":      "octocat",
			"name":       "The Octocat",
			"avatar_url": "https://avatars.githubusercontent.com/u/42",
		})
	})

	// /user/emails endpoint
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer gh-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]gitHubEmail{
			{Email: "noreply@github.com", Primary: false, Verified: true},
			{Email: "octocat@github.com", Primary: true, Verified: true},
			{Email: "unverified@example.com", Primary: false, Verified: false},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &OAuthProvider{
		Name:        "github",
		UserInfoURL: server.URL + "/user",
		EmailsURL:   server.URL + "/user/emails",
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "gh-token")
	require.NoError(t, err)

	assert.Equal(t, "octocat@github.com", info.Email)
	assert.Equal(t, "The Octocat", info.DisplayName)
	assert.Equal(t, "42", info.SubjectID)
	assert.Equal(t, "https://avatars.githubusercontent.com/u/42", info.AvatarURL)
}

func TestFetchUserInfo_GitHub_EmailInProfile(t *testing.T) {
	// When the user has a public email on their profile, /user/emails should not be called
	mux := http.NewServeMux()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         99,
			"login":      "devuser",
			"name":       "Dev User",
			"email":      "dev@example.com",
			"avatar_url": "https://avatars.githubusercontent.com/u/99",
		})
	})

	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		t.Error("/user/emails should not be called when email is in profile")
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &OAuthProvider{
		Name:        "github",
		UserInfoURL: server.URL + "/user",
		EmailsURL:   server.URL + "/user/emails",
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "token")
	require.NoError(t, err)

	assert.Equal(t, "dev@example.com", info.Email)
	assert.Equal(t, "Dev User", info.DisplayName)
	assert.Equal(t, "99", info.SubjectID)
}

func TestFetchUserInfo_GitHub_FallbackToLogin(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// name is empty, should fall back to login
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         77,
			"login":      "noname-user",
			"email":      "noname@example.com",
			"avatar_url": "",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &OAuthProvider{
		Name:        "github",
		UserInfoURL: server.URL + "/user",
		EmailsURL:   server.URL + "/user/emails",
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "token")
	require.NoError(t, err)

	assert.Equal(t, "noname-user", info.DisplayName)
}

func TestFetchUserInfo_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "google",
		UserInfoURL:  server.URL,
		SupportsOIDC: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	_, err := p.FetchUserInfo(ctx, "bad-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")
}

func TestFetchUserInfoFromToken_WithIDToken(t *testing.T) {
	// Create a mock ID token (header.payload.signature)
	claims := map[string]interface{}{
		"sub":     "oidc-sub-123",
		"email":   "oidc@example.com",
		"name":    "OIDC User",
		"picture": "https://example.com/photo.jpg",
	}
	claimsJSON, _ := json.Marshal(claims)

	// Build a fake JWT (we don't verify signatures)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	idToken := fmt.Sprintf("%s.%s.%s", header, payload, signature)

	// The UserInfo endpoint should not be called when ID token parsing succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("UserInfo endpoint should not be called when ID token is available")
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "google",
		UserInfoURL:  server.URL,
		SupportsOIDC: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	tokenResp := &TokenResponse{
		AccessToken: "access-token",
		IDToken:     idToken,
	}

	info, err := p.FetchUserInfoFromToken(ctx, tokenResp)
	require.NoError(t, err)

	assert.Equal(t, "oidc@example.com", info.Email)
	assert.Equal(t, "OIDC User", info.DisplayName)
	assert.Equal(t, "oidc-sub-123", info.SubjectID)
	assert.Equal(t, "https://example.com/photo.jpg", info.AvatarURL)
}

func TestFetchUserInfoFromToken_FallbackToUserInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":   "fallback-sub",
			"email": "fallback@example.com",
			"name":  "Fallback User",
		})
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "google",
		UserInfoURL:  server.URL,
		SupportsOIDC: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()

	// No ID token - should fall back to UserInfo endpoint
	tokenResp := &TokenResponse{
		AccessToken: "access-token",
	}

	info, err := p.FetchUserInfoFromToken(ctx, tokenResp)
	require.NoError(t, err)

	assert.Equal(t, "fallback@example.com", info.Email)
	assert.Equal(t, "Fallback User", info.DisplayName)
}

func TestFetchUserInfoFromToken_NonOIDCProvider(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    55,
			"login": "ghuser",
			"name":  "GH User",
			"email": "gh@example.com",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &OAuthProvider{
		Name:         "github",
		UserInfoURL:  server.URL + "/user",
		EmailsURL:    server.URL + "/user/emails",
		SupportsOIDC: false,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	tokenResp := &TokenResponse{
		AccessToken: "gh-token",
		IDToken:     "should-be-ignored", // GitHub doesn't support OIDC
	}

	info, err := p.FetchUserInfoFromToken(ctx, tokenResp)
	require.NoError(t, err)

	assert.Equal(t, "gh@example.com", info.Email)
	assert.Equal(t, "GH User", info.DisplayName)
}

func TestParseIDToken(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		claims := map[string]interface{}{
			"sub":     "subject-id",
			"email":   "test@example.com",
			"name":    "Test User",
			"picture": "https://example.com/pic.jpg",
		}
		claimsJSON, _ := json.Marshal(claims)

		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
		payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
		sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))

		info, err := parseIDToken(strings.Join([]string{header, payload, sig}, "."))
		require.NoError(t, err)
		assert.Equal(t, "subject-id", info.SubjectID)
		assert.Equal(t, "test@example.com", info.Email)
		assert.Equal(t, "Test User", info.DisplayName)
		assert.Equal(t, "https://example.com/pic.jpg", info.AvatarURL)
	})

	t.Run("invalid format", func(t *testing.T) {
		_, err := parseIDToken("not-a-jwt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JWT format")
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, err := parseIDToken("a.!!!invalid!!!.c")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decoding JWT payload")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`not json`))
		sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))

		_, err := parseIDToken(strings.Join([]string{header, payload, sig}, "."))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parsing JWT claims")
	})
}

func TestBase64URLDecode_Padding(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no padding needed", "dGVzdA"},            // "test" - len%4 == 0 (after raw encoding... actually len=4)
		{"needs one pad", "dGVzdDE"},               // "test1" - len%4 == 3
		{"needs two pads", "dGVzdDEy"},             // "test12" - len%4 == 0 (after raw encoding)
		{"standard base64url", "SGVsbG8gV29ybGQ"}, // "Hello World"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := base64URLDecode(tt.input)
			require.NoError(t, err)
			assert.NotEmpty(t, result)
		})
	}
}

func TestExchangeCode_NoClientSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseForm()
		require.NoError(t, err)

		// client_secret should not be present when empty
		assert.Empty(t, r.FormValue("client_secret"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{AccessToken: "token"})
	}))
	defer server.Close()

	p := &OAuthProvider{
		Name:         "google",
		TokenURL:     server.URL,
		SupportsPKCE: true,
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	_, err := p.ExchangeCode(ctx, "code", "http://localhost/cb", "client", "", "verifier")
	require.NoError(t, err)
}

func TestGitHub_NoVerifiedEmail(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    100,
			"login": "noemail",
		})
	})

	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]gitHubEmail{
			{Email: "unverified@example.com", Primary: true, Verified: false},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &OAuthProvider{
		Name:        "github",
		UserInfoURL: server.URL + "/user",
		EmailsURL:   server.URL + "/user/emails",
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	_, err := p.FetchUserInfo(ctx, "token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no verified email")
}

func TestGitHub_FallbackToVerifiedNonPrimary(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    101,
			"login": "secondary",
		})
	})

	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]gitHubEmail{
			{Email: "unverified@example.com", Primary: true, Verified: false},
			{Email: "verified-secondary@example.com", Primary: false, Verified: true},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &OAuthProvider{
		Name:        "github",
		UserInfoURL: server.URL + "/user",
		EmailsURL:   server.URL + "/user/emails",
	}

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	ctx := context.Background()
	info, err := p.FetchUserInfo(ctx, "token")
	require.NoError(t, err)
	assert.Equal(t, "verified-secondary@example.com", info.Email)
}
