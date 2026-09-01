package oauth

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// Issue #1148, round 4 finding 3: the redaction RULES live here rather than in
// internal/server because there are TWO doors onto the same server config —
// the MCP `upstream_servers list` payload and `GET /api/v1/servers` — and
// internal/httpapi sits BELOW internal/server in the import graph, so it cannot
// reuse a rule defined up there. Every previous half-done shape on this issue
// (argv masked on MCP but not REST, a field masked on read with no matching
// write-path answer) came from a rule that existed in only one of the two
// places. This file is the one place.
//
// internal/oauth already owns the mask RENDERINGS (MaskValue, AuditMaskValue),
// the name matchers (IsSensitiveKeyName, IsSensitiveHeaderName) and the
// write-path unmaskers (UnmaskURL, UnmaskEnvValues, UnmaskHeaders), so the leaf
// rules that combine them belong here too — and putting them here keeps the
// read rendering and the write revert defined side by side, which is the
// invariant this issue keeps breaking.

// Redaction is the shared rule set for masking one server's secret-bearing
// leaves. The RULES — which key names are sensitive, which values look like
// credentials, how argv pairs up, how a URL is rendered component-wise — are
// shared by EVERY door: the MCP `upstream_servers list` /
// `quarantine_security list_quarantined` payloads, `GET /api/v1/servers`, the
// `/events` SSE `servers.changed` payload and the activity store. Only the
// mask RENDERING differs, and that difference is the one thing this struct
// carries.
type Redaction struct {
	// Mask renders one masked secret. Defaults to MaskValue when nil.
	Mask func(string) string

	// DetectContracted collapses a URL-shaped rendering: the value-shaped
	// detector runs over the WHOLE string rather than component by component.
	//
	// Only the AUDIT policy sets it. Audit rows are never written back and are
	// never edited by an operator, so nothing constrains their rendering and
	// maximal masking wins.
	//
	// A LIVE surface cannot collapse the rendering: #872 deliberately renders a
	// connection string component-wise (`postgres://u:••••(N chars)@host/db`)
	// so an operator can edit the database name without the password mask being
	// persisted as the password, and UnmaskURL recognises exactly that
	// rendering on the write path.
	//
	// It does NOT follow that a live surface skips the detector. Round 6
	// finding 1: the old guard skipped it for the ENTIRE string as soon as the
	// name rule rewrote any one component, so a URL holding two credentials
	// published the second in the clear. The detector now runs per component
	// (see maskDetectedPreservingURL), which closes that without collapsing
	// anything the write path depends on.
	DetectContracted bool

	// Limit caps each string leaf the generic walker produces, or 0 for no cap.
	//
	// It is a property of the DESTINATION, not of the rules: the activity store
	// caps every leaf so one mutation carrying a large env map cannot bloat
	// BBolt, while a LIVE read surface must not cap — a truncated URL or arg is
	// no longer equal to mask(stored), so the write-path unmasker would write
	// the truncation through.
	Limit int
}

// WithLimit returns a copy of the policy that caps each string leaf at n bytes.
func (r Redaction) WithLimit(n int) Redaction {
	r.Limit = n
	return r
}

// LiveRedaction is the policy for the LIVE read surfaces (the MCP
// `upstream_servers list` / `quarantine_security list_quarantined` payloads and
// `GET /api/v1/servers`). It keeps MaskValue's `••••<last2> (<N> chars)`
// rendering, which the write-path unmaskers recognise.
var LiveRedaction = Redaction{Mask: MaskValue, DetectContracted: false}

// AuditRedaction is the policy for anything PERSISTED or EXPORTED: the fixed
// `••••` marker, carrying neither the secret's length nor its trailing bytes.
var AuditRedaction = Redaction{Mask: AuditMaskValue, DetectContracted: true}

func (r Redaction) masker() func(string) string {
	if r.Mask == nil {
		return MaskValue
	}
	return r.Mask
}

// Leaf masks one string leaf, judged by the field NAME that encloses it and
// then by the value's own shape.
//
// The name rule decides first — it is what keeps the payload readable and says
// WHICH credential is configured — and whatever survives it is offered to the
// value-shaped detector. A key name is never the only rule: `--endpoint=ghp_…`,
// a credential under a header name no matcher recognises, or a token pasted
// into a field nobody thought of is invisible to name matching and obvious to
// the detector.
//
// `key` is the enclosing field name; pass "" for a leaf with no key (a
// positional argv token).
func (r Redaction) Leaf(key, s string) string {
	switch {
	case IsHeadersFieldName(key):
		return r.HeaderValue(key, s)
	case strings.EqualFold(key, "url"):
		return r.URLValue(s)
	default:
		// An UNCONTRACTED leaf: a plain config field, a future field, or an
		// argv token's value. The env-var rule is this package's single source
		// of truth for "does this key name look like it holds a secret": it
		// masks sensitive-looking keys, passes ${keyring:…}/${env:…}
		// references through, URL-redacts connection strings, and runs
		// RedactSensitiveData over everything else.
		return r.finish(s, maskedEnvValueWith(key, s, r.masker()))
	}
}

// EnvValue masks one environment-variable leaf, reproducing maskedEnvValue's
// NAME rule exactly so UnmaskEnvValues can recognise the rendering if a client
// echoes it back on the write path, then running the value-shaped detector over
// whatever the name rule left of the original value.
func (r Redaction) EnvValue(name, value string) string {
	return r.finish(value, maskedEnvValueWith(name, value, r.masker()))
}

// HeaderValue masks one HTTP header leaf, judged by the header NAME first and
// then by the value's own shape. Both rules are needed and neither is
// sufficient: the name matcher cannot enumerate every custom credential header,
// and the detector cannot recognise an opaque value with no credential shape.
func (r Redaction) HeaderValue(name, value string) string {
	return r.finish(value, maskedHeaderValueWith(name, value, r.masker()))
}

// URLValue masks one URL leaf: the name rule first (sensitive query parameters
// and the userinfo password, rendered component-wise per #872), then the
// value-shaped detector over every component the name rule left alone.
func (r Redaction) URLValue(s string) string {
	return r.finish(s, RedactURLQueryParamsWith(s, r.masker()))
}

// finish runs the value-shaped detector over the output of a NAME rule.
// `original` is the leaf before the name rule ran, `masked` after it.
//
// The detector is what closes the gap the name rules structurally cannot see: a
// vendor-shaped credential under a benign key (`env: {BENIGN: ghp_…}`), a
// header name no matcher enumerates, a query parameter called `opaque`.
//
// It is skipped in exactly ONE case: when the name rule already replaced the
// WHOLE value with this package's own mask rendering. No byte of the secret
// survives there for the detector to find, and re-masking would produce a
// rendering UnmaskEnvValues / UnmaskHeaders could not recognise on the write
// path — trading a non-disclosure for the read-modify-write corruption of
// #1142/#1146.
//
// Everything else — a partially-rewritten value, a URL rendered component-wise,
// an untouched one — still carries original bytes, so the detector runs. For a
// URL it runs PER COMPONENT so the scheme, host and component-wise userinfo
// mask the write path binds to are preserved (round 6 finding 1).
func (r Redaction) finish(original, masked string) string {
	if r.DetectContracted {
		return MaskDetectedSecrets(masked)
	}
	if masked == r.masker()(original) {
		return masked
	}
	return maskDetectedPreservingURL(masked)
}

// maskDetectedPreservingURL runs the value-shaped detector over s, component by
// component when s is a whole absolute URL.
//
// Round 6 finding 1. The detector must not be handed a URL whole on a LIVE
// surface: it would re-mask the `••••<last2> (<N> chars)` userinfo/query
// renderings that UnmaskURL binds to on the write path. But skipping it for the
// whole string — which is what the old `masked != original` guard did — meant
// one masked component silenced the detector for every other one, publishing a
// second credential in the clear.
//
// So it runs on the components the name rule does not own: the path, the
// fragment, and every query parameter whose name no matcher recognises. Those
// are exactly the components UnmaskLiveURLParams reverts, and the ones it
// cannot are refused by the write doors' residual-mask check.
func maskDetectedPreservingURL(s string) string {
	if u, err := url.Parse(s); err != nil || u.Scheme == "" || u.Host == "" {
		return MaskDetectedSecrets(s)
	}

	main, fragment, hasFragment := strings.Cut(s, "#")
	base, rawQuery, hasQuery := strings.Cut(main, "?")

	out := maskDetectedInURLPath(base)
	if hasQuery {
		out += "?" + maskDetectedInURLQuery(rawQuery)
	}
	if hasFragment {
		out += "#" + MaskDetectedSecrets(fragment)
	}
	return out
}

// maskDetectedInURLPath runs the detector over the PATH of `scheme://authority/path`,
// leaving the scheme, the host and the userinfo — whose password the name rule
// has already rendered component-wise — byte-identical.
func maskDetectedInURLPath(base string) string {
	start := 0
	if i := strings.Index(base, "://"); i >= 0 {
		start = i + len("://")
	}
	slash := strings.IndexByte(base[start:], '/')
	if slash < 0 {
		return base
	}
	cut := start + slash
	return base[:cut] + MaskDetectedSecrets(base[cut:])
}

// maskDetectedInURLQuery runs the detector over each query parameter value the
// NAME rule did not already mask.
//
// The detector sees the RAW (still percent-encoded) value bytes, because that
// is what UnmaskLiveURLParams compares against when it reverts an echoed mask
// per parameter — the two must render the same bytes or the revert silently
// stops working.
func maskDetectedInURLQuery(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			// A bare token carries no parameter name at all, so nothing but the
			// detector can judge it.
			parts[i] = MaskDetectedSecrets(part)
			continue
		}
		key, value := part[:eq], part[eq+1:]
		if isSensitiveQueryParam(queryUnescapeOr(key)) {
			// The name rule already replaced this value with MaskValue's
			// rendering; re-masking it would defeat UnmaskURL.
			continue
		}
		if isConfigReference(queryUnescapeOr(value)) {
			// A ${keyring:…} / ${env:…} label, not a secret.
			continue
		}
		parts[i] = key + "=" + MaskDetectedSecrets(value)
	}
	return strings.Join(parts, "&")
}

// Argv masks a command-line argument vector. Returns nil for nil.
//
// Two rules, because argv secrets announce themselves in two ways:
//
//   - The FLAG names the credential — `--api-key VALUE` and `--api-key=VALUE`.
//     Delegated to Leaf with the de-dashed flag as the key, so the same single
//     source of truth the env map uses decides, and the flag itself stays
//     readable (it is the audit signal: WHICH credential moved).
//   - The VALUE is recognisably a credential on its own — an AWS key, a GitHub
//     token, a PEM block, a high-entropy blob — wherever it sits.
//
// BOTH rules run on BOTH spellings: Leaf ends in the detector for every leaf,
// which keeps `--endpoint=ghp_…` and `--endpoint ghp_…` in step by construction
// rather than by two call sites remembering to agree.
//
// Everything else round-trips verbatim: package names, subcommands, paths and
// ports are what make the payload useful.
func (r Redaction) Argv(argv []string) []string {
	if argv == nil {
		return nil
	}
	out := make([]string, len(argv))
	maskNext := false
	for i, s := range argv {
		if maskNext {
			out[i] = r.masker()(s)
			maskNext = false
			continue
		}
		if flag, value, inline := strings.Cut(s, "="); inline && IsArgvFlag(flag) {
			out[i] = flag + "=" + r.Leaf(ArgvFlagKey(flag), value)
			continue
		}
		// An unpaired token — a positional argument, a flag name, or the value
		// of a flag whose name says nothing. Routing through Leaf with NO
		// enclosing key is what keeps the inline and space-separated spellings
		// in step: it is the same function the inline branch delegates to, so
		// the URL rule, the env-name rule and the detector all apply to both.
		out[i] = r.Leaf("", s)
		maskNext = IsArgvFlag(s) && IsSensitiveKeyName(ArgvFlagKey(s))
	}
	return out
}

// IsArgvFlag reports whether a token is an option rather than a positional
// argument. A bare "-" or "--" is a convention, not a flag.
func IsArgvFlag(token string) bool {
	return strings.HasPrefix(token, "-") && strings.Trim(token, "-") != ""
}

// ArgvFlagKey turns `--api-key` into the key name the env/header redactors
// match on.
func ArgvFlagKey(flag string) string {
	return strings.TrimLeft(flag, "-")
}

// IsHeadersFieldName reports whether a map/leaf should be masked with HTTP
// header semantics. Covers both the `headers` config field and the
// `headers_json` tool parameter (the suffix is trimmed before the walk).
func IsHeadersFieldName(key string) bool {
	return strings.EqualFold(key, "headers")
}

// detector is built from the DEFAULT detection config rather than the running
// one on purpose: masking a read surface or an audit row is a property of THAT
// surface, and an operator who turned sensitive-data detection off for the call
// path must not thereby cause credentials to be published or persisted in the
// clear. (Detector.MaskText ignores the `enabled` flag for the same reason.)
var detector = sync.OnceValue(func() *security.Detector {
	return security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
})

// MaskDetectedSecrets masks any credential the value-shaped detector recognises
// inside s, leaving text it does not recognise untouched.
func MaskDetectedSecrets(s string) string {
	masked, _ := detector().MaskText(s)
	return masked
}

// maskRendering is one function that RENDERS a secret as a mask on a read door,
// paired with the MARKER constant its output is built from and a probe input it
// is guaranteed to mask.
//
// This registry is the single source the fail-closed net reads. MaskMarkers
// below is assembled from it and from nothing else, and
// TestMaskMarkers_CoverEveryRendering runs each entry's probe through its
// renderer and fails when the net cannot detect the result — so a rendering
// cannot be added, or an existing one changed, without the net learning about
// it in the same edit.
//
// Issue #1148, round 7 finding 1. The marker list used to be hand-maintained
// beside the renderers and listed only the bullet run and the detector's
// elision. It did not know about `***REDACTED***`, which is what
// RedactSensitiveData emits — and RedactSensitiveData runs on every
// UNCONTRACTED leaf through Redaction.Leaf, so `isolation.extra_args` holding
// `-e API_KEY=<token>` was rendered `-e API_KEY=***REDACTED***` on the read
// door and that echo was ACCEPTED and PERSISTED over the real credential.
// Hand-maintaining a list beside the thing it is supposed to track is the same
// fail-open shape as every other defect on this issue; the list is now derived.
type maskRendering struct {
	name   string
	marker string
	probe  string
	render func(string) string
}

var maskRenderings = []maskRendering{
	{"oauth.MaskValue", bulletMarker, "supersecretvalue", MaskValue},
	{"oauth.MaskValue/short", bulletMarker, "abcd", MaskValue},
	{"oauth.AuditMaskValue", bulletMarker, "supersecretvalue", AuditMaskValue},
	{"oauth.RedactSensitiveData/secret", redactedMarker, "API_KEY=supersecretvalue", RedactSensitiveData},
	{"oauth.RedactSensitiveData/bearer", redactedMarker, "Authorization: Bearer supersecretvalue", RedactSensitiveData},
	{"oauth.RedactSensitiveData/userinfo", redactedMarker, "postgres://u:supersecretvalue@host/db", RedactSensitiveData},
	{"oauth.RedactURL", redactedMarker, "https://host/mcp?access_token=supersecretvalue", RedactURL},
	{"security.MaskValue/prefix", securityMaskSuffixMarker(), "AKIAIOSFODNN7EXAMPLE", MaskDetectedSecrets},
}

// maskOAuthSecret / maskExtraParams are deliberately NOT in the registry. They
// render `abc***wxyz` / `***` for zap DEBUG LOGS only (config.go,
// transport_wrapper.go) — no client ever reads them off a door and echoes them
// back at a write, so widening the net to `***` would refuse legitimate values
// to protect a path that does not exist. The registry is the set of renderings
// that reach a READ DOOR; if one of them ever does, it belongs here and the
// test below will be what proves the net follows.

// securityMaskSuffixMarker is security.MaskValue's own long-value marker, read
// from that package rather than copied, so a change there breaks the registry
// test instead of silently narrowing the net.
func securityMaskSuffixMarker() string {
	return security.MaskMarkers()[0]
}

// MaskMarkers are the substrings every masked rendering carries. They are
// DERIVED from maskRenderings above plus security.MaskMarkers(), so the net can
// never fall behind a rendering.
//
// They back up the byte-for-byte echo checks so a mask is still recognised when
// the stored value has since changed and the exact comparison no longer
// matches. TestArgvMaskMarkers_MatchTheRenderings and
// TestMaskMarkers_CoverEveryRendering pin them to the functions that produce
// them.
var MaskMarkers = deriveMaskMarkers()

func deriveMaskMarkers() []string {
	seen := map[string]bool{}
	var out []string
	add := func(marker string) {
		if marker == "" || seen[marker] {
			return
		}
		seen[marker] = true
		out = append(out, marker)
	}
	for _, r := range maskRenderings {
		add(r.marker)
	}
	// security.MaskValue has a SECOND rendering for values too short to keep a
	// prefix; taking the package's whole marker set rather than only the one
	// the registry probe exercises is what keeps that from being a hole.
	for _, marker := range security.MaskMarkers() {
		add(marker)
	}
	return out
}

// ContainsMaskMarker reports whether a string carries a mask this proxy
// rendered.
func ContainsMaskMarker(s string) bool {
	for _, marker := range MaskMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// CheckArgvMaskEcho reports an error when an incoming argv vector still carries
// a mask this proxy rendered — either the exact masking of a stored token, or
// any token bearing a mask marker. `field` names the request parameter to quote
// back to the caller (`args_json` on the MCP surface, `args` on REST).
//
// Issue #1148, round 3: argv masks are NEVER reverted, they are REFUSED. An
// argv token has NO KEY — env values bind to the variable name, headers to the
// header name, a URL secret to its scheme+host, but an argv slot has only its
// index and its neighbours, and the caller supplies the WHOLE vector *and*
// `command` in the same request. So every candidate binding is
// caller-controlled and any revert can be steered into relocating the live
// credential into an attacker-chosen command line.
//
// Refusing rather than silently keeping the stored vector is deliberate: the
// argv field REPLACES the vector on both surfaces, so silently ignoring it
// would make the write look applied when it was not.
//
// Returns nil for a vector that carries no mask, which is every legitimate
// write: a caller that changed nothing sends the real values it already has, a
// caller that rotated a credential sends the new one.
func CheckArgvMaskEcho(field string, incoming, stored []string) error {
	if len(incoming) == 0 {
		return nil
	}

	echoed := make(map[string]struct{}, len(stored))
	for i, masked := range LiveRedaction.Argv(stored) {
		if masked != stored[i] {
			echoed[masked] = struct{}{}
		}
	}

	for i, token := range incoming {
		if _, ok := echoed[token]; !ok && !ContainsMaskMarker(token) {
			continue
		}
		return fmt.Errorf("%s[%d] is a redaction placeholder, not an argument value: "+
			"credential-shaped argv tokens are masked on read and are never restored on write, "+
			"because an argv slot carries no key to bind the secret to. "+
			"Resend the real value for that argument, or omit %s to leave the stored arguments unchanged",
			field, i, field)
	}
	return nil
}

// UnmaskLiveURLParams reverts, PER QUERY PARAMETER, a value that is exactly the
// value-shaped detector's masking of the stored value for the same parameter
// name.
//
// Issue #1148, round 4 finding 2. UnmaskURL only knows the SENSITIVE query
// params — the ones it masked itself — so a credential the live surface masked
// through the DETECTOR, under a parameter name no matcher recognises
// (`?opaque=ghp_…`), had no revert at all. The whole-URL echo check covered the
// unedited case, but editing any other byte of the URL (the path, a neighbouring
// param) defeated it and the mask was written through as the credential.
//
// The revert is bound to the PARAMETER NAME and to the authority, and to
// nothing else — not to the path, not to the other parameters — which is what
// makes it survive an unrelated edit. As in UnmaskURL, a stored secret is
// restored ONLY when incoming keeps the stored scheme and host:port, so a URL
// redirected at another host never receives it; duplicate keys are restored
// positionally and only when the occurrence counts match.
//
// Anything it cannot bind is left masked on purpose: the caller is then refused
// by the surface's residual-marker check rather than having a mask persisted
// over a credential.
func UnmaskLiveURLParams(incoming, stored string) string {
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
	if inU.Scheme != stU.Scheme || inU.Host != stU.Host || inU.RawQuery == "" {
		return incoming
	}

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
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			incomingCountByKey[queryUnescapeOr(part[:eq])]++
		}
	}

	occ := map[string]int{}
	changed := false
	for i, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		key := part[:eq]
		decKey := queryUnescapeOr(key)
		n := occ[decKey]
		occ[decKey]++
		storedList, ok := storedByKey[decKey]
		if !ok || incomingCountByKey[decKey] != len(storedList) || n >= len(storedList) {
			continue
		}
		storedRaw := storedList[n]
		// The detector runs over the URL's RAW bytes on the read path, so the
		// comparison is against the raw stored bytes too.
		if part[eq+1:] == MaskDetectedSecrets(storedRaw) {
			parts[i] = key + "=" + storedRaw
			changed = true
		}
	}
	if !changed {
		return incoming
	}
	inU.RawQuery = strings.Join(parts, "&")
	return inU.String()
}

// ---------------------------------------------------------------------------
// The generic walk.
//
// Issue #1148, round 7 findings 2 and 3. Both are the same hole: the leaf RULES
// were shared by every door, but the WALK that applies them to a nested payload
// lived in internal/server, so the two doors that could not import it —
// RedactServerSecretFields (REST + SSE) and the hand-built isolation blocks in
// `upstream_servers list` — each reached into the config struct field by field
// and each missed `isolation.extra_args`.
//
// So the walk moves here, beside the rules, and every door calls it. A field
// added under a server payload is then masked because the walk reaches it, not
// because somebody remembered to add a line.
// ---------------------------------------------------------------------------

// Value walks a generic JSON value (as produced by NormalizeForRedaction),
// masking every string leaf according to the key that encloses it. `key` is the
// enclosing field name — pass "" at the root, and for a slice the name of the
// slice itself.
func (r Redaction) Value(key string, v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		isHeaders := IsHeadersFieldName(key)
		isEnv := IsEnvFieldName(key)
		for k, val := range typed {
			if s, ok := val.(string); ok {
				switch {
				case isHeaders:
					// HTTP header semantics: mask by header name.
					out[k] = r.CapString(r.HeaderValue(k, s))
					continue
				case isEnv:
					// Environment-variable semantics: mask by var name, using
					// oauth's own rendering so the write path can recognise an
					// echoed mask.
					out[k] = r.CapString(r.EnvValue(k, s))
					continue
				}
			}
			out[k] = r.Value(k, val)
		}
		return out
	case []interface{}:
		if IsArgvFieldName(key) {
			return r.argvValue(typed)
		}
		out := make([]interface{}, len(typed))
		for i, val := range typed {
			out[i] = r.Value(key, val)
		}
		return out
	case string:
		return r.CapString(r.Leaf(key, typed))
	default:
		// Bools, json.Number and nil carry no secrets and stay verbatim.
		return v
	}
}

// argvValue masks a command-line argument vector reached through the generic
// walk, using the same Argv rules the typed doors use.
//
// Non-string elements (only reachable from a malformed `args_json` payload)
// carry no flag semantics: they are walked with no enclosing key and stand in
// as an empty token, which resets the flag pairing exactly as the typed walk
// does.
func (r Redaction) argvValue(argv []interface{}) []interface{} {
	tokens := make([]string, len(argv))
	for i, item := range argv {
		if s, ok := item.(string); ok {
			tokens[i] = s
		}
	}
	masked := r.Argv(tokens)

	out := make([]interface{}, len(argv))
	for i, item := range argv {
		if _, ok := item.(string); !ok {
			out[i] = r.Value("", item)
			continue
		}
		out[i] = r.CapString(masked[i])
	}
	return out
}

// CapString applies the policy's per-leaf length cap, if any, cutting on a rune
// boundary and marking the cut.
func (r Redaction) CapString(s string) string {
	if r.Limit <= 0 || len(s) <= r.Limit {
		return s
	}
	return s[:safeTruncateBytes(s, r.Limit)] + capEllipsis
}

// capEllipsis marks a leaf the cap shortened.
const capEllipsis = "…"

// safeTruncateBytes returns the largest cut <= limit that does not split a
// UTF-8 rune.
func safeTruncateBytes(s string, limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit >= len(s) {
		return len(s)
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

// IsEnvFieldName reports whether a map should be masked with
// environment-variable semantics. Covers both the `env` config field and the
// `env_json` tool parameter (the suffix is trimmed before the walk).
func IsEnvFieldName(key string) bool {
	return strings.EqualFold(key, "env")
}

// IsArgvFieldName reports whether a slice is a command-line argument vector.
// Covers the `args` config field and the `args_json` tool parameter.
//
// `isolation.extra_args` is deliberately NOT argv here: `Redaction.Argv` pairs
// `--flag value` and keeps unrecognised tokens verbatim, which is right for a
// vector whose command it belongs to is also on the wire. An extra_args vector
// is docker-run flags, and its elements go through Leaf like any other free
// text — which is what masks `-e API_KEY=<token>`.
func IsArgvFieldName(key string) bool {
	return strings.EqualFold(key, "args")
}

// RedactNested masks every secret-bearing leaf of an arbitrary nested value in
// place, returning a value of the SAME Go type.
//
// It is how the typed doors (RedactServerSecretFields) redact a nested block
// without enumerating its fields: the value is normalised to generic JSON,
// walked by the shared rules, and decoded back over `dst`. A field added to the
// nested struct is therefore masked by default.
//
// On any encode/decode failure `dst` is left UNTOUCHED and false is returned,
// so the caller can fail closed rather than publish a half-redacted block.
//
// The masked value is decoded into a FRESH zero value and only then assigned
// over `dst`. Decoding straight into `dst` would be the #1142 corruption in a
// new place: encoding/json REUSES an existing slice's backing array, so a
// projection whose []string still aliases the stored config (a caller that
// assigned the slice instead of copying it) would have its live credentials
// overwritten with their own masks by the act of rendering a read response.
func (r Redaction) RedactNested(key string, dst interface{}) bool {
	if dst == nil {
		return false
	}
	ptr := reflect.ValueOf(dst)
	if ptr.Kind() != reflect.Ptr || ptr.IsNil() {
		return false
	}
	walked := r.Value(key, NormalizeForRedaction(dst))
	encoded, err := json.Marshal(walked)
	if err != nil {
		return false
	}
	fresh := reflect.New(ptr.Type().Elem())
	if err := json.Unmarshal(encoded, fresh.Interface()); err != nil {
		return false
	}
	ptr.Elem().Set(fresh.Elem())
	return true
}
