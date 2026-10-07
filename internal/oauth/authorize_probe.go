package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Authorization-endpoint probe for a persisted Dynamic Client Registration
// (DCR) client_id.
//
// Authorization servers delete DCR clients (Cloudflare's workers-oauth-provider
// does so after a while). mcpproxy persists the DCR client_id and reuses it on
// every login, so after such a deletion every login opened the browser on an
// authorization URL the server answers with HTTP 400
// {"error":"invalid_request","error_description":"Invalid client_id"} — and,
// as RFC 6749 §4.1.2.1 requires for an invalid client, it never redirects
// back, so mcpproxy never learned the login had failed. The refresh path
// handles the equivalent token-endpoint answer (invalid_client) since Spec
// 113-a; this probe gives the login path the same signal before the URL is
// handed out.

const (
	// AuthorizeProbeTimeout bounds the whole probe request.
	AuthorizeProbeTimeout = 5 * time.Second
	// authorizeProbeMaxBody caps how much of the response body is read; an
	// OAuth error object is tiny, and a consent page is never parsed.
	authorizeProbeMaxBody = 4 << 10
)

// AuthorizeProbeResult is the outcome of ProbeAuthorizationClient. Only
// ClientRejected=true is actionable; every other outcome (Err set, 2xx, 3xx,
// 5xx, an unrelated 4xx, an unparsable body) means "proceed as before".
type AuthorizeProbeResult struct {
	// ClientRejected reports that the authorization server identified the
	// client_id itself as unknown or invalid.
	ClientRejected bool
	// StatusCode is the HTTP status of the probe response (0 when Err is set).
	StatusCode int
	// OAuthError and ErrorDescription are the parsed OAuth error fields, when
	// the body was an OAuth JSON error object.
	OAuthError       string
	ErrorDescription string
	// Err is the transport-level failure, if any.
	Err error
}

// Summary renders the server's answer for logs and error messages, e.g.
// `HTTP 400 invalid_request: Invalid client_id`.
func (r AuthorizeProbeResult) Summary() string {
	s := fmt.Sprintf("HTTP %d", r.StatusCode)
	if r.OAuthError != "" {
		s += " " + r.OAuthError
	}
	if r.ErrorDescription != "" {
		s += ": " + r.ErrorDescription
	}
	return s
}

// invalid_request descriptions that name the client (id) as unknown/invalid.
// Kept deliberately narrow: "Missing client_id" (our own bug, not a stale
// registration), a bad scope or a bad redirect_uri must not match. The
// rejection word must sit right next to "client"/"client_id" (only an
// optional "is"/"was"/"has been" between them): a description such as
// "client sent an invalid code_challenge" is about the request, not the
// registration, and must not clear a valid stored client.
var (
	clientThenRejection = regexp.MustCompile(`\bclient(?:_id| id)?\s+(?:(?:is|was|has\s+been)\s+)?(?:invalid|unknown|not found|does not exist|doesn't exist|not registered|no longer exists)\b`)
	rejectionThenClient = regexp.MustCompile(`\b(?:invalid|unknown|unregistered|nonexistent|no such)\s+client(?:_id| id)?\b`)
)

// ClassifyAuthorizeClientRejection decides whether an authorization-endpoint
// response says the client_id itself is unknown or invalid. It returns the
// parsed OAuth error code and description alongside the verdict.
//
// Matches (status 400 or 401, OAuth JSON error body):
//   - error=invalid_client or error=unauthorized_client;
//   - error=invalid_request whose error_description names the client(_id) as
//     invalid, unknown, not found, not registered or nonexistent
//     (Cloudflare: "Invalid client_id"), case-insensitively — unless the
//     description is about the redirect_uri or the scope.
func ClassifyAuthorizeClientRejection(status int, body []byte) (rejected bool, oauthErr, description string) {
	if status != http.StatusBadRequest && status != http.StatusUnauthorized {
		return false, "", ""
	}
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, "", ""
	}
	oauthErr, description = payload.Error, payload.ErrorDescription
	switch strings.ToLower(strings.TrimSpace(oauthErr)) {
	case "invalid_client", "unauthorized_client":
		return true, oauthErr, description
	case "invalid_request":
		d := strings.ToLower(description)
		if strings.Contains(d, "redirect") || strings.Contains(d, "scope") {
			return false, oauthErr, description
		}
		if clientThenRejection.MatchString(d) || rejectionThenClient.MatchString(d) {
			return true, oauthErr, description
		}
	}
	return false, oauthErr, description
}

// ProbeAuthorizationClient issues one GET to authURL without following
// redirects (a valid client gets a login/consent page or a redirect; only an
// invalid client gets a direct 400/401) and classifies the answer with
// ClassifyAuthorizeClientRejection. httpClient may be nil; its CheckRedirect
// is always overridden, and AuthorizeProbeTimeout applies unless the client
// sets a shorter Timeout. No cookies are sent, so no session is touched.
func ProbeAuthorizationClient(ctx context.Context, httpClient *http.Client, authURL string) AuthorizeProbeResult {
	u, err := url.Parse(authURL)
	if err != nil {
		return AuthorizeProbeResult{Err: err}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return AuthorizeProbeResult{Err: fmt.Errorf("authorization URL scheme %q is not probed", u.Scheme)}
	}

	hc := &http.Client{}
	if httpClient != nil {
		copied := *httpClient
		hc = &copied
	}
	hc.Jar = nil
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if hc.Timeout <= 0 || hc.Timeout > AuthorizeProbeTimeout {
		hc.Timeout = AuthorizeProbeTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, hc.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, http.NoBody)
	if err != nil {
		return AuthorizeProbeResult{Err: err}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return AuthorizeProbeResult{Err: err}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, authorizeProbeMaxBody))
	res := AuthorizeProbeResult{StatusCode: resp.StatusCode}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		res.Err = readErr
		return res
	}
	res.ClientRejected, res.OAuthError, res.ErrorDescription = ClassifyAuthorizeClientRejection(resp.StatusCode, body)
	return res
}
