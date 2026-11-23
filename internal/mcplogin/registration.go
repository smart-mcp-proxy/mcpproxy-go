package mcplogin

import (
	"time"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientidentity"
)

// RFC 7591 error codes used by registration.
const (
	// ErrCodeInvalidRedirectURI is the error code for a refused redirect URI.
	ErrCodeInvalidRedirectURI = "invalid_redirect_uri"
	// ErrCodeInvalidClientMetadata is the error code for other refused metadata.
	ErrCodeInvalidClientMetadata = "invalid_client_metadata"
)

const (
	maxNameBytes     = 128
	maxSoftwareBytes = 64
)

// RegistrationRequest is the JSON body of POST /oauth/register.
type RegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	SoftwareID              string   `json:"software_id"`
	SoftwareVersion         string   `json:"software_version"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// Registration is a validated registration with its effective values. The
// caller assigns ClientID (see NewClientID) before storing or answering.
type Registration struct {
	ClientID                string
	RedirectURIs            []string
	ClientName              string
	SoftwareID              string
	SoftwareVersion         string
	TokenEndpointAuthMethod string
	GrantTypes              []string
	ResponseTypes           []string
}

// RegError is a registration refusal, shaped for an RFC 7591 error answer.
type RegError struct {
	// Code is ErrCodeInvalidRedirectURI or ErrCodeInvalidClientMetadata.
	Code string
	// Description is a human-readable reason.
	Description string
}

// Error implements error.
func (e *RegError) Error() string { return e.Code + ": " + e.Description }

// truncateUTF8 cuts s to at most max bytes without splitting a character.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func sanitize(s string, max int) string {
	return truncateUTF8(clientidentity.SanitizeDisplay(s), max)
}

// ValidateRegistration checks a request and returns the registration with the
// effective values: method "none", grants authorization_code and
// refresh_token, response type code. A request that asks for anything else is
// refused rather than silently changed.
func ValidateRegistration(req RegistrationRequest) (Registration, *RegError) {
	if err := ValidateRegisteredRedirects(req.RedirectURIs); err != nil {
		return Registration{}, &RegError{Code: ErrCodeInvalidRedirectURI, Description: err.Error()}
	}
	if m := req.TokenEndpointAuthMethod; m != "" && m != "none" {
		return Registration{}, &RegError{Code: ErrCodeInvalidClientMetadata,
			Description: "only token_endpoint_auth_method \"none\" is supported"}
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return Registration{}, &RegError{Code: ErrCodeInvalidClientMetadata,
				Description: "only the authorization_code and refresh_token grant types are supported"}
		}
	}
	for _, r := range req.ResponseTypes {
		if r != "code" {
			return Registration{}, &RegError{Code: ErrCodeInvalidClientMetadata,
				Description: "only the code response type is supported"}
		}
	}
	return Registration{
		RedirectURIs:            append([]string(nil), req.RedirectURIs...),
		ClientName:              sanitize(req.ClientName, maxNameBytes),
		SoftwareID:              sanitize(req.SoftwareID, maxSoftwareBytes),
		SoftwareVersion:         sanitize(req.SoftwareVersion, maxSoftwareBytes),
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
	}, nil
}

// NewClientID returns a fresh registration client id: 22 base62 characters.
func NewClientID() (string, error) { return randomBase62(22) }

// Response is the RFC 7591 201 body. It never carries a client_secret.
func (r Registration) Response(issuedAt time.Time) map[string]any {
	m := map[string]any{
		"client_id":                  r.ClientID,
		"client_id_issued_at":        issuedAt.Unix(),
		"redirect_uris":              r.RedirectURIs,
		"client_name":                r.ClientName,
		"token_endpoint_auth_method": r.TokenEndpointAuthMethod,
		"grant_types":                r.GrantTypes,
		"response_types":             r.ResponseTypes,
	}
	if r.SoftwareID != "" {
		m["software_id"] = r.SoftwareID
	}
	if r.SoftwareVersion != "" {
		m["software_version"] = r.SoftwareVersion
	}
	return m
}
