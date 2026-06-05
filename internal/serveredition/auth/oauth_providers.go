//go:build server

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OAuthProvider defines the OAuth endpoints and behavior for an identity provider.
type OAuthProvider struct {
	Name         string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	EmailsURL    string // GitHub-specific: endpoint for fetching primary email
	Scopes       []string
	SupportsOIDC bool // If true, ID token contains user info
	SupportsPKCE bool
}

// TokenResponse represents the OAuth token exchange response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// OAuthUserInfo represents user profile information from an OAuth provider.
type OAuthUserInfo struct {
	Email       string
	DisplayName string
	SubjectID   string // Provider-unique user identifier
	AvatarURL   string
}

// providerRegistry holds the built-in provider configurations.
var providerRegistry = map[string]func(tenantID string) *OAuthProvider{
	"google":    newGoogleProvider,
	"github":    newGitHubProvider,
	"microsoft": newMicrosoftProvider,
}

func newGoogleProvider(_ string) *OAuthProvider {
	return &OAuthProvider{
		Name:         "google",
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		UserInfoURL:  "https://openidconnect.googleapis.com/v1/userinfo",
		Scopes:       []string{"openid", "email", "profile"},
		SupportsOIDC: true,
		SupportsPKCE: true,
	}
}

func newGitHubProvider(_ string) *OAuthProvider {
	return &OAuthProvider{
		Name:         "github",
		AuthURL:      "https://github.com/login/oauth/authorize",
		TokenURL:     "https://github.com/login/oauth/access_token",
		UserInfoURL:  "https://api.github.com/user",
		EmailsURL:    "https://api.github.com/user/emails",
		Scopes:       []string{"user:email", "read:user"},
		SupportsOIDC: false,
		SupportsPKCE: false,
	}
}

func newMicrosoftProvider(tenantID string) *OAuthProvider {
	if tenantID == "" {
		tenantID = "common"
	}
	return &OAuthProvider{
		Name:         "microsoft",
		AuthURL:      fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenantID),
		TokenURL:     fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		UserInfoURL:  "https://graph.microsoft.com/v1.0/me",
		Scopes:       []string{"openid", "email", "profile", "User.Read"},
		SupportsOIDC: true,
		SupportsPKCE: true,
	}
}

// GetProvider returns a provider configuration by name.
// For Microsoft, tenantID specifies the Azure AD tenant; empty defaults to "common".
func GetProvider(name string, tenantID string) (*OAuthProvider, error) {
	factory, ok := providerRegistry[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown OAuth provider: %q (supported: google, github, microsoft)", name)
	}
	return factory(tenantID), nil
}

// BuildAuthURL constructs the authorization URL with the required query parameters.
func (p *OAuthProvider) BuildAuthURL(clientID, redirectURI, state, codeChallenge string) string {
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(p.Scopes, " ")},
		"state":         {state},
	}

	if p.SupportsPKCE && codeChallenge != "" {
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")
	}

	return p.AuthURL + "?" + params.Encode()
}

// httpClient is the HTTP client used for provider requests. Package-level for testability.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// ExchangeCode exchanges an authorization code for tokens via HTTP POST to the token endpoint.
func (p *OAuthProvider) ExchangeCode(ctx context.Context, code, redirectURI, clientID, clientSecret, codeVerifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"client_id":    {clientID},
	}

	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	if p.SupportsPKCE && codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// GitHub requires Accept: application/json to return JSON instead of form-encoded
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	return &tokenResp, nil
}

// FetchUserInfo retrieves user profile information from the provider.
// For OIDC providers (Google, Microsoft), it first tries to extract info from the ID token,
// falling back to the UserInfo endpoint. For GitHub, it calls /user and /user/emails.
func (p *OAuthProvider) FetchUserInfo(ctx context.Context, accessToken string) (*OAuthUserInfo, error) {
	if p.Name == "github" {
		return p.fetchGitHubUserInfo(ctx, accessToken)
	}
	return p.fetchOIDCUserInfo(ctx, accessToken)
}

// FetchUserInfoFromToken extracts user info from an ID token (for OIDC providers).
// Falls back to the UserInfo endpoint if the ID token cannot be parsed.
func (p *OAuthProvider) FetchUserInfoFromToken(ctx context.Context, tokenResp *TokenResponse) (*OAuthUserInfo, error) {
	if tokenResp.IDToken != "" && p.SupportsOIDC {
		info, err := parseIDToken(tokenResp.IDToken)
		if err == nil && info.Email != "" {
			return info, nil
		}
		// Fall back to UserInfo endpoint
	}
	return p.FetchUserInfo(ctx, tokenResp.AccessToken)
}

// fetchOIDCUserInfo fetches user info from the provider's UserInfo endpoint.
func (p *OAuthProvider) fetchOIDCUserInfo(ctx context.Context, accessToken string) (*OAuthUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading userinfo response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo request failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse the standard OIDC/OAuth userinfo response
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing userinfo response: %w", err)
	}

	info := &OAuthUserInfo{}

	// Standard OIDC claims
	if email, ok := raw["email"].(string); ok {
		info.Email = email
	}
	if name, ok := raw["name"].(string); ok {
		info.DisplayName = name
	}
	if sub, ok := raw["sub"].(string); ok {
		info.SubjectID = sub
	}
	if picture, ok := raw["picture"].(string); ok {
		info.AvatarURL = picture
	}

	// Microsoft Graph API uses different field names
	if p.Name == "microsoft" {
		if displayName, ok := raw["displayName"].(string); ok && info.DisplayName == "" {
			info.DisplayName = displayName
		}
		if mail, ok := raw["mail"].(string); ok && info.Email == "" {
			info.Email = mail
		}
		if upn, ok := raw["userPrincipalName"].(string); ok && info.Email == "" {
			info.Email = upn
		}
		if id, ok := raw["id"].(string); ok && info.SubjectID == "" {
			info.SubjectID = id
		}
	}

	return info, nil
}

// fetchGitHubUserInfo fetches user info from GitHub's /user and /user/emails endpoints.
func (p *OAuthProvider) fetchGitHubUserInfo(ctx context.Context, accessToken string) (*OAuthUserInfo, error) {
	// Fetch user profile
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating github user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github user request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading github user response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var userResp struct {
		ID        int    `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &userResp); err != nil {
		return nil, fmt.Errorf("parsing github user response: %w", err)
	}

	info := &OAuthUserInfo{
		SubjectID: strconv.Itoa(userResp.ID),
		AvatarURL: userResp.AvatarURL,
	}

	// Use display name, fall back to login
	if userResp.Name != "" {
		info.DisplayName = userResp.Name
	} else {
		info.DisplayName = userResp.Login
	}

	// If email is set on the profile, use it
	if userResp.Email != "" {
		info.Email = userResp.Email
	}

	// Fetch primary verified email from /user/emails
	if info.Email == "" && p.EmailsURL != "" {
		email, err := p.fetchGitHubPrimaryEmail(ctx, accessToken)
		if err != nil {
			return nil, fmt.Errorf("fetching github email: %w", err)
		}
		info.Email = email
	}

	return info, nil
}

// gitHubEmail represents a GitHub email entry from the /user/emails endpoint.
type gitHubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// fetchGitHubPrimaryEmail fetches the user's primary verified email from GitHub.
func (p *OAuthProvider) fetchGitHubPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.EmailsURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating github emails request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github emails request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading github emails response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github emails request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var emails []gitHubEmail
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", fmt.Errorf("parsing github emails response: %w", err)
	}

	// Find primary verified email
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}

	// Fall back to any verified email
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}

	return "", fmt.Errorf("no verified email found in github account")
}

// parseIDToken parses a JWT ID token without signature verification.
// We trust the token because it was received over HTTPS directly from the provider.
func parseIDToken(idToken string) (*OAuthUserInfo, error) {
	// JWT is three base64url-encoded parts separated by dots: header.payload.signature
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	// Decode the payload (second part)
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decoding JWT payload: %w", err)
	}

	var claims struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parsing JWT claims: %w", err)
	}

	return &OAuthUserInfo{
		SubjectID:   claims.Sub,
		Email:       claims.Email,
		DisplayName: claims.Name,
		AvatarURL:   claims.Picture,
	}, nil
}

// base64URLDecode decodes a base64url-encoded string with optional padding.
func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}
