//go:build server

package cliclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// CredentialStatus is the non-secret connection view for one brokered upstream,
// as returned by GET /api/v1/user/credentials (spec 074 T8). It deliberately
// mirrors the server's response shape but contains NO token fields: the CLI
// decodes into this typed struct precisely so that any secret material a
// response might carry is dropped rather than rendered (FR-026).
type CredentialStatus struct {
	Server      string     `json:"server" yaml:"server"`
	Mode        string     `json:"mode" yaml:"mode"`
	Status      string     `json:"status" yaml:"status"`
	TokenType   string     `json:"token_type,omitempty" yaml:"token_type,omitempty"`
	Scopes      []string   `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Audience    string     `json:"audience,omitempty" yaml:"audience,omitempty"`
	ObtainedVia string     `json:"obtained_via,omitempty" yaml:"obtained_via,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	ConnectPath string     `json:"connect_path,omitempty" yaml:"connect_path,omitempty"`
}

// credentialListResponse wraps the per-user credential statuses.
type credentialListResponse struct {
	Credentials []CredentialStatus `json:"credentials"`
}

// credentialMessageResponse is the {"message": ...} shape returned by DELETE.
type credentialMessageResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

// ListCredentials fetches the connection status of every brokered upstream for
// the authenticated user. The result never contains secret values (FR-026):
// the response is decoded into the typed CredentialStatus, which has no token
// fields, so any secret a response carries is discarded.
func (c *Client) ListCredentials(ctx context.Context) ([]CredentialStatus, error) {
	resp, err := c.DoRaw(ctx, http.MethodGet, "/api/v1/user/credentials", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call credentials API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, credentialHTTPError(resp.StatusCode, body)
	}

	var out credentialListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return out.Credentials, nil
}

// DeleteCredential disconnects (revokes) the authenticated user's credential for
// a brokered upstream and returns the server's confirmation message.
func (c *Client) DeleteCredential(ctx context.Context, server string) (string, error) {
	path := "/api/v1/user/credentials/" + url.PathEscape(server)
	resp, err := c.DoRaw(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return "", fmt.Errorf("failed to call credentials API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", credentialHTTPError(resp.StatusCode, body)
	}

	var out credentialMessageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	return out.Message, nil
}

// credentialHTTPError builds a friendly error from a non-200 credential
// response, preferring the server's "message"/"error" field and adding a hint
// for the common unauthenticated case.
func credentialHTTPError(status int, body []byte) error {
	msg := ""
	var parsed credentialMessageResponse
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Message != "" {
			msg = parsed.Message
		} else if parsed.Error != "" {
			msg = parsed.Error
		}
	}
	if status == http.StatusUnauthorized {
		hint := "authentication required: provide a user token via --token or MCPPROXY_TOKEN"
		if msg != "" {
			return fmt.Errorf("%s (%s)", hint, msg)
		}
		return fmt.Errorf("%s", hint)
	}
	if msg != "" {
		return fmt.Errorf("credentials API returned status %d: %s", status, msg)
	}
	return fmt.Errorf("credentials API returned status %d: %s", status, string(body))
}
