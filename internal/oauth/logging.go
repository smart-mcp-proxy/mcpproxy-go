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

// Mask MARKERS — the substrings this package's own mask renderings are built
// from. They exist as constants, and every renderer below is written in terms
// of them, because the fail-closed write-path net has to RECOGNISE what the
// read doors rendered: MaskMarkers in redactview.go is assembled from exactly
// these constants (plus security's), so a rendering cannot be added or changed
// without the net learning about it in the same edit.
//
// Issue #1148, round 7 finding 1: the marker list used to be hand-maintained
// beside the renderers and did not know about `***REDACTED***`, so an echoed
// RedactSensitiveData rendering was accepted on a write and persisted over the
// live credential.
const (
	// redactedMarker is what the REGEX scrubbers (RedactSensitiveData,
	// RedactURL, RedactHeaders) put in place of a matched secret.
	redactedMarker = "***REDACTED***"

	// bulletMarker opens every MaskValue / AuditMaskValue rendering.
	bulletMarker = "••••"
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

// sensitiveHeaderSegments catch credential-bearing custom header names that
// cannot be enumerated exhaustively (for example access_token or
// X-Client-Credential). Matching complete delimiter-separated segments avoids
// false positives such as X-Author-ID and X-Monkey-ID.
var sensitiveHeaderSegments = map[string]bool{
	"auth":          true,
	"authorization": true,
	"bearer":        true,
	"cookie":        true,
	"token":         true,
	"secret":        true,
	"key":           true,
	"apikey":        true,
	"password":      true,
	"passwd":        true,
	"credential":    true,
	"private":       true,
	"session":       true,
}

var headerNameSegmentPattern = regexp.MustCompile(`[a-z0-9]+`)

// headerNameCamelSegmentPattern splits a CamelCase header name on its case
// transitions. Issue #1146 (review round 3): the delimiter-based pass above
// only ever sees whole `-`/`_`-separated segments, so `X-AuthToken` collapsed
// to the single segment "authtoken", matched nothing, and a credential under a
// custom header name was recorded and logged in the clear. Splitting on case
// transitions as well recovers "Auth" and "Token" without reopening the
// substring false positives the segment rule exists to prevent: "Author" and
// "Monkey" are still single segments that equal no marker.
//
// Only mixed-case words are extracted here — an all-caps run like
// `X-CUSTOM-TOKEN` has no case transition to split on and is already covered by
// the delimiter pass.
var headerNameCamelSegmentPattern = regexp.MustCompile(`[A-Z][a-z0-9]*`)

// IsSensitiveHeaderName reports whether an HTTP header NAME looks like it
// carries a credential. Exported for callers that must decide masking from a
// name they hold apart from the value (the activity/audit redactor, issue
// #1146) so the rule stays defined in exactly one place.
func IsSensitiveHeaderName(name string) bool {
	return isSensitiveHeaderKey(name)
}

func isSensitiveHeaderKey(name string) bool {
	if sensitiveHeaders[strings.ToLower(name)] {
		return true
	}

	for _, segment := range headerNameSegmentPattern.FindAllString(strings.ToLower(name), -1) {
		if sensitiveHeaderSegments[segment] {
			return true
		}
	}

	for _, segment := range headerNameCamelSegmentPattern.FindAllString(name, -1) {
		if sensitiveHeaderSegments[strings.ToLower(segment)] {
			return true
		}
	}

	return false
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

// urlUserinfoPattern matches the `scheme://user:password@` prefix of a URL
// embedded in free-form text (issue #1148, review round 2).
//
// RedactURLQueryParams has masked the userinfo password since #872, but that
// function only ever sees a value the caller already knows is a URL. The
// free-form scrubbers — connection errors, tailed log lines, health.detail —
// see a URL buried in a sentence, and neither tokenPattern (bearer only) nor
// secretPattern (`<name>=<value>`) nor the `<param>=` sweep below has any
// `user:pass@` shape, so a basic-auth password travelled through all of them in
// the clear.
//
// The username is deliberately kept: it is the operator's signal for WHICH
// credential failed, and it is not the secret.
var urlUserinfoPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)([^\s/:@]+):([^\s/@]+)@`)

// redactURLUserinfo masks the password half of every `scheme://user:pass@`
// prefix in s, keeping the username. Shared by RedactSensitiveData (free-form
// text) and RedactURL (the parse-failure fallback) so the two cannot disagree
// about whether a basic-auth password is a secret.
func redactURLUserinfo(s string) string {
	return urlUserinfoPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := urlUserinfoPattern.FindStringSubmatch(match)
		if len(groups) != 4 {
			return match
		}
		// A ${keyring:…}/${env:…} reference is a label, not a secret; masking
		// it would erase exactly what makes the line diagnosable.
		if isConfigReference(groups[3]) {
			return match
		}
		return groups[1] + groups[2] + ":" + redactedMarker + "@"
	})
}

// RedactSensitiveData redacts sensitive information from a string.
// It replaces tokens, secrets, and other sensitive data with redacted placeholders.
func RedactSensitiveData(data string) string {
	if data == "" {
		return data
	}

	// Redact URL userinfo passwords (scheme://user:pass@host).
	result := redactURLUserinfo(data)

	// Redact Bearer tokens
	result = tokenPattern.ReplaceAllString(result, "${1}"+redactedMarker)

	// Redact secrets and passwords
	result = secretPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Find the position of = or : and redact everything after
		for _, sep := range []string{"=", ":"} {
			if idx := strings.Index(match, sep); idx != -1 {
				return match[:idx+1] + redactedMarker
			}
		}
		return redactedMarker
	})

	// Redact sensitive URL parameters
	for _, param := range sensitiveParams {
		pattern := regexp.MustCompile(`(?i)(` + param + `=)[^&\s]+`)
		result = pattern.ReplaceAllString(result, "${1}"+redactedMarker)
	}

	return result
}

// RedactHeaders creates a copy of headers with sensitive values redacted.
// Returns a map suitable for logging.
func RedactHeaders(headers http.Header) map[string]string {
	redacted := make(map[string]string)

	for key, values := range headers {
		if isSensitiveHeaderKey(key) {
			redacted[key] = redactedMarker
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
	return RedactStringHeadersWith(headers, MaskValue)
}

// RedactStringHeadersWith is RedactStringHeaders with a caller-chosen mask.
//
// Issue #1146 (review round 3): MaskValue's `••••<last2> (<N> chars)` rendering
// is a deliberate trade for the INTERACTIVE surfaces — it lets an operator
// recognise which token is configured, and the write path (UnmaskHeaders)
// depends on being able to recognise it when a client echoes it back. On the
// AUDIT surfaces that trade is wrong: those rows are persisted, streamed and
// exported, so the length and trailing bytes become a durable fingerprint of
// every credential. Those callers pass AuditMaskValue instead. Splitting the
// mask out — rather than forking the "which name is sensitive" rules — keeps
// one definition of sensitivity behind both renderings.
func RedactStringHeadersWith(headers map[string]string, mask func(string) string) map[string]string {
	if headers == nil {
		return nil
	}
	redacted := make(map[string]string, len(headers))
	for key, value := range headers {
		if isSensitiveHeaderKey(key) {
			redacted[key] = mask(value)
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

// IsSensitiveKeyName reports whether a NAME looks like it holds a secret, using
// the same case-insensitive marker match RedactEnvValues applies to env-var
// keys. Exported for callers that have to decide masking from a name they hold
// separately from the value — e.g. a command-line flag naming the argv token
// that follows it (issue #1146), where there is no map to hand to
// RedactEnvValues.
func IsSensitiveKeyName(name string) bool {
	return isSensitiveEnvKey(name)
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
	return RedactEnvValuesWith(env, MaskValue)
}

// RedactEnvValuesWith is RedactEnvValues with a caller-chosen mask. See
// RedactStringHeadersWith for why the audit surfaces need a different one.
func RedactEnvValuesWith(env map[string]string, mask func(string) string) map[string]string {
	if env == nil {
		return nil
	}
	redacted := make(map[string]string, len(env))
	for key, value := range env {
		redacted[key] = maskedEnvValueWith(key, value, mask)
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
	return maskedEnvValueWith(key, value, MaskValue)
}

func maskedEnvValueWith(key, value string, mask func(string) string) string {
	if isSensitiveEnvKey(key) {
		return mask(value)
	}
	if strings.Contains(value, "://") {
		return RedactURLQueryParamsWith(value, mask)
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
	return RedactURLQueryParamsWith(rawURL, MaskValue)
}

// RedactURLQueryParamsWith is RedactURLQueryParams with a caller-chosen mask.
// See RedactStringHeadersWith for why the audit surfaces need a different one.
func RedactURLQueryParamsWith(rawURL string, mask func(string) string) string {
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
			u.User = url.UserPassword(u.User.Username(), mask(pw))
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
			parts[i] = key + "=" + url.QueryEscape(mask(decVal))
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

// IsConfigReference is the exported form of isConfigReference, for callers
// outside this package that must decide whether a value is a secret-store
// LABEL rather than a secret (issue #1146: the activity redactor runs a
// value-shaped detector over everything the name rules pass through, and a
// reference must not be mangled by it).
func IsConfigReference(v string) bool {
	return isConfigReference(v)
}

// AuditMaskValue is the mask for surfaces that PERSIST and EXPORT what they
// redact — the activity store, its SSE payloads and `mcpproxy activity list`
// (issue #1146, review round 3).
//
// Unlike MaskValue it carries no length and no trailing bytes. On an
// interactive surface those help an operator recognise which token is
// configured and are re-read within seconds; written into an audit row they
// become a durable fingerprint of the credential — a correlation handle across
// records and a materially smaller search space for a low-entropy secret. The
// audit surfaces have no need for the affordance, so they do not pay for it.
//
// ${keyring:…} / ${env:…} references still pass through unchanged: they are
// labels pointing at the secret store, and masking them would erase exactly the
// information the audit row exists to carry.
func AuditMaskValue(v string) string {
	if v == "" {
		return "(empty)"
	}
	if isConfigReference(v) {
		return v
	}
	return bulletMarker
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
		return bulletMarker
	}
	return bulletMarker + v[len(v)-2:] + " (" + strconv.Itoa(len(v)) + " chars)"
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
//
// Authority safeguard (Codex round 2, finding 1): a stored secret is restored
// ONLY when incoming keeps the stored scheme and host:port — and, for the
// userinfo password, the same username. If the authority changed (e.g. the URL
// was redirected to an attacker host), nothing is restored and the mask is left
// literal; the write then fails downstream validation rather than silently
// leaking the real credential to a new host.
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

	// Never move a stored secret onto a different scheme/host.
	if inU.Scheme != stU.Scheme || inU.Host != stU.Host {
		return incoming
	}

	changed := false

	// Userinfo password: restore only if the incoming password is the mask of
	// the stored one AND the username is unchanged (same principal).
	if inU.User != nil && stU.User != nil {
		if inPW, hasPW := inU.User.Password(); hasPW {
			if stPW, stHas := stU.User.Password(); stHas &&
				inU.User.Username() == stU.User.Username() &&
				inPW == MaskValue(stPW) {
				inU.User = url.UserPassword(inU.User.Username(), stPW)
				changed = true
			}
		}
	}

	// Query params: hand-edit RawQuery (never url.Values.Encode, which reorders
	// and re-escapes) so untouched params keep their exact original bytes,
	// mirroring RedactURLQueryParams. Duplicate keys are masked at every
	// occurrence by the read path, so restore POSITIONALLY — the i-th masked
	// occurrence maps to the i-th stored occurrence — and only when the incoming
	// and stored occurrence counts for that key match (Codex round 2, finding 3).
	// A count mismatch is ambiguous, so those masks are left literal.
	if inU.RawQuery != "" {
		storedByKey := map[string][]string{} // decoded key -> ordered stored raw values
		for _, part := range strings.Split(stU.RawQuery, "&") {
			eq := strings.IndexByte(part, '=')
			if eq < 0 {
				continue
			}
			dk := queryUnescapeOr(part[:eq])
			storedByKey[dk] = append(storedByKey[dk], part[eq+1:])
		}

		parts := strings.Split(inU.RawQuery, "&")
		incomingCountByKey := map[string]int{}
		for _, part := range parts {
			eq := strings.IndexByte(part, '=')
			if eq < 0 {
				continue
			}
			incomingCountByKey[queryUnescapeOr(part[:eq])]++
		}

		occ := map[string]int{} // per-key occurrence index as we walk incoming
		queryChanged := false
		for i, part := range parts {
			eq := strings.IndexByte(part, '=')
			if eq < 0 {
				continue
			}
			key := part[:eq]
			decKey := queryUnescapeOr(key)
			if !isSensitiveQueryParam(decKey) {
				continue
			}
			n := occ[decKey]
			occ[decKey]++
			storedList, ok := storedByKey[decKey]
			if !ok || incomingCountByKey[decKey] != len(storedList) || n >= len(storedList) {
				continue
			}
			storedRaw := storedList[n]
			if queryUnescapeOr(part[eq+1:]) == MaskValue(queryUnescapeOr(storedRaw)) {
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

// queryUnescapeOr percent-decodes a query token, returning the original bytes
// when decoding fails.
func queryUnescapeOr(s string) string {
	if d, err := url.QueryUnescape(s); err == nil {
		return d
	}
	return s
}

// UnmaskEnvValues is the env-map analogue of UnmaskURL. It guards against a
// client echoing a masked `env` map back on the write path (Issue #872, Codex
// round). Returns incoming unchanged for nil.
//
// The whole-value revert is tried FIRST: if incoming exactly equals the masked
// rendering of the stored value it is reverted to the stored value outright.
// This covers whole-value masks (sensitive keys, RedactSensitiveData fallback)
// and — importantly (Codex round 3) — a MALFORMED URL-shaped value that
// RedactURLQueryParams masked via its RedactURL regex fallback: UnmaskURL cannot
// re-parse such a value, so without this ordering the mask would be persisted
// over the stored secret on an unedited echo.
//
// Only when incoming differs (a genuine edit) does a well-formed URL-shaped
// value under a non-sensitive key fall to component-wise unmasking via UnmaskURL
// — which restores just the masked userinfo password / sensitive query params
// and carries the authority safeguard, letting an operator edit a non-secret
// part of a connection string (db name, an option) without the password mask
// being persisted as the real password (Codex round 2, finding 2).
func UnmaskEnvValues(incoming, stored map[string]string) map[string]string {
	if incoming == nil {
		return incoming
	}
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		sv, ok := stored[k]
		switch {
		case !ok:
			out[k] = v
		case v == maskedEnvValue(k, sv):
			out[k] = sv
		case !isSensitiveEnvKey(k) && strings.Contains(sv, "://") && strings.Contains(v, "://"):
			out[k] = UnmaskURL(v, sv)
		default:
			out[k] = v
		}
	}
	return out
}

// UnmaskHeaders is the header-map analogue of UnmaskEnvValues. Headers are
// always whole-value masked by RedactStringHeaders (no component-wise URL
// masking), so a plain whole-value comparison is sufficient.
func UnmaskHeaders(incoming, stored map[string]string) map[string]string {
	return unmaskMapValues(incoming, stored, maskedHeaderValue)
}

// maskedHeaderValue is the single source of truth for how RedactStringHeaders
// renders one (key, value) pair; reused by UnmaskHeaders to recognise echoed
// masks.
func maskedHeaderValue(key, value string) string {
	return maskedHeaderValueWith(key, value, MaskValue)
}

// maskedHeaderValueWith is maskedHeaderValue with a caller-chosen mask. See
// RedactStringHeadersWith for why the audit surfaces need a different one.
func maskedHeaderValueWith(key, value string, mask func(string) string) string {
	if isSensitiveHeaderKey(key) {
		return mask(value)
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
//
// Issue #1148, round 4: this is not only a standalone helper — it is the
// fallback RedactURLQueryParams takes when url.Parse FAILS, and a URL that
// fails to parse is precisely the URL a connection error is being logged
// about. It masked the query params but had no `user:pass@` rule, so the
// basic-auth password survived every logSafeURL() site in
// internal/upstream/core, internal/upstream/managed and internal/transport
// whenever the configured URL was malformed. Reuse RedactSensitiveData's
// urlUserinfoPattern so the two scrubbers cannot drift apart.
func RedactURL(urlStr string) string {
	if urlStr == "" {
		return urlStr
	}

	result := redactURLUserinfo(urlStr)
	for _, param := range sensitiveParams {
		pattern := regexp.MustCompile(`(?i)(` + param + `=)[^&]+`)
		result = pattern.ReplaceAllString(result, "${1}"+redactedMarker)
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
