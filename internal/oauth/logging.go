// Package oauth provides OAuth 2.1 authentication support for MCP servers.
// This file implements enhanced logging utilities with sensitive data redaction.
package oauth

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Sensitive header names that should be redacted in logs.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"cookie":              true,
	"set-cookie":          true,
	"x-access-token":      true,
	"x-refresh-token":     true,
	"x-auth-token":        true,
	"proxy-authorization": true,
}

// Sensitive parameter names in request bodies or URLs.
var sensitiveParams = []string{
	"access_token",
	"refresh_token",
	"client_secret",
	"code",
	"password",
	"token",
	"id_token",
	"assertion",
	// Issue #872: Azure-SAS-style URL credentials. sig/signature are not
	// caught by secretPattern (which keys off secret|password|token|key), so
	// list them explicitly here — this list feeds RedactSensitiveData/RedactURL
	// (the free-form last_error / health.detail scrubbers) as well as
	// sensitiveQueryParams below.
	"sig",
	"signature",
	// Issue #872 (Codex review round): AWS SigV4 credential param. The regex
	// scrubbers (RedactURL / RedactSensitiveData) match `<param>=` as a suffix,
	// so "signature" already covers X-Amz-Signature and "token" covers
	// authToken / X-Amz-Security-Token; "credential" closes the remaining
	// X-Amz-Credential gap in the free-form error/health.detail scrubbers.
	"credential",
}

// tokenPattern matches Bearer tokens and other sensitive token patterns.
var tokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[a-zA-Z0-9\-_\.]+`)

// secretPattern matches common secret patterns.
var secretPattern = regexp.MustCompile(`(?i)(secret|password|token|key)["']?\s*[:=]\s*["']?[a-zA-Z0-9\-_\.]+`)

// RedactSensitiveData redacts sensitive information from a string.
// It replaces tokens, secrets, and other sensitive data with redacted placeholders.
func RedactSensitiveData(data string) string {
	if data == "" {
		return data
	}

	// Redact Bearer tokens
	result := tokenPattern.ReplaceAllString(data, "${1}***REDACTED***")

	// Redact secrets and passwords
	result = secretPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Find the position of = or : and redact everything after
		for _, sep := range []string{"=", ":"} {
			if idx := strings.Index(match, sep); idx != -1 {
				return match[:idx+1] + "***REDACTED***"
			}
		}
		return "***REDACTED***"
	})

	// Redact sensitive URL parameters
	for _, param := range sensitiveParams {
		pattern := regexp.MustCompile(`(?i)(` + param + `=)[^&\s]+`)
		result = pattern.ReplaceAllString(result, "${1}***REDACTED***")
	}

	return result
}

// RedactHeaders creates a copy of headers with sensitive values redacted.
// Returns a map suitable for logging.
func RedactHeaders(headers http.Header) map[string]string {
	redacted := make(map[string]string)

	for key, values := range headers {
		lowerKey := strings.ToLower(key)
		if sensitiveHeaders[lowerKey] {
			redacted[key] = "***REDACTED***"
		} else {
			// Join multiple values and redact any sensitive data within
			value := strings.Join(values, ", ")
			redacted[key] = RedactSensitiveData(value)
		}
	}

	return redacted
}

// RedactStringHeaders is the map[string]string analogue of RedactHeaders, for
// the per-server config form (cfg.Headers) used in the upstream_servers MCP
// tool and the /api/v1/servers REST response. Returns a new map; the input
// is not mutated. Returns nil for nil input so JSON callers can keep emitting
// `null` rather than `{}` if they were doing so before.
//
// Sensitive header values are replaced with a length-preserving mask of
// the form `••••<last2> (<N> chars)` — the same format the Web UI and
// macOS tray apply to display literals. This gives all callers a single
// uniform representation: clients render whatever string the API hands
// back, no `***REDACTED***`-vs-`••••XX`-vs-plaintext branching.
//
// Carrying the length and last two characters is intentional. They are
// already exposed indirectly (length via response size analysis, tail
// via prior history, etc.), they materially help operators identify
// which token is in use without revealing the secret, and they make the
// "Convert to secret" affordance work on the UI side because the user
// can confirm a recognisable suffix before approving.
func RedactStringHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	redacted := make(map[string]string, len(headers))
	for key, value := range headers {
		lowerKey := strings.ToLower(key)
		if sensitiveHeaders[lowerKey] {
			redacted[key] = MaskValue(value)
		} else {
			redacted[key] = RedactSensitiveData(value)
		}
	}
	return redacted
}

// sensitiveEnvMarkers are substrings that, when present in an env var name
// (case-insensitive), mark its value as a secret to be masked. The list is
// deliberately broad — masking a non-secret value is safe (it just becomes
// less readable), whereas leaking a real secret is not — but the markers are
// specific enough to leave ordinary configuration (LOG_LEVEL, HOME, NODE_ENV,
// HTTP_PROXY, …) readable. Covers API_KEY/APIKEY (KEY), PASSWORD/PASSWD/PASS,
// CREDENTIAL, AUTH, BEARER, PRIVATE, CERT.
// Issue #872 (Codex review round): DSN / CONNECTION_STRING / CONN_STR keys hold
// database connection strings whose embedded password must be masked whole —
// they don't contain any of the other markers. (DATABASE_URL / REDIS_URL are
// deliberately NOT markers: they are handled by the URL-aware default branch in
// RedactEnvValues so scheme/host/db stay readable.)
var sensitiveEnvMarkers = []string{
	"TOKEN", "SECRET", "KEY", "PASSWORD", "PASSWD", "PASS",
	"CREDENTIAL", "AUTH", "BEARER", "PRIVATE", "CERT",
	"DSN", "CONNECTION_STRING", "CONN_STR",
}

// isSensitiveEnvKey reports whether an env var name looks like it holds a
// secret, based on a case-insensitive substring match against
// sensitiveEnvMarkers.
func isSensitiveEnvKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range sensitiveEnvMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// RedactEnvValues is the env-var analogue of RedactStringHeaders, for the
// per-server config `env` map surfaced by the upstream_servers MCP tool, the
// /api/v1/servers REST response, and the SSE event stream. Returns a new map;
// the input is not mutated. Returns nil for nil input so JSON callers keep
// emitting `null` rather than `{}` (same back-compat contract as
// RedactStringHeaders).
//
// Values under a sensitive-looking key (see isSensitiveEnvKey) are replaced
// with MaskValue — which passes ${env:...}/${keyring:...} references through
// unchanged and renders literals as `••••<last2> (<N> chars)`. Non-sensitive
// keys stay readable so operators can still see LOG_LEVEL, NODE_ENV, etc.,
// with a RedactSensitiveData pass over the value as a defence-in-depth fallback
// for embedded secrets (it leaves ordinary values like `debug` untouched).
func RedactEnvValues(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	redacted := make(map[string]string, len(env))
	for key, value := range env {
		redacted[key] = maskedEnvValue(key, value)
	}
	return redacted
}

// maskedEnvValue is the single source of truth for how RedactEnvValues renders
// one (key, value) pair. It is reused by UnmaskEnvValues on the write path so a
// value that was echoed back masked can be recognised and reverted.
//
// Sensitive-looking keys mask the whole value. For every other key the value is
// still run through RedactSensitiveData (defence in depth), and — Issue #872 —
// when it parses as a connection URL (postgres://user:pass@host/db,
// redis://:pass@host, …) it is passed through RedactURLQueryParams so the
// embedded userinfo password and any credential query params are masked while
// scheme/host/db stay readable.
func maskedEnvValue(key, value string) string {
	if isSensitiveEnvKey(key) {
		return MaskValue(value)
	}
	if strings.Contains(value, "://") {
		return RedactURLQueryParams(value)
	}
	return RedactSensitiveData(value)
}

// normalizeParamName folds a query-parameter (or env) name to a canonical form
// for sensitivity matching: lowercase with '-' and '_' separators stripped.
// This makes X-Amz-Signature, x_amz_signature and xamzsignature all compare
// equal, and access-token equal to access_token (Issue #872, Codex round).
func normalizeParamName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(name) {
		if r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sensitiveQueryParams holds the NORMALIZED (see normalizeParamName) query
// parameter names whose values are masked by RedactURLQueryParams. It extends
// the base sensitiveParams set (used by the log redactors) with the parameter
// names commonly seen carrying credentials in MCP / presigned-URL query strings.
// Because matching is done on the normalized name, exact-name misses such as
// X-Amz-Credential, X-Amz-Signature, X-Amz-Security-Token, authToken and
// access-token are all covered.
var sensitiveQueryParams = func() map[string]bool {
	names := make([]string, 0, len(sensitiveParams)+10)
	names = append(names, sensitiveParams...) // token, sig, signature, credential, …
	names = append(names,
		"apikey", "api_key", "key", "secret",
		"authtoken",
		"x-amz-credential", "x-amz-signature", "x-amz-security-token",
		"security-token", "session-token",
	)
	m := make(map[string]bool, len(names))
	for _, p := range names {
		m[normalizeParamName(p)] = true
	}
	return m
}()

// isSensitiveQueryParam reports whether a query-parameter name (in its raw,
// possibly percent-decoded form) is sensitive, matched on its normalized form.
func isSensitiveQueryParam(name string) bool {
	return sensitiveQueryParams[normalizeParamName(name)]
}

// RedactURLQueryParams masks the values of sensitive query parameters in a URL
// while leaving the rest of the URL — path, non-sensitive params, and any
// ${env:...}/${keyring:...} reference values — verbatim. Unlike RedactURL
// (regex, log-oriented, emits `***REDACTED***`) it parses with net/url and
// masks with MaskValue, giving the same client-facing representation as the
// header/env redactors. References are passed through unchanged because they
// are labels, not secrets. A URL with no query, or no sensitive params, is
// returned unchanged. On parse failure it falls back to the regex RedactURL.
func RedactURLQueryParams(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return RedactURL(rawURL)
	}
	changed := false

	// Issue #872: basic-auth credentials embedded in the URL userinfo
	// (https://user:pass@host) are just as sensitive as a query secret. Mask
	// the password, keep the username. u.String() writes RawQuery verbatim, so
	// re-encoding the userinfo does not disturb the query bytes we hand-edit
	// below.
	if u.User != nil {
		if pw, hasPW := u.User.Password(); hasPW && !isConfigReference(pw) {
			u.User = url.UserPassword(u.User.Username(), MaskValue(pw))
			changed = true
		}
	}

	// Edit RawQuery by hand rather than via url.Values.Encode(): Encode
	// re-percent-encodes and reorders every parameter, which would mangle
	// reference values like ${env:NAME} into an unrecognizable form and
	// defeat the UI's keyring-chip detection. Here only the masked value
	// changes; untouched parameters keep their exact original bytes.
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		queryChanged := false
		for i, part := range parts {
			eq := strings.IndexByte(part, '=')
			if eq < 0 {
				continue
			}
			key := part[:eq]
			decKey, keyErr := url.QueryUnescape(key)
			if keyErr != nil {
				decKey = key
			}
			if !isSensitiveQueryParam(decKey) {
				continue
			}
			decVal, valErr := url.QueryUnescape(part[eq+1:])
			if valErr != nil {
				decVal = part[eq+1:]
			}
			if isConfigReference(decVal) {
				continue
			}
			parts[i] = key + "=" + url.QueryEscape(MaskValue(decVal))
			queryChanged = true
		}
		if queryChanged {
			u.RawQuery = strings.Join(parts, "&")
			changed = true
		}
	}

	if !changed {
		return rawURL
	}
	return u.String()
}

// configRefPattern matches a value that is ENTIRELY a single ${keyring:NAME} or
// ${env:VAR} reference. Anchored on both ends (Issue #872, Codex round) so a
// composite like `${env:NAME}garbage` — which a prefix check would wave through
// unmasked — is treated as a secret. Mirrors internal/secret's canonical
// ${type:name} shape, restricted to the two reference types the masker honours.
var configRefPattern = regexp.MustCompile(`^\$\{(?:keyring|env):[^}]+\}$`)

// isConfigReference reports whether the given value is a complete
// ${keyring:NAME} or ${env:VAR} reference. These aren't secrets — they
// are public labels pointing at the actual secret store — so the
// backend masker passes them through unchanged. A value that merely starts
// with a reference but has extra bytes is NOT a reference and is masked.
func isConfigReference(v string) bool {
	return configRefPattern.MatchString(v)
}

// MaskValue renders a string secret as `••••<last2> (<N> chars)` for
// human display. Returns "(empty)" for empty input, a 4-bullet preview
// for values up to 4 characters (where revealing the last two would
// leak too much), and `${keyring:NAME}` / `${env:VAR}` reference strings
// pass through unchanged because they are labels, not secrets — the UI
// renders them as keyring chips and a masked reference would defeat
// that detection. The format mirrors what the Web UI / macOS tray apply
// client-side for env vars and other non-redacted-by-backend literals,
// so a single rendering path produces a uniform look.
func MaskValue(v string) string {
	if v == "" {
		return "(empty)"
	}
	if isConfigReference(v) {
		return v
	}
	if len(v) <= 4 {
		return "••••"
	}
	return "••••" + v[len(v)-2:] + " (" + strconv.Itoa(len(v)) + " chars)"
}

// UnmaskURL protects the write path from a client that echoes a masked URL
// back (Issue #872, Codex round). The read path (RedactURLQueryParams) renders
// sensitive query-param values and the userinfo password with MaskValue; a
// client editing only a non-secret part of a URL — the tray sends `url` as a
// single string, not a field-level diff — would otherwise persist the mask over
// the real credential.
//
// For each sensitive query param (and the userinfo password) in incoming, if
// its value is exactly MaskValue(<stored value>), the stored real value is
// substituted so the secret survives. Genuinely edited values (anything that is
// not the mask of the stored value) are kept verbatim. If stored is empty or
// either URL fails to parse, incoming is returned unchanged.
func UnmaskURL(incoming, stored string) string {
	if incoming == "" || stored == "" {
		return incoming
	}
	inU, err := url.Parse(incoming)
	if err != nil {
		return incoming
	}
	stU, err := url.Parse(stored)
	if err != nil {
		return incoming
	}
	changed := false

	// Userinfo password: restore if the incoming password is the mask of the
	// stored one.
	if inU.User != nil {
		if inPW, hasPW := inU.User.Password(); hasPW && stU.User != nil {
			if stPW, stHas := stU.User.Password(); stHas && inPW == MaskValue(stPW) {
				inU.User = url.UserPassword(inU.User.Username(), stPW)
				changed = true
			}
		}
	}

	// Query params: hand-edit RawQuery (never url.Values.Encode, which reorders
	// and re-escapes) so untouched params keep their exact original bytes,
	// mirroring RedactURLQueryParams.
	if inU.RawQuery != "" {
		storedRawByKey := map[string]string{} // decoded key -> stored raw value bytes
		for _, part := range strings.Split(stU.RawQuery, "&") {
			eq := strings.IndexByte(part, '=')
			if eq < 0 {
				continue
			}
			dk, e := url.QueryUnescape(part[:eq])
			if e != nil {
				dk = part[:eq]
			}
			storedRawByKey[dk] = part[eq+1:]
		}

		parts := strings.Split(inU.RawQuery, "&")
		queryChanged := false
		for i, part := range parts {
			eq := strings.IndexByte(part, '=')
			if eq < 0 {
				continue
			}
			key := part[:eq]
			decKey, keyErr := url.QueryUnescape(key)
			if keyErr != nil {
				decKey = key
			}
			if !isSensitiveQueryParam(decKey) {
				continue
			}
			storedRaw, ok := storedRawByKey[decKey]
			if !ok {
				continue
			}
			decIn, e := url.QueryUnescape(part[eq+1:])
			if e != nil {
				decIn = part[eq+1:]
			}
			decStored, e := url.QueryUnescape(storedRaw)
			if e != nil {
				decStored = storedRaw
			}
			if decIn == MaskValue(decStored) {
				// Restore the stored value's exact original bytes.
				parts[i] = key + "=" + storedRaw
				queryChanged = true
			}
		}
		if queryChanged {
			inU.RawQuery = strings.Join(parts, "&")
			changed = true
		}
	}

	if !changed {
		return incoming
	}
	return inU.String()
}

// UnmaskEnvValues is the env-map analogue of UnmaskURL. For each key in incoming
// whose value is exactly the masked rendering of the stored value
// (maskedEnvValue), the stored real value is substituted; every other value is
// kept as sent. Guards against a client echoing a masked `env` map back on the
// write path (Issue #872, Codex round). Returns incoming unchanged for nil.
func UnmaskEnvValues(incoming, stored map[string]string) map[string]string {
	return unmaskMapValues(incoming, stored, maskedEnvValue)
}

// UnmaskHeaders is the header-map analogue of UnmaskEnvValues.
func UnmaskHeaders(incoming, stored map[string]string) map[string]string {
	return unmaskMapValues(incoming, stored, maskedHeaderValue)
}

// maskedHeaderValue is the single source of truth for how RedactStringHeaders
// renders one (key, value) pair; reused by UnmaskHeaders to recognise echoed
// masks.
func maskedHeaderValue(key, value string) string {
	if sensitiveHeaders[strings.ToLower(key)] {
		return MaskValue(value)
	}
	return RedactSensitiveData(value)
}

// unmaskMapValues reverts values that equal the masked rendering of their
// stored counterpart. rendered(key, storedValue) must reproduce exactly what the
// read path emitted for that pair.
func unmaskMapValues(incoming, stored map[string]string, rendered func(k, v string) string) map[string]string {
	if incoming == nil {
		return incoming
	}
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		if sv, ok := stored[k]; ok && v == rendered(k, sv) {
			out[k] = sv
		} else {
			out[k] = v
		}
	}
	return out
}

// RedactURL redacts sensitive query parameters from a URL string.
func RedactURL(urlStr string) string {
	if urlStr == "" {
		return urlStr
	}

	result := urlStr
	for _, param := range sensitiveParams {
		pattern := regexp.MustCompile(`(?i)(` + param + `=)[^&]+`)
		result = pattern.ReplaceAllString(result, "${1}***REDACTED***")
	}

	return result
}

// LogOAuthRequest logs an OAuth HTTP request with redacted sensitive data.
// Use at debug level for comprehensive request tracing.
func LogOAuthRequest(logger *zap.Logger, method, url string, headers http.Header) {
	logger.Debug("OAuth HTTP request",
		zap.String("method", method),
		zap.String("url", RedactURL(url)),
		zap.Any("headers", RedactHeaders(headers)),
		zap.Time("timestamp", time.Now()),
	)
}

// LogOAuthResponse logs an OAuth HTTP response with redacted sensitive data.
// Use at debug level for comprehensive response tracing.
func LogOAuthResponse(logger *zap.Logger, statusCode int, headers http.Header, duration time.Duration) {
	logger.Debug("OAuth HTTP response",
		zap.Int("status_code", statusCode),
		zap.String("status", http.StatusText(statusCode)),
		zap.Any("headers", RedactHeaders(headers)),
		zap.Duration("duration", duration),
		zap.Time("timestamp", time.Now()),
	)
}

// LogOAuthResponseError logs an OAuth HTTP response error.
func LogOAuthResponseError(logger *zap.Logger, statusCode int, errorMsg string, duration time.Duration) {
	logger.Warn("OAuth HTTP response error",
		zap.Int("status_code", statusCode),
		zap.String("status", http.StatusText(statusCode)),
		zap.String("error", RedactSensitiveData(errorMsg)),
		zap.Duration("duration", duration),
	)
}

// TokenMetadata contains non-sensitive token information for logging.
type TokenMetadata struct {
	TokenType       string
	ExpiresAt       time.Time
	ExpiresIn       time.Duration
	Scope           string
	HasRefreshToken bool
}

// LogTokenMetadata logs token metadata without exposing actual token values.
// Safe to use at info level as no sensitive data is included.
func LogTokenMetadata(logger *zap.Logger, metadata TokenMetadata) {
	logger.Info("OAuth token metadata",
		zap.String("token_type", metadata.TokenType),
		zap.Time("expires_at", metadata.ExpiresAt),
		zap.Duration("expires_in", metadata.ExpiresIn),
		zap.String("scope", metadata.Scope),
		zap.Bool("has_refresh_token", metadata.HasRefreshToken),
	)
}

// LogClientConnectionAttempt logs a client connection attempt (not an actual token refresh).
// Note: This is called when retrying client.Start(), which may trigger automatic
// token refresh internally by mcp-go, but we cannot observe whether refresh actually occurred.
func LogClientConnectionAttempt(logger *zap.Logger, attempt int, maxAttempts int) {
	logger.Info("OAuth client connection attempt",
		zap.Int("attempt", attempt),
		zap.Int("max_attempts", maxAttempts),
	)
}

// LogClientConnectionSuccess logs a successful client connection.
// Note: This does NOT mean a token refresh occurred - it means the client connected.
// The mcp-go library may have used a cached token or performed automatic refresh internally.
func LogClientConnectionSuccess(logger *zap.Logger, duration time.Duration) {
	logger.Info("OAuth client connection successful",
		zap.Duration("duration", duration),
	)
}

// LogClientConnectionFailure logs a failed client connection attempt.
func LogClientConnectionFailure(logger *zap.Logger, attempt int, err error) {
	logger.Warn("OAuth client connection failed",
		zap.Int("attempt", attempt),
		zap.Error(err),
	)
}

// Deprecated: Use LogClientConnectionAttempt instead.
// LogTokenRefreshAttempt is kept for backward compatibility but is misleading.
func LogTokenRefreshAttempt(logger *zap.Logger, attempt int, maxAttempts int) {
	LogClientConnectionAttempt(logger, attempt, maxAttempts)
}

// Deprecated: Use LogClientConnectionSuccess instead.
// LogTokenRefreshSuccess is kept for backward compatibility but is misleading.
// This is called when client.Start() succeeds, not when a token refresh occurs.
func LogTokenRefreshSuccess(logger *zap.Logger, duration time.Duration) {
	LogClientConnectionSuccess(logger, duration)
}

// Deprecated: Use LogClientConnectionFailure instead.
// LogTokenRefreshFailure is kept for backward compatibility but is misleading.
func LogTokenRefreshFailure(logger *zap.Logger, attempt int, err error) {
	LogClientConnectionFailure(logger, attempt, err)
}

// LogActualTokenRefreshAttempt logs an actual proactive token refresh attempt.
// This is called by RefreshManager when it initiates a token refresh operation.
func LogActualTokenRefreshAttempt(logger *zap.Logger, serverName string, tokenAge time.Duration) {
	logger.Info("OAuth token refresh attempt",
		zap.String("server", serverName),
		zap.Duration("token_age", tokenAge),
	)
}

// LogActualTokenRefreshResult logs the result of an actual token refresh operation.
// This is called by RefreshManager after a refresh attempt completes.
func LogActualTokenRefreshResult(logger *zap.Logger, serverName string, success bool, duration time.Duration, err error) {
	if success {
		logger.Info("OAuth token refresh succeeded",
			zap.String("server", serverName),
			zap.Duration("duration", duration),
		)
	} else {
		logger.Warn("OAuth token refresh failed",
			zap.String("server", serverName),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
	}
}

// LogOAuthFlowStart logs the start of an OAuth flow.
func LogOAuthFlowStart(logger *zap.Logger, serverName string, correlationID string) {
	logger.Info("Starting OAuth flow",
		zap.String("server", serverName),
		zap.String("correlation_id", correlationID),
		zap.Time("start_time", time.Now()),
	)
}

// LogOAuthFlowEnd logs the end of an OAuth flow.
func LogOAuthFlowEnd(logger *zap.Logger, serverName string, correlationID string, success bool, duration time.Duration) {
	if success {
		logger.Info("OAuth flow completed successfully",
			zap.String("server", serverName),
			zap.String("correlation_id", correlationID),
			zap.Duration("total_duration", duration),
		)
	} else {
		logger.Warn("OAuth flow failed",
			zap.String("server", serverName),
			zap.String("correlation_id", correlationID),
			zap.Duration("total_duration", duration),
		)
	}
}
