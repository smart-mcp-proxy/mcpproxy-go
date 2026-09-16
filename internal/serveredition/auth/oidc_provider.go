//go:build server

package auth

// The generic `oidc` provider (Spec 107 FR-020..FR-022, research D3/D4):
// OpenID Connect Discovery, a JWKS cache, the token exchange with the client
// authentication method discovery advertises, and a VERIFIED ID token —
// signature by kid, allowed algorithms, exp/nbf/iat with 60 s skew, exact
// iss, aud/azp, nonce — before any claim is read. Groups come from the token,
// or from userinfo only when the token lacks the claim and userinfo's `sub`
// equals the verified token's `sub`.
//
// Every back-channel request (discovery, JWKS, token, userinfo) goes through
// one client that never follows redirects and times out after 10 s; any 3xx
// is provider_error. Discovery is lazy — nothing is fetched at construction
// — so boot and readiness never depend on the IdP.

import (
	"context"
	"crypto"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

const (
	oidcBackChannelTimeout = 10 * time.Second
	oidcClockSkew          = 60 * time.Second
	oidcCacheTTLMin        = 5 * time.Minute
	oidcCacheTTLMax        = 24 * time.Hour
	oidcCacheTTLDefault    = time.Hour
	oidcMaxBodyBytes       = 1 << 20

	clientAuthBasic = "client_secret_basic"
	clientAuthPost  = "client_secret_post"
)

// oidcAllowedAlgs is the closed set an ID token may be signed with (FR-021):
// asymmetric only, never `none`, never HS*.
var oidcAllowedAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512"}

// oidcError is a classified back-channel or verification failure: the closed
// reason the handler reports, the name of the check that failed (logged, never
// a claim value) and the underlying cause.
type oidcError struct {
	reason LoginRefusal
	check  string
	err    error
}

func (e *oidcError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s: %v", e.reason, e.check, e.err)
	}
	return fmt.Sprintf("%s: %s", e.reason, e.check)
}

func (e *oidcError) Unwrap() error { return e.err }

func newOIDCError(reason LoginRefusal, check string, err error) *oidcError {
	return &oidcError{reason: reason, check: check, err: err}
}

// discoveryDoc is the subset of OpenID Provider Metadata the login needs.
type discoveryDoc struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`

	clientAuth  string   // clientAuthBasic | clientAuthPost, chosen once
	allowedAlgs []string // intersection with oidcAllowedAlgs
	fetchedAt   time.Time
	expiresAt   time.Time
}

// oidcProvider holds the per-issuer state: the configuration it was built
// from, the non-redirecting client and the discovery/JWKS caches.
type oidcProvider struct {
	cfg    *config.ServerEditionOAuthConfig
	client *http.Client
	now    func() time.Time

	mu          sync.Mutex
	disc        *discoveryDoc
	jwks        map[string]crypto.PublicKey
	jwksExpires time.Time
}

func newOIDCProvider(cfg *config.ServerEditionOAuthConfig) *oidcProvider {
	return &oidcProvider{
		cfg: cfg,
		// Transport stays nil (http.DefaultTransport) on purpose: tests observe
		// the back channel by swapping the default transport.
		client: &http.Client{
			Timeout: oidcBackChannelTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

// idTokenClaims are the verified claims the handler consumes.
type idTokenClaims struct {
	Subject       string
	Email         string
	EmailVerified *bool
	Name          string
	Picture       string
	raw           map[string]any
}

// --- back channel ------------------------------------------------------------

// get performs one non-redirecting GET and returns the body of a 200 with the
// response's cache TTL; any transport failure, 3xx or non-200 is
// provider_error.
func (p *oidcProvider) get(ctx context.Context, what, endpoint, bearer string) ([]byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, newOIDCError(LoginProviderError, what+" request", err)
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, newOIDCError(LoginProviderError, what+" request failed", redactURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcMaxBodyBytes))
	if err != nil {
		return nil, 0, newOIDCError(LoginProviderError, what+" response read", err)
	}
	if resp.StatusCode/100 == 3 {
		return nil, 0, newOIDCError(LoginProviderError, what+" endpoint answered a redirect", fmt.Errorf("status %d", resp.StatusCode))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, newOIDCError(LoginProviderError, what+" endpoint status", fmt.Errorf("status %d", resp.StatusCode))
	}
	return body, cacheTTL(resp.Header.Get("Cache-Control")), nil
}

// redactURLError strips the URL (which may carry query values) from a
// transport error so the log line names only the failure class.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// cacheTTL derives a cache lifetime from Cache-Control max-age, clamped to
// [5 min, 24 h]; a missing or unparsable directive yields the default.
func cacheTTL(cacheControl string) time.Duration {
	ttl := oidcCacheTTLDefault
	for _, directive := range strings.Split(cacheControl, ",") {
		directive = strings.TrimSpace(strings.ToLower(directive))
		if strings.HasPrefix(directive, "max-age=") {
			if secs, err := strconv.Atoi(strings.TrimPrefix(directive, "max-age=")); err == nil {
				ttl = time.Duration(secs) * time.Second
			}
		}
	}
	return min(max(ttl, oidcCacheTTLMin), oidcCacheTTLMax)
}

// --- discovery -----------------------------------------------------------

// discover returns the cached discovery document, fetching and validating it
// when absent or expired. A rejected document is never cached.
func (p *oidcProvider) discover(ctx context.Context) (*discoveryDoc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disc != nil && p.now().Before(p.disc.expiresAt) {
		return p.disc, nil
	}
	doc, err := p.fetchDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	p.disc = doc
	return doc, nil
}

func (p *oidcProvider) fetchDiscovery(ctx context.Context) (*discoveryDoc, error) {
	endpoint := strings.TrimSuffix(p.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	body, ttl, err := p.get(ctx, "discovery", endpoint, "")
	if err != nil {
		return nil, err
	}
	var doc discoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, newOIDCError(LoginDiscoveryFailed, "discovery document is not JSON", err)
	}
	if doc.Issuer != p.cfg.IssuerURL {
		return nil, newOIDCError(LoginDiscoveryFailed, "discovery issuer does not equal issuer_url", nil)
	}
	for name, ep := range map[string]string{
		"authorization_endpoint": doc.AuthorizationEndpoint,
		"token_endpoint":         doc.TokenEndpoint,
		"jwks_uri":               doc.JWKSURI,
	} {
		if ep == "" {
			return nil, newOIDCError(LoginDiscoveryFailed, "discovery document lacks "+name, nil)
		}
		if !config.IsAllowedOIDCEndpoint(ep, p.cfg.AllowInsecureIssuer) {
			return nil, newOIDCError(LoginDiscoveryFailed, "discovery "+name+" is not an absolute https URL", nil)
		}
	}
	if doc.UserinfoEndpoint != "" && !config.IsAllowedOIDCEndpoint(doc.UserinfoEndpoint, p.cfg.AllowInsecureIssuer) {
		return nil, newOIDCError(LoginDiscoveryFailed, "discovery userinfo_endpoint is not an absolute https URL", nil)
	}
	switch {
	case doc.TokenEndpointAuthMethodsSupported == nil, slices.Contains(doc.TokenEndpointAuthMethodsSupported, clientAuthBasic):
		doc.clientAuth = clientAuthBasic // the OIDC Discovery §3 default when absent
	case slices.Contains(doc.TokenEndpointAuthMethodsSupported, clientAuthPost):
		doc.clientAuth = clientAuthPost
	default:
		return nil, newOIDCError(LoginDiscoveryFailed, "token_endpoint_auth_methods_supported offers neither client_secret_basic nor client_secret_post", nil)
	}
	advertised := doc.IDTokenSigningAlgValuesSupported
	if advertised == nil {
		advertised = []string{"RS256"} // mandatory-to-implement (OIDC Discovery §3)
	}
	for _, alg := range oidcAllowedAlgs {
		if slices.Contains(advertised, alg) {
			doc.allowedAlgs = append(doc.allowedAlgs, alg)
		}
	}
	if len(doc.allowedAlgs) == 0 {
		return nil, newOIDCError(LoginDiscoveryFailed, "id_token_signing_alg_values_supported has no allowed asymmetric algorithm", nil)
	}
	doc.fetchedAt = p.now()
	doc.expiresAt = doc.fetchedAt.Add(ttl)
	return &doc, nil
}

// --- authorization URL and token exchange --------------------------------------

// authorizationURL builds the authorization request with state, nonce and
// PKCE S256 against the discovered authorization_endpoint.
func (p *oidcProvider) authorizationURL(ctx context.Context, callbackURL, state, nonce, codeChallenge string) (string, error) {
	doc, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	// Parse and merge into the endpoint's own query rather than blindly
	// appending "?" + params.Encode(): an authorization_endpoint that already
	// carries a query component (e.g. a tenant/realm selector) would otherwise
	// produce "?tenant=acme?client_id=..." — a single malformed query string
	// the IdP cannot parse, so client_id/state/nonce/PKCE never arrive
	// (cross-review round 2, chunk 1 P2).
	u, err := url.Parse(doc.AuthorizationEndpoint)
	if err != nil {
		return "", newOIDCError(LoginDiscoveryFailed, "authorization_endpoint is not a valid URL", err)
	}
	q := u.Query()
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", callbackURL)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// exchangeCode redeems the authorization code once, with the client
// authentication method discovery advertised.
func (p *oidcProvider) exchangeCode(ctx context.Context, code, callbackURL, codeVerifier string) (*TokenResponse, error) {
	doc, err := p.discover(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {callbackURL},
		"code_verifier": {codeVerifier},
	}
	if doc.clientAuth == clientAuthPost {
		form.Set("client_id", p.cfg.ClientID)
		form.Set("client_secret", p.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, newOIDCError(LoginProviderError, "token request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if doc.clientAuth == clientAuthBasic {
		// RFC 6749 §2.3.1: credentials are form-encoded before Basic.
		req.SetBasicAuth(url.QueryEscape(p.cfg.ClientID), url.QueryEscape(p.cfg.ClientSecret))
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, newOIDCError(LoginProviderError, "token request failed", redactURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcMaxBodyBytes))
	if err != nil {
		return nil, newOIDCError(LoginProviderError, "token response read", err)
	}
	if resp.StatusCode/100 == 3 {
		return nil, newOIDCError(LoginProviderError, "token endpoint answered a redirect", fmt.Errorf("status %d", resp.StatusCode))
	}
	if resp.StatusCode != http.StatusOK {
		// Only a known RFC 6749 §5.2 error code is surfaced — never the raw
		// field, which is IdP-controlled and could carry log-injection
		// content (control characters, multi-line text) or echo request
		// inputs (cross-review round 1, chunk 1 P2).
		var oauthErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &oauthErr)
		return nil, newOIDCError(LoginProviderError, "token endpoint status", fmt.Errorf("status %d error %q", resp.StatusCode, sanitizeOAuthErrorCode(oauthErr.Error)))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, newOIDCError(LoginProviderError, "token response is not JSON", err)
	}
	if tok.IDToken == "" {
		return nil, newOIDCError(LoginIDTokenInvalid, "token response carries no id_token", nil)
	}
	return &tok, nil
}

// oauthErrorCodes is the closed RFC 6749 §5.2 token-endpoint error vocabulary.
// sanitizeOAuthErrorCode clamps anything else to "unknown" so an IdP-controlled
// `error` field can never reach the log or the wire verbatim (cross-review
// round 1, chunk 1 P2: the raw field could carry control characters or echo
// request inputs).
var oauthErrorCodes = map[string]bool{
	"invalid_request":        true,
	"invalid_client":         true,
	"invalid_grant":          true,
	"unauthorized_client":    true,
	"unsupported_grant_type": true,
	"invalid_scope":          true,
}

func sanitizeOAuthErrorCode(code string) string {
	if oauthErrorCodes[code] {
		return code
	}
	return "unknown"
}

// --- JWKS -----------------------------------------------------------------

// keyForKid resolves a signing key from the JWKS cache, fetching the set at
// most once per call: when the cache is cold or expired, or once more when
// the kid is unknown to a warm cache. An unknown kid after that is refused.
func (p *oidcProvider) keyForKid(ctx context.Context, doc *discoveryDoc, kid string) (crypto.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jwks != nil && p.now().Before(p.jwksExpires) {
		if key, ok := p.jwks[kid]; ok {
			return key, nil
		}
	}
	body, ttl, err := p.get(ctx, "jwks", doc.JWKSURI, "")
	if err != nil {
		return nil, err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return nil, newOIDCError(LoginProviderError, "jwks document", err)
	}
	p.jwks, p.jwksExpires = keys, p.now().Add(ttl)
	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, newOIDCError(LoginIDTokenInvalid, "id_token kid is not in the JWKS after one refetch", nil)
}

// --- ID token verification ---------------------------------------------------

// verifyIDToken turns the raw id_token into verified claims (Definitions:
// verified ID token). Order: signing method ∈ allowed algs → key by kid →
// signature → exp/nbf/iat (60 s skew) → exact iss → aud/azp → nonce.
func (p *oidcProvider) verifyIDToken(ctx context.Context, raw, nonce string) (*idTokenClaims, error) {
	doc, err := p.discover(ctx)
	if err != nil {
		return nil, err
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods(doc.allowedAlgs), jwt.WithoutClaimsValidation())
	_, err = parser.ParseWithClaims(raw, claims, func(tok *jwt.Token) (any, error) {
		kid, _ := tok.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("id_token header has no kid")
		}
		return p.keyForKid(ctx, doc, kid)
	})
	if err != nil {
		var oerr *oidcError
		if errors.As(err, &oerr) {
			return nil, oerr
		}
		return nil, newOIDCError(LoginIDTokenInvalid, "id_token signature or signing method", err)
	}

	validator := jwt.NewValidator(jwt.WithLeeway(oidcClockSkew), jwt.WithIssuedAt(), jwt.WithExpirationRequired())
	if err := validator.Validate(claims); err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, newOIDCError(LoginTokenExpired, "id_token exp", err)
		}
		return nil, newOIDCError(LoginIDTokenInvalid, "id_token exp/nbf/iat", err)
	}
	// jwt.WithIssuedAt() validates iat only when present; it does not make
	// the claim mandatory (there is no WithIssuedAtRequired in v5). `iat` is
	// a REQUIRED ID Token claim per OIDC Core §2, so its absence must be
	// refused explicitly (cross-review round 1, chunk 1 P2).
	if _, hasIat := claims["iat"]; !hasIat {
		return nil, newOIDCError(LoginIDTokenInvalid, "id_token has no iat", nil)
	}

	if iss, _ := claims["iss"].(string); iss != p.cfg.IssuerURL {
		return nil, newOIDCError(LoginIssuerMismatch, "id_token iss does not equal issuer_url", nil)
	}
	auds, audOK := audienceList(claims["aud"])
	if !audOK {
		return nil, newOIDCError(LoginAudienceMismatch, "id_token aud contains a non-string entry", nil)
	}
	if !slices.Contains(auds, p.cfg.ClientID) {
		return nil, newOIDCError(LoginAudienceMismatch, "id_token aud does not contain client_id", nil)
	}
	azp, hasAzp := claims["azp"].(string)
	if len(auds) > 1 && !hasAzp {
		return nil, newOIDCError(LoginAudienceMismatch, "id_token has multiple aud values and no azp", nil)
	}
	if hasAzp && azp != p.cfg.ClientID {
		return nil, newOIDCError(LoginAudienceMismatch, "id_token azp does not equal client_id", nil)
	}
	gotNonce, _ := claims["nonce"].(string)
	if nonce == "" || gotNonce == "" || subtle.ConstantTimeCompare([]byte(gotNonce), []byte(nonce)) != 1 {
		return nil, newOIDCError(LoginNonceMismatch, "id_token nonce does not equal the nonce bound to the state", nil)
	}

	out := &idTokenClaims{raw: claims}
	out.Subject, _ = claims["sub"].(string)
	if out.Subject == "" {
		return nil, newOIDCError(LoginIDTokenInvalid, "id_token has no sub", nil)
	}
	out.Email, _ = claims["email"].(string)
	out.Name, _ = claims["name"].(string)
	out.Picture, _ = claims["picture"].(string)
	if v, ok := claims["email_verified"].(bool); ok {
		out.EmailVerified = &v
	}
	return out, nil
}

// audienceList normalises the `aud` claim (string or array of strings). ok is
// false when the claim is an array containing a non-string entry: silently
// dropping such an entry would undercount the audience and could let a
// malformed multi-aud token skip the azp check that array length is meant to
// trigger (cross-review round 1, chunk 1 P2) — the token is refused instead.
func audienceList(v any) (auds []string, ok bool) {
	switch a := v.(type) {
	case string:
		return []string{a}, true
	case []any:
		out := make([]string, 0, len(a))
		for _, item := range a {
			s, isString := item.(string)
			if !isString {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return a, true
	}
	return nil, true
}

// --- groups -----------------------------------------------------------------

// groupsFromClaims reads the configured groups claim: present reports the
// key exists, ok reports it is a flat array of strings or a single string.
func groupsFromClaims(claims map[string]any, name string) (groups []string, present, ok bool) {
	v, present := claims[name]
	if !present {
		return nil, false, false
	}
	switch g := v.(type) {
	case string:
		return []string{g}, true, true
	case []any:
		out := make([]string, 0, len(g))
		for _, item := range g {
			s, isString := item.(string)
			if !isString {
				return nil, true, false
			}
			out = append(out, s)
		}
		return out, true, true
	case []string:
		return append([]string{}, g...), true, true
	}
	return nil, true, false
}

// hasOverageMarker reports an Entra "groups overage" claim reference
// (`_claim_names` naming the groups claim) — the groups are not in the token.
func hasOverageMarker(claims map[string]any, name string) bool {
	names, ok := claims["_claim_names"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = names[name]
	return ok
}

// resolveGroups returns the verified groups for the login (FR-008): from the
// ID token; from userinfo only when the token lacks the claim and userinfo's
// sub equals the verified token's sub. missing reports the fail-closed
// `groups_claim_missing` case (absent everywhere, malformed shape, overage
// marker) in which groups is [].
func (p *oidcProvider) resolveGroups(ctx context.Context, claims *idTokenClaims, accessToken string) (groups []string, missing bool, err error) {
	name := p.cfg.GroupsClaim
	if hasOverageMarker(claims.raw, name) {
		return []string{}, true, nil
	}
	if g, present, ok := groupsFromClaims(claims.raw, name); present {
		if ok {
			return g, false, nil
		}
		return []string{}, true, nil
	}

	doc, err := p.discover(ctx)
	if err != nil {
		return nil, false, err
	}
	if doc.UserinfoEndpoint == "" || accessToken == "" {
		return []string{}, true, nil
	}
	body, _, err := p.get(ctx, "userinfo", doc.UserinfoEndpoint, accessToken)
	if err != nil {
		return nil, false, err
	}
	var info map[string]any
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, false, newOIDCError(LoginProviderError, "userinfo response is not JSON", err)
	}
	if info == nil {
		// A JSON scalar (`null`, a number, a string, an array) decodes to a
		// nil map without an Unmarshal error; that is not a JSON object and
		// must fail closed as provider_error (FR-022), never be read as
		// "sub absent" (cross-review round 1, chunk 1 P3).
		return nil, false, newOIDCError(LoginProviderError, "userinfo response is not a JSON object", nil)
	}
	// No userinfo claim is read before its sub is compared with the verified
	// token's sub (FR-022).
	if sub, _ := info["sub"].(string); sub == "" || sub != claims.Subject {
		return nil, false, newOIDCError(LoginUserinfoSubjectMismatch, "userinfo sub does not equal the id_token sub", nil)
	}
	if g, present, ok := groupsFromClaims(info, name); present && ok {
		return g, false, nil
	}
	return []string{}, true, nil
}
