package oauthserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// handleUserinfo handles GET/POST /userinfo (OpenID Connect Core §5.3).
// Registered only when Options.OIDC is on. It answers the identity claims of
// the user behind a valid access token, or 401 with a Bearer challenge.
func (s *OAuthTestServer) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.userinfoUnauthorized(w, "invalid_request", "Bearer token required")
		return
	}
	username, ok := s.accessTokenSubject(strings.TrimPrefix(authHeader, "Bearer "))
	if !ok {
		s.userinfoUnauthorized(w, "invalid_token", "Access token is invalid or expired")
		return
	}

	// Tamper knobs (research D2): each is one transport defect.
	mode := s.errorMode()
	switch {
	case mode.UserinfoUnavailable:
		http.Error(w, "userinfo temporarily unavailable", http.StatusServiceUnavailable)
		return
	case mode.UserinfoRedirect:
		w.Header().Set("Location", "https://userinfo.invalid/userinfo")
		w.WriteHeader(http.StatusFound)
		return
	case mode.UserinfoNonJSON:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>not json</body></html>"))
		return
	}

	claims := s.identityClaims(username)
	if mode.UserinfoSubMismatch {
		sub, _ := claims["sub"].(string)
		claims["sub"] = "someone-else-" + sub
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(claims); err != nil {
		http.Error(w, "Failed to encode userinfo", http.StatusInternalServerError)
	}
}

// userinfoUnauthorized answers 401 with an RFC 6750 challenge.
func (s *OAuthTestServer) userinfoUnauthorized(w http.ResponseWriter, code, description string) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="userinfo", error="%s", error_description="%s"`, code, description))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(OAuthError{Error: code, ErrorDescription: description})
}

// accessTokenSubject verifies an access token issued by this server (RS256,
// key looked up by kid so tokens survive rotation) and returns its subject —
// the ValidUsers username the code was authorised as.
func (s *OAuthTestServer) accessTokenSubject(tokenStr string) (string, bool) {
	claims := &TokenClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		if kid, _ := t.Header["kid"].(string); kid != "" {
			if key, ok := s.keyRing.GetKey(kid); ok {
				return &key.PublicKey, nil
			}
		}
		return s.keyRing.GetPublicKey(), nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(s.issuerURL))
	if err != nil || !tok.Valid || claims.Subject == "" {
		return "", false
	}
	return claims.Subject, true
}
