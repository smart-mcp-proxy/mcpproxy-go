package oauthserver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 T032 — the fake OIDC IdP (research D2). Every ErrorMode tamper
// knob has its own sub-test so an unimplemented knob is a named failure.

const (
	oidcUser        = "alice@example.com"
	oidcPass        = "pass"
	oidcRedirectURI = "http://127.0.0.1/api/v1/auth/callback"
)

var oidcAliceClaims = map[string]any{
	"sub":            "alice-sub-1",
	"email":          "alice@example.com",
	"email_verified": true,
	"name":           "Alice Example",
	"groups":         []any{"eng", "sre"},
}

type oidcRig struct {
	t      *testing.T
	srv    *ServerResult
	client *http.Client // never follows redirects
}

func oidcOptions(mode ErrorMode) Options {
	return Options{
		OIDC:               true,
		ValidUsers:         map[string]string{oidcUser: oidcPass},
		UserClaims:         map[string]map[string]any{oidcUser: oidcAliceClaims},
		ClientRedirectURIs: []string{oidcRedirectURI},
		GroupsClaim:        "groups",
		ErrorMode:          mode,
	}
}

func startOIDC(t *testing.T, mode ErrorMode) *oidcRig {
	t.Helper()
	srv := Start(t, oidcOptions(mode))
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &oidcRig{
		t:   t,
		srv: srv,
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (r *oidcRig) discovery() map[string]any {
	r.t.Helper()
	resp, err := r.client.Get(r.srv.IssuerURL + "/.well-known/openid-configuration")
	require.NoError(r.t, err)
	defer resp.Body.Close()
	require.Equal(r.t, http.StatusOK, resp.StatusCode)
	var doc map[string]any
	require.NoError(r.t, json.NewDecoder(resp.Body).Decode(&doc))
	return doc
}

func (r *oidcRig) jwks() JWKS {
	r.t.Helper()
	resp, err := r.client.Get(r.srv.JWKSURL)
	require.NoError(r.t, err)
	defer resp.Body.Close()
	var set JWKS
	require.NoError(r.t, json.NewDecoder(resp.Body).Decode(&set))
	return set
}

func pkcePair() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(h[:])
}

func (r *oidcRig) authorizeQuery(nonce, challenge string) url.Values {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", r.srv.ClientID)
	q.Set("redirect_uri", oidcRedirectURI)
	q.Set("scope", "openid profile email groups")
	q.Set("state", "st-123")
	q.Set("nonce", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return q
}

// authorizeGET renders the login page (200) or redirects with an error.
func (r *oidcRig) authorizeGET(nonce, challenge string) *http.Response {
	r.t.Helper()
	resp, err := r.client.Get(r.srv.AuthorizationEndpoint + "?" + r.authorizeQuery(nonce, challenge).Encode())
	require.NoError(r.t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// authorizePOST submits the headless login form and returns the redirect Location.
func (r *oidcRig) authorizePOST(nonce, challenge string) *url.URL {
	r.t.Helper()
	form := r.authorizeQuery(nonce, challenge)
	form.Set("username", oidcUser)
	form.Set("password", oidcPass)
	form.Set("consent", "on")
	form.Set("action", "approve")
	resp, err := r.client.PostForm(r.srv.AuthorizationEndpoint, form)
	require.NoError(r.t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(r.t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(r.t, err)
	return loc
}

func (r *oidcRig) exchangeRaw(code, verifier string) *http.Response {
	r.t.Helper()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", oidcRedirectURI)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequest(http.MethodPost, r.srv.TokenEndpoint, strings.NewReader(form.Encode()))
	require.NoError(r.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(r.srv.ClientID, r.srv.ClientSecret)
	resp, err := r.client.Do(req)
	require.NoError(r.t, err)
	return resp
}

// login drives GET /authorize → POST /authorize → POST /token and returns the token response.
func (r *oidcRig) login(nonce string) map[string]any {
	r.t.Helper()
	verifier, challenge := pkcePair()
	require.Equal(r.t, http.StatusOK, r.authorizeGET(nonce, challenge).StatusCode, "redirect_uri from ClientRedirectURIs must be accepted")
	loc := r.authorizePOST(nonce, challenge)
	require.Equal(r.t, "st-123", loc.Query().Get("state"))
	code := loc.Query().Get("code")
	require.NotEmpty(r.t, code, "location=%s", loc)
	resp := r.exchangeRaw(code, verifier)
	defer resp.Body.Close()
	require.Equal(r.t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(r.t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func (r *oidcRig) userinfo(accessToken string) *http.Response {
	r.t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.srv.IssuerURL+"/userinfo", nil)
	require.NoError(r.t, err)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := r.client.Do(req)
	require.NoError(r.t, err)
	return resp
}

func (r *oidcRig) userinfoJSON(accessToken string) map[string]any {
	r.t.Helper()
	resp := r.userinfo(accessToken)
	defer resp.Body.Close()
	require.Equal(r.t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(r.t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func idTokenOf(t *testing.T, tok map[string]any) string {
	t.Helper()
	raw, _ := tok["id_token"].(string)
	require.NotEmpty(t, raw, "token response must carry id_token: %v", tok)
	return raw
}

// decodeJWT splits a compact JWS without verifying it.
func decodeJWT(t *testing.T, raw string) (header, claims map[string]any, sig string) {
	t.Helper()
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3, "compact JWS must have three parts: %q", raw)
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(hb, &header))
	require.NoError(t, json.Unmarshal(cb, &claims))
	return header, claims, parts[2]
}

// verifyAgainstJWKS verifies an RS256 token against the published JWKS by kid.
func (r *oidcRig) verifyAgainstJWKS(raw string) error {
	set := r.jwks()
	_, err := jwt.NewParser(jwt.WithoutClaimsValidation(), jwt.WithValidMethods([]string{"RS256"})).
		Parse(raw, func(tok *jwt.Token) (any, error) {
			kid, _ := tok.Header["kid"].(string)
			for _, k := range set.Keys {
				if k.Kid == kid && k.Kty == "RSA" {
					return jwkToRSAPublic(k)
				}
			}
			return nil, errors.New("kid not in JWKS")
		})
	return err
}

func jwkKty(set JWKS, kid string) string {
	for _, k := range set.Keys {
		if k.Kid == kid {
			return k.Kty
		}
	}
	return ""
}

func TestOIDC_HappyPath(t *testing.T) {
	r := startOIDC(t, ErrorMode{})

	t.Run("discovery", func(t *testing.T) {
		doc := r.discovery()
		assert.Equal(t, r.srv.IssuerURL+"/userinfo", doc["userinfo_endpoint"])
		assert.Contains(t, doc["id_token_signing_alg_values_supported"], "RS256")
		assert.Equal(t, r.srv.IssuerURL, doc["issuer"])
		assert.Equal(t, r.srv.JWKSURL, doc["jwks_uri"])
		assert.Contains(t, doc["scopes_supported"], "openid")
	})

	t.Run("jwks has RSA and EC keys", func(t *testing.T) {
		set := r.jwks()
		var kty []string
		for _, k := range set.Keys {
			kty = append(kty, k.Kty)
			assert.NotEmpty(t, k.Kid)
			if k.Kty == "EC" {
				assert.Equal(t, "P-256", k.Crv)
				assert.NotEmpty(t, k.X)
				assert.NotEmpty(t, k.Y)
				assert.Equal(t, "ES256", k.Alg)
			}
		}
		assert.Contains(t, kty, "RSA")
		assert.Contains(t, kty, "EC")
	})

	tok := r.login("nonce-abc")
	assert.Equal(t, "Bearer", tok["token_type"])
	raw := idTokenOf(t, tok)
	header, claims, _ := decodeJWT(t, raw)

	t.Run("id_token is RS256 by a JWKS key", func(t *testing.T) {
		assert.Equal(t, "RS256", header["alg"])
		assert.Equal(t, r.srv.Server.KeyRing().GetActiveKid(), header["kid"])
		require.NoError(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("id_token claims match UserClaims", func(t *testing.T) {
		assert.Equal(t, r.srv.IssuerURL, claims["iss"])
		assert.Equal(t, "alice-sub-1", claims["sub"])
		assert.Equal(t, r.srv.ClientID, claims["aud"])
		assert.Equal(t, r.srv.ClientID, claims["azp"])
		assert.Equal(t, "nonce-abc", claims["nonce"])
		assert.Equal(t, "alice@example.com", claims["email"])
		assert.Equal(t, true, claims["email_verified"])
		assert.Equal(t, "Alice Example", claims["name"])
		assert.Equal(t, []any{"eng", "sre"}, claims["groups"])
		exp, _ := claims["exp"].(float64)
		iat, _ := claims["iat"].(float64)
		assert.Greater(t, exp, float64(time.Now().Unix()))
		assert.LessOrEqual(t, iat, float64(time.Now().Unix()))
	})

	t.Run("userinfo returns the same claims", func(t *testing.T) {
		ui := r.userinfoJSON(tok["access_token"].(string))
		assert.Equal(t, "alice-sub-1", ui["sub"])
		assert.Equal(t, "alice@example.com", ui["email"])
		assert.Equal(t, true, ui["email_verified"])
		assert.Equal(t, "Alice Example", ui["name"])
		assert.Equal(t, []any{"eng", "sre"}, ui["groups"])
	})

	t.Run("userinfo rejects a bad token", func(t *testing.T) {
		resp := r.userinfo("not-a-token")
		resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer")
	})

	t.Run("kid rotation", func(t *testing.T) {
		oldKid := header["kid"].(string)
		newKid, err := r.srv.Server.KeyRing().RotateKey()
		require.NoError(t, err)
		require.NotEqual(t, oldKid, newKid)
		h2, _, _ := decodeJWT(t, idTokenOf(t, r.login("nonce-2")))
		assert.Equal(t, newKid, h2["kid"])
		set := r.jwks()
		assert.Equal(t, "RSA", jwkKty(set, oldKid), "old kid stays published for verification")
		assert.Equal(t, "RSA", jwkKty(set, newKid))
		require.NoError(t, r.verifyAgainstJWKS(raw), "token under the old kid still verifies")
	})
}

// TestOIDC_DefaultClaims: a ValidUsers entry without UserClaims still yields
// a usable identity (sub/email/name derived, email_verified true, no groups).
func TestOIDC_DefaultClaims(t *testing.T) {
	srv := Start(t, Options{OIDC: true, ClientRedirectURIs: []string{oidcRedirectURI}})
	t.Cleanup(func() { _ = srv.Shutdown() })
	r := &oidcRig{t: t, srv: srv, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	// default ValidUsers = testuser/testpass; log in as that user
	verifier, challenge := pkcePair()
	form := r.authorizeQuery("n", challenge)
	form.Set("username", "testuser")
	form.Set("password", "testpass")
	form.Set("consent", "on")
	form.Set("action", "approve")
	resp, err := r.client.PostForm(srv.AuthorizationEndpoint, form)
	require.NoError(t, err)
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	tr := r.exchangeRaw(loc.Query().Get("code"), verifier)
	defer tr.Body.Close()
	var body map[string]any
	require.NoError(t, json.NewDecoder(tr.Body).Decode(&body))
	_, claims, _ := decodeJWT(t, idTokenOf(t, body))
	assert.NotEmpty(t, claims["sub"])
	assert.Equal(t, "testuser@example.com", claims["email"])
	assert.Equal(t, true, claims["email_verified"])
	assert.NotEmpty(t, claims["name"])
	assert.Equal(t, "n", claims["nonce"])
	assert.Equal(t, []any{}, claims["groups"])
}

func TestOIDC_ErrorModeKnobs(t *testing.T) {
	now := func() float64 { return float64(time.Now().Unix()) }

	t.Run("IDTokenBadSignature", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenBadSignature: true})
		raw := idTokenOf(t, r.login("n"))
		header, _, _ := decodeJWT(t, raw)
		assert.Equal(t, "RS256", header["alg"])
		assert.Equal(t, "RSA", jwkKty(r.jwks(), header["kid"].(string)), "kid still resolves to a published key")
		assert.Error(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenWrongIssuer", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenWrongIssuer: true})
		raw := idTokenOf(t, r.login("n"))
		_, claims, _ := decodeJWT(t, raw)
		assert.NotEqual(t, r.srv.IssuerURL, claims["iss"])
		assert.NotEmpty(t, claims["iss"])
		assert.NoError(t, r.verifyAgainstJWKS(raw), "only the claim is wrong")
	})

	t.Run("IDTokenWrongAudience", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenWrongAudience: true})
		raw := idTokenOf(t, r.login("n"))
		_, claims, _ := decodeJWT(t, raw)
		assert.NotEqual(t, r.srv.ClientID, claims["aud"])
		assert.NotContains(t, []any{claims["aud"]}, r.srv.ClientID)
		assert.NoError(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenMultiAudNoAzp", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenMultiAudNoAzp: true})
		raw := idTokenOf(t, r.login("n"))
		_, claims, _ := decodeJWT(t, raw)
		aud, ok := claims["aud"].([]any)
		require.True(t, ok, "aud must be an array: %v", claims["aud"])
		assert.GreaterOrEqual(t, len(aud), 2)
		assert.Contains(t, aud, r.srv.ClientID)
		_, hasAzp := claims["azp"]
		assert.False(t, hasAzp, "azp must be absent")
		assert.NoError(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenExpired", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenExpired: true})
		raw := idTokenOf(t, r.login("n"))
		_, claims, _ := decodeJWT(t, raw)
		exp, _ := claims["exp"].(float64)
		assert.Less(t, exp, now()-120, "exp must be past the 60 s leeway")
		assert.NoError(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenNbfFuture", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenNbfFuture: true})
		raw := idTokenOf(t, r.login("n"))
		_, claims, _ := decodeJWT(t, raw)
		nbf, _ := claims["nbf"].(float64)
		assert.Greater(t, nbf, now()+120, "nbf must be past the 60 s leeway")
		exp, _ := claims["exp"].(float64)
		assert.Greater(t, exp, nbf)
		assert.NoError(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenNoNonce", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenNoNonce: true})
		raw := idTokenOf(t, r.login("n"))
		_, claims, _ := decodeJWT(t, raw)
		_, has := claims["nonce"]
		assert.False(t, has, "nonce must be absent")
		assert.NoError(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenAlgNone", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenAlgNone: true})
		raw := idTokenOf(t, r.login("n"))
		header, claims, sig := decodeJWT(t, raw)
		assert.Equal(t, "none", header["alg"])
		assert.Empty(t, sig)
		assert.Equal(t, "n", claims["nonce"])
		assert.Error(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenHS256", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenHS256: true})
		raw := idTokenOf(t, r.login("n"))
		header, _, sig := decodeJWT(t, raw)
		assert.Equal(t, "HS256", header["alg"])
		assert.NotEmpty(t, sig)
		assert.Error(t, r.verifyAgainstJWKS(raw))
	})

	t.Run("IDTokenUnknownKid", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenUnknownKid: true})
		raw := idTokenOf(t, r.login("n"))
		header, _, _ := decodeJWT(t, raw)
		kid, _ := header["kid"].(string)
		assert.NotEmpty(t, kid)
		assert.Equal(t, "", jwkKty(r.jwks(), kid), "kid must not be in the JWKS")
		assert.Equal(t, "RS256", header["alg"])
	})

	t.Run("IDTokenKeyAlgMismatch", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{IDTokenKeyAlgMismatch: true})
		raw := idTokenOf(t, r.login("n"))
		header, _, _ := decodeJWT(t, raw)
		assert.Equal(t, "ES256", header["alg"])
		kid, _ := header["kid"].(string)
		assert.Equal(t, "RSA", jwkKty(r.jwks(), kid), "header names an RSA kid with an EC alg")
	})

	t.Run("EmailVerifiedFalse", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{EmailVerifiedFalse: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		assert.Equal(t, false, claims["email_verified"])
		ui := r.userinfoJSON(tok["access_token"].(string))
		assert.Equal(t, false, ui["email_verified"])
	})

	t.Run("GroupsNonArray", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{GroupsNonArray: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, isStr := claims["groups"].(string)
		assert.True(t, isStr, "groups must be a non-array: %v", claims["groups"])
		ui := r.userinfoJSON(tok["access_token"].(string))
		_, isStr = ui["groups"].(string)
		assert.True(t, isStr)
	})

	t.Run("GroupsOverageMarker", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{GroupsOverageMarker: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, has := claims["groups"]
		assert.False(t, has, "groups absent when overage marker set")
		names, _ := claims["_claim_names"].(map[string]any)
		assert.Contains(t, names, "groups")
		_, hasSrc := claims["_claim_sources"]
		assert.True(t, hasSrc)
		ui := r.userinfoJSON(tok["access_token"].(string))
		_, has = ui["groups"]
		assert.False(t, has)
		uiNames, _ := ui["_claim_names"].(map[string]any)
		assert.Contains(t, uiNames, "groups")
	})

	t.Run("GroupsAbsentEverywhere", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{GroupsAbsentEverywhere: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, has := claims["groups"]
		assert.False(t, has)
		ui := r.userinfoJSON(tok["access_token"].(string))
		_, has = ui["groups"]
		assert.False(t, has)
		assert.Equal(t, claims["sub"], ui["sub"])
	})

	t.Run("UserinfoSubMismatch", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{UserinfoSubMismatch: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, has := claims["groups"]
		assert.False(t, has, "token must lack groups so the verifier consults userinfo")
		ui := r.userinfoJSON(tok["access_token"].(string))
		assert.NotEqual(t, claims["sub"], ui["sub"])
		assert.NotEmpty(t, ui["sub"])
		assert.Equal(t, []any{"eng", "sre"}, ui["groups"])
	})

	t.Run("UserinfoRedirect", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{UserinfoRedirect: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, has := claims["groups"]
		assert.False(t, has)
		resp := r.userinfo(tok["access_token"].(string))
		resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode)
		loc, err := url.Parse(resp.Header.Get("Location"))
		require.NoError(t, err)
		assert.True(t, loc.IsAbs())
		assert.NotEqual(t, r.srv.Server.addr, loc.Host, "redirect must leave the issuer origin")
	})

	t.Run("UserinfoNonJSON", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{UserinfoNonJSON: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, has := claims["groups"]
		assert.False(t, has)
		resp := r.userinfo(tok["access_token"].(string))
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "json")
		var any map[string]any
		assert.Error(t, json.Unmarshal(body, &any), "body must not be JSON: %s", body)
	})

	t.Run("UserinfoUnavailable", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{UserinfoUnavailable: true})
		tok := r.login("n")
		_, claims, _ := decodeJWT(t, idTokenOf(t, tok))
		_, has := claims["groups"]
		assert.False(t, has)
		resp := r.userinfo(tok["access_token"].(string))
		resp.Body.Close()
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("DiscoveryHTTPTokenEndpoint", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{DiscoveryHTTPTokenEndpoint: true})
		doc := r.discovery()
		te, _ := doc["token_endpoint"].(string)
		u, err := url.Parse(te)
		require.NoError(t, err, te)
		assert.Equal(t, "http", u.Scheme)
		assert.NotContains(t, []string{"127.0.0.1", "localhost", "::1"}, u.Hostname(), "must not be loopback (loopback http is allowed by allow_insecure_issuer)")
		assert.NotEqual(t, r.srv.Server.addr, u.Host)
	})

	t.Run("TokenEndpointRedirect", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{TokenEndpointRedirect: true})
		verifier, challenge := pkcePair()
		loc := r.authorizePOST("n", challenge)
		resp := r.exchangeRaw(loc.Query().Get("code"), verifier)
		resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode)
		target, err := url.Parse(resp.Header.Get("Location"))
		require.NoError(t, err)
		assert.True(t, target.IsAbs())
		assert.NotEqual(t, r.srv.Server.addr, target.Host, "redirect must leave the issuer origin")
	})

	t.Run("AuthAccessDenied", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{AuthAccessDenied: true})
		_, challenge := pkcePair()
		resp := r.authorizeGET("n", challenge)
		assert.Equal(t, http.StatusFound, resp.StatusCode)
		loc, err := url.Parse(resp.Header.Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "access_denied", loc.Query().Get("error"))
		assert.Equal(t, "st-123", loc.Query().Get("state"))
	})

	// A real IdP decides an injected authorization-time error at the
	// authorization request, before any login form is shown, so it must
	// apply the same way to a caller that POSTs straight to the authorize
	// endpoint (simulating the form submission) without ever GETting it
	// first — scripts/dev-server-edition.sh's headless_login does exactly
	// that. Before this fix, handleAuthorizePOST never checked
	// ErrorMode.AuthAccessDenied/AuthInvalidRequest at all, so a POST-only
	// caller silently bypassed both knobs (cross-review round 6, chunk 4 P3).
	t.Run("AuthAccessDenied on a POST-only caller (no preceding GET)", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{AuthAccessDenied: true})
		_, challenge := pkcePair()
		loc := r.authorizePOST("n", challenge)
		assert.Equal(t, "access_denied", loc.Query().Get("error"))
		assert.Equal(t, "st-123", loc.Query().Get("state"))
	})

	t.Run("AuthInvalidRequest on a POST-only caller (no preceding GET)", func(t *testing.T) {
		r := startOIDC(t, ErrorMode{AuthInvalidRequest: true})
		_, challenge := pkcePair()
		loc := r.authorizePOST("n", challenge)
		assert.Equal(t, "invalid_request", loc.Query().Get("error"))
		assert.Equal(t, "st-123", loc.Query().Get("state"))
	})
}

// TestOIDC_ExistingBehaviourUntouched: with OIDC off nothing changes — no
// id_token, no userinfo route, no EC key in the JWKS.
func TestOIDC_ExistingBehaviourUntouched(t *testing.T) {
	srv := Start(t, Options{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	resp, err := http.Get(srv.IssuerURL + "/.well-known/openid-configuration")
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	resp.Body.Close()
	_, has := doc["userinfo_endpoint"]
	assert.False(t, has)
	_, has = doc["id_token_signing_alg_values_supported"]
	assert.False(t, has)

	resp, err = http.Get(srv.IssuerURL + "/userinfo")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// jwkToRSAPublic converts a published RSA JWK back into a public key (test-local, stdlib only).
func jwkToRSAPublic(k JWK) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
}

// TestOIDC_LoginFormCarriesNonce: a browser submits the rendered form (not
// the query string), so the nonce must ride a hidden field.
func TestOIDC_LoginFormCarriesNonce(t *testing.T) {
	r := startOIDC(t, ErrorMode{})
	_, challenge := pkcePair()
	resp, err := r.client.Get(r.srv.AuthorizationEndpoint + "?" + r.authorizeQuery("nonce-form-1", challenge).Encode())
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), `name="nonce" value="nonce-form-1"`)
}
