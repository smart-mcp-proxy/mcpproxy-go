package oauth

import (
	"fmt"
	"net/url"
	"strings"
	"sync"

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

// Redaction is the pair of decisions that separate the two surfaces the rules
// serve. The RULES — which key names are sensitive, which values look like
// credentials, how argv pairs up — are shared; only the rendering and whether
// the value-shaped detector runs over oauth's own contracted leaves differ, so
// the two surfaces cannot drift on the part that decides what is a secret.
type Redaction struct {
	// Mask renders one masked secret. Defaults to MaskValue when nil.
	Mask func(string) string

	// DetectContracted runs the value-shaped secret detector over the THREE
	// leaf kinds this package owns a write-path unmask contract for — env
	// values, header values and the server URL — EVEN WHEN the name/URL rules
	// already rewrote them.
	//
	// Only the AUDIT policy sets it. Audit rows are never written back, so
	// nothing constrains their rendering and maximal masking wins.
	//
	// A LIVE surface cannot mask unconditionally: UnmaskEnvValues /
	// UnmaskHeaders / UnmaskURL recognise an echoed mask by comparing against
	// this package's own rendering byte for byte, and #872 deliberately renders
	// a connection string component-wise (`postgres://u:••••(N chars)@host/db`)
	// so an operator can edit the database name without the password mask being
	// persisted as the password.
	//
	// It does NOT follow that a live surface skips the detector entirely: a
	// vendor-shaped credential under a benign name (`env: {BENIGN: ghp_…}`) is
	// invisible to the name matcher. detectContracted below carries the narrow
	// rule that closes that without collapsing the component-wise rendering.
	DetectContracted bool
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
// WHICH credential is configured — and whatever it passes through is offered to
// the value-shaped detector. A key name is never the only rule: `--endpoint=ghp_…`,
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
		return r.detectContracted(s, RedactURLQueryParamsWith(s, r.masker()))
	default:
		// An UNCONTRACTED leaf: a plain config field, a future field, or an
		// argv token's value. Nothing unmasks these, so the detector always
		// runs. The env-var rule is this package's single source of truth for
		// "does this key name look like it holds a secret": it masks
		// sensitive-looking keys, passes ${keyring:…}/${env:…} references
		// through, URL-redacts connection strings, and runs RedactSensitiveData
		// over everything else as defence in depth.
		named := RedactEnvValuesWith(map[string]string{key: s}, r.masker())[key]
		return MaskDetectedSecrets(named)
	}
}

// EnvValue masks one environment-variable leaf, reproducing maskedEnvValue
// exactly so UnmaskEnvValues can recognise the rendering if a client echoes it
// back on the write path.
func (r Redaction) EnvValue(name, value string) string {
	return r.detectContracted(value, RedactEnvValuesWith(map[string]string{name: value}, r.masker())[name])
}

// HeaderValue masks one HTTP header leaf, judged by the header NAME first and
// then by the value's own shape. Both rules are needed and neither is
// sufficient: the name matcher cannot enumerate every custom credential header,
// and the detector cannot recognise an opaque value with no credential shape.
func (r Redaction) HeaderValue(name, value string) string {
	return r.detectContracted(value, RedactStringHeadersWith(map[string]string{name: value}, r.masker())[name])
}

// detectContracted runs the value-shaped detector over a leaf this package owns
// a write-path unmask contract for. `original` is the value before the name/URL
// rule ran, `masked` the value after.
//
// AUDIT: always. Nothing writes back through an audit row.
//
// LIVE: only when the name/URL rule left the value BYTE-IDENTICAL — exactly the
// gap where a credential the name matcher cannot see (`ghp_…` under
// `BENIGN_NAME`) would otherwise be published. The guard is what keeps it from
// collapsing the #872 component-wise connection-string rendering, whose
// readable scheme/host/db the operator's edit flow depends on.
//
// When the detector DOES fire on a live surface the rendering is no longer this
// package's, so UnmaskEnvValues / UnmaskHeaders / UnmaskURL can no longer
// recognise an echoed mask. The live surfaces reproduce THIS rendering on the
// write path (see UnmaskLiveURLParams here, and unmaskLive* in
// internal/server/mcp_server_view.go), so the round trip still cannot persist a
// mask over a secret.
func (r Redaction) detectContracted(original, masked string) string {
	if !r.DetectContracted && masked != original {
		return masked
	}
	return MaskDetectedSecrets(masked)
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

// MaskMarkers are the substrings every masked rendering carries: MaskValue /
// AuditMaskValue open with the bullet run, and the value-shaped detector
// (security.MaskValue) ends with the elision+asterisks.
//
// They back up the byte-for-byte echo checks so a mask is still recognised when
// the stored value has since changed and the exact comparison no longer
// matches. TestArgvMaskMarkers_MatchTheRenderings pins them to the functions
// that produce them.
var MaskMarkers = []string{"••••", "…****"}

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
