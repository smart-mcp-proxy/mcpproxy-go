package server

import (
	"encoding/json"
	"errors"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 132: the public OAuth endpoints of the browser login for MCP clients
// (contracts/oauth-endpoints.md). Everything here runs behind oauthLoginGate,
// so it answers only when the feature is on, the peer and the Host are
// loopback and the build is the personal edition. The handlers parse the wire
// format and map outcomes to status codes; every decision is the service's
// (runtime.OAuthLoginService).

// oauthLoginSvc returns the runtime's browser-login service, or nil.
func (s *Server) oauthLoginSvc() *runtime.OAuthLoginService {
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.OAuthLoginService()
}

// oauthLoginLive reports whether browser login is on now or was ever used:
// the two states in which an mcp_oat_ token is refused outside its own
// address. Off and never used it is false, so nothing changes.
func (s *Server) oauthLoginLive() bool {
	if cfg := s.liveConfig(); cfg != nil && cfg.MCPOAuthEnabled {
		return true
	}
	return s.oauthLoginEverUsedNow()
}

func (s *Server) oauthRateLimiter() *mcplogin.Limiter {
	s.oauthLimiterOnce.Do(func() { s.oauthLimiter = mcplogin.NewLimiter(nil) })
	return s.oauthLimiter
}

// oauthLoginPublicHandler dispatches /oauth/* and /.well-known/oauth-* once the
// gate admitted the request.
func (s *Server) oauthLoginPublicHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc := s.oauthLoginSvc()
		if svc == nil {
			http.NotFound(w, r)
			return
		}
		path := r.URL.Path
		switch {
		case path == "/oauth/register":
			s.oauthRegister(w, r, svc)
		case path == "/oauth/authorize":
			s.oauthAuthorize(w, r, svc)
		case path == "/oauth/token":
			s.oauthToken(w, r, svc)
		case path == "/.well-known/oauth-authorization-server":
			s.oauthASMetadata(w, r)
		case strings.HasPrefix(path, oauthProtectedResourceWellKnown):
			s.oauthResourceMetadata(w, r, svc)
		default:
			http.NotFound(w, r)
		}
	})
}

const oauthProtectedResourceWellKnown = "/.well-known/oauth-protected-resource"

// --- helpers -------------------------------------------------------------------

func oauthNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func oauthWriteJSON(w http.ResponseWriter, status int, v any) {
	oauthNoStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthJSONError(w http.ResponseWriter, status int, code, desc string) {
	oauthWriteJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func oauthMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	oauthJSONError(w, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
}

// oauthRemoteIP is the rate-limit key of the peer.
func oauthRemoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// oauthAllow charges the endpoint's budget and answers 429 when it is spent.
func (s *Server) oauthAllow(w http.ResponseWriter, r *http.Request, endpoint string, asHTML bool) bool {
	ok, retry := s.oauthRateLimiter().Allow(endpoint, oauthRemoteIP(r))
	if ok {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	if asHTML {
		oauthErrorPage(w, http.StatusTooManyRequests, "Too many requests", "Try again in a little while.")
	} else {
		oauthJSONError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many requests")
	}
	return false
}

// oauthErrorPage is the HTML answer of the authorize endpoint when the request
// cannot be redirected back to the client. No external assets, no script.
func oauthErrorPage(w http.ResponseWriter, status int, title, detail string) {
	oauthNoStore(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title></head><body style=\"font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem\"><h1>" +
		html.EscapeString(title) + "</h1><p>" + html.EscapeString(detail) + "</p></body></html>"))
}

// oauthMediaType reports whether the request's Content-Type is want (a media
// type; parameters such as charset are allowed).
func oauthMediaType(r *http.Request, want string) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == want
}

// oauthCrossSite refuses (403, and reports true) a state-changing public
// request that a web page in the administrator's browser sent to the loopback
// address: an Origin that is not this request's own origin, or
// Sec-Fetch-Site: cross-site. Native clients send neither header. It is not
// applied to the authorize GET, which is a top-level browser navigation.
func oauthCrossSite(w http.ResponseWriter, r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != mcplogin.Origin(r) {
		oauthJSONError(w, http.StatusForbidden, "access_denied", "cross-origin requests are not accepted")
		return true
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		oauthJSONError(w, http.StatusForbidden, "access_denied", "cross-site requests are not accepted")
		return true
	}
	return false
}

// --- metadata --------------------------------------------------------------------

func (s *Server) oauthASMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		oauthMethodNotAllowed(w, "GET, HEAD")
		return
	}
	oauthWriteJSON(w, http.StatusOK, mcplogin.AuthorizationServerMetadata(mcplogin.Origin(r)))
}

// oauthResourceMetadata serves /.well-known/oauth-protected-resource/mcp/oauth
// and /…/mcp/oauth/{client} (RFC 9728). An unknown client id is 404, like the
// address itself.
func (s *Server) oauthResourceMetadata(w http.ResponseWriter, r *http.Request, svc *runtime.OAuthLoginService) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		oauthMethodNotAllowed(w, "GET, HEAD")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, oauthProtectedResourceWellKnown)
	clientID, generic, ok := mcplogin.ParseResourcePath(suffix)
	if !ok || (!generic && !svc.ClientExists(clientID)) {
		http.NotFound(w, r)
		return
	}
	oauthWriteJSON(w, http.StatusOK, mcplogin.ProtectedResourceMetadata(mcplogin.Origin(r), clientID))
}

// --- register --------------------------------------------------------------------

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request, svc *runtime.OAuthLoginService) {
	if r.Method != http.MethodPost {
		oauthMethodNotAllowed(w, "POST")
		return
	}
	if oauthCrossSite(w, r) || !s.oauthAllow(w, r, mcplogin.EndpointRegister, false) {
		return
	}
	if !oauthMediaType(r, "application/json") {
		oauthJSONError(w, http.StatusUnsupportedMediaType, "invalid_request", "the body must be application/json")
		return
	}
	var req mcplogin.RegistrationRequest
	body := http.MaxBytesReader(w, r.Body, mcplogin.MaxBodyBytes)
	dec := json.NewDecoder(body)
	err := dec.Decode(&req)
	if err == nil {
		// Exactly one JSON value: whatever follows it (a second value, or
		// padding that pushes the body past the limit) is refused.
		var extra json.RawMessage
		if err = dec.Decode(&extra); errors.Is(err, io.EOF) {
			err = nil
		} else if err == nil {
			oauthJSONError(w, http.StatusBadRequest, mcplogin.ErrCodeInvalidClientMetadata, "the body must be a single JSON object")
			return
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			oauthJSONError(w, http.StatusRequestEntityTooLarge, mcplogin.ErrCodeInvalidClientMetadata, "the request is too large")
			return
		}
		oauthJSONError(w, http.StatusBadRequest, mcplogin.ErrCodeInvalidClientMetadata, "the body must be a JSON object")
		return
	}
	resp, regErr, err := svc.Register(req)
	switch {
	case errors.Is(err, runtime.ErrOAuthFeatureDisabled):
		http.NotFound(w, r)
	case err != nil:
		s.logger.Error("browser login: registration failed", zap.Error(err))
		oauthJSONError(w, http.StatusInternalServerError, "server_error", "the server could not complete the request")
	case regErr != nil:
		oauthJSONError(w, http.StatusBadRequest, regErr.Code, regErr.Description)
	default:
		oauthWriteJSON(w, http.StatusCreated, resp)
	}
}

// --- authorize ---------------------------------------------------------------------

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request, svc *runtime.OAuthLoginService) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		oauthErrorPage(w, http.StatusMethodNotAllowed, "Method not allowed", "Use GET.")
		return
	}
	if !s.oauthAllow(w, r, mcplogin.EndpointAuthorize, true) {
		return
	}
	q := r.URL.Query()
	origin := mcplogin.Origin(r)
	id, aerr := svc.BeginAuthorization(runtime.AuthorizeInput{
		Origin: origin, ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), State: q.Get("state"),
		CodeChallenge: q.Get("code_challenge"), Method: q.Get("code_challenge_method"),
		Resource: q.Get("resource"), ResponseType: q.Get("response_type"),
	})
	if aerr != nil {
		switch aerr.Kind {
		case runtime.AuthorizeNotFound:
			oauthErrorPage(w, http.StatusNotFound, "Not found", "This sign-in address is not known.")
		case runtime.AuthorizeRedirect:
			target, err := aerr.RedirectURL()
			if err != nil {
				oauthErrorPage(w, http.StatusBadRequest, "Sign-in request refused", "The request could not be completed.")
				return
			}
			oauthNoStore(w)
			http.Redirect(w, r, target, http.StatusFound)
		default:
			oauthErrorPage(w, http.StatusBadRequest, "Sign-in request refused", aerr.Description)
		}
		return
	}
	oauthNoStore(w)
	http.Redirect(w, r, origin+"/ui/authorize?request="+url.QueryEscape(id), http.StatusFound)
}

// --- token ---------------------------------------------------------------------------

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request, svc *runtime.OAuthLoginService) {
	if r.Method != http.MethodPost {
		oauthMethodNotAllowed(w, "POST")
		return
	}
	if oauthCrossSite(w, r) || !s.oauthAllow(w, r, mcplogin.EndpointToken, false) {
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/x-www-form-urlencoded") {
		oauthJSONError(w, http.StatusBadRequest, "invalid_request", "the body must be application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcplogin.MaxBodyBytes)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			oauthJSONError(w, http.StatusRequestEntityTooLarge, "invalid_request", "the request is too large")
			return
		}
		oauthJSONError(w, http.StatusBadRequest, "invalid_request", "the body could not be read")
		return
	}
	f := r.PostForm
	in := runtime.TokenInput{
		Origin: mcplogin.Origin(r), Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"), ClientID: f.Get("client_id"),
		CodeVerifier: f.Get("code_verifier"), RefreshToken: f.Get("refresh_token"), Resource: f.Get("resource"),
	}
	var (
		res  runtime.TokenResult
		terr *runtime.TokenError
	)
	switch f.Get("grant_type") {
	case "authorization_code":
		res, terr = svc.Exchange(in)
	case "refresh_token":
		res, terr = svc.Refresh(in)
	default:
		oauthJSONError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
		return
	}
	if terr != nil {
		status := http.StatusBadRequest
		if terr.Code == runtime.TokenErrServer {
			status = http.StatusInternalServerError
		}
		oauthJSONError(w, status, terr.Code, terr.Description)
		return
	}
	oauthWriteJSON(w, http.StatusOK, res)
}

// --- the MCP resource ------------------------------------------------------------------

// oauthBearer returns the credential of an Authorization header that uses the
// Bearer scheme (case-insensitive), else "". Only this header counts on an
// OAuth address: X-API-Key and ?apikey= are never read there.
func oauthBearer(h string) string {
	const scheme = "Bearer "
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(h[len(scheme):])
}

// oauthResourceAuth is the authentication of /mcp/oauth and /mcp/oauth/{client}
// (research R2). It accepts only a live mcp_oat_ access token for exactly this
// origin and address, asks the service on EVERY request (so revoke, forget,
// switch-off and expiry take effect on the next request of an open session) and
// never falls back to the anonymous context: any other credential, or none, is
// 401 with the protected-resource challenge. An unknown client id is 404.
func (s *Server) oauthResourceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc := s.oauthLoginSvc()
		clientID, generic, ok := mcplogin.ParseResourcePath(r.URL.Path)
		if svc == nil || !ok || (!generic && !svc.ClientExists(clientID)) {
			http.NotFound(w, r)
			return
		}
		origin := mcplogin.Origin(r)
		unauthorized := func() {
			w.Header().Set("WWW-Authenticate", mcplogin.Challenge(origin, clientID))
			oauthNoStore(w)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Sign in required. Authenticate with an OAuth access token for this address."}`))
		}
		token := oauthBearer(r.Header.Get("Authorization"))
		if token == "" {
			unauthorized()
			return
		}
		// The switch-off epoch is read BEFORE authenticating, so a switch-off
		// that lands in between makes this request older than the epoch.
		run := s.oauthRunGen()
		epoch := svc.DisableEpoch()
		if h := s.testHookAfterEpochCapture; h != nil {
			h()
		}
		ac, grant, err := svc.Authenticate(token, origin, r.URL.Path, time.Now())
		if err != nil {
			unauthorized()
			return
		}
		svc.Touch(grant.GrantID, "")
		// The request (an SSE stream can live for hours) ends when the login is
		// revoked, its client forgotten, the feature switched off, its tokens
		// rotated or its access token expires.
		s.serveTracked(w, r.WithContext(auth.WithAuthContext(r.Context(), ac)), mcpRequestMiddlewares(next), grant.GrantID, grant.AccessExpiresAt, epoch, run)
	})
}
