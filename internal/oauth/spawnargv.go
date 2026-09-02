package oauth

import (
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
)

// Issue #1158. The SPAWN log sinks — "launcher command prepared", the
// `[launcher] starting:` banner in the per-server log file, "Initialized stdio
// transport", the docker-isolation and cidfile lines — write the child's
// command line to disk. Two rules are needed there and neither is sufficient
// alone:
//
//   - shellwrap.RedactDockerArgs is STRUCTURAL: it masks the value half of
//     every `-e KEY=VALUE` env injection regardless of whether the key looks
//     sensitive, which is the only rule that can see `-e BENIGN=<opaque token>`.
//     It knows nothing about `--api-key sk-live-…`.
//   - Redaction.Argv is SEMANTIC: the flag NAME rule plus the value-shaped
//     detector. It catches `--api-key VALUE`, `--api-key=VALUE` and a
//     vendor-formatted credential sitting anywhere. It does not blanket-mask
//     `-e BENIGN=…`.
//
// Every existing spawn log site ran only the structural rule and was therefore
// half-fixed. SpawnArgv / SpawnCommandString compose both.
//
// Redaction.Argv itself must NOT be widened to do this. It backs the READ doors
// (`GET /api/v1/servers`, MCP `upstream_servers list`, the `/events`
// servers.changed payload) AND CheckArgvMaskEcho on the write doors: blanket
// masking every `-e FOO=bar` there would newly mask benign docker env values on
// the read payload and then make the write door REFUSE the echo. Hence a
// separate, log-only rule.
//
// There is deliberately NO "already masked, skip" guard between the two stages.
// The reason is that such a guard would LEAK, not that it would be redundant:
// shellwrap routes any whitespace-bearing element (the `-c "<whole command
// line>"` produced by wrapWithUserShell on every macOS stdio server, and by the
// docker login-shell fallback) through the command-string rule, so a single
// benign `-e UV_CACHE_DIR=/tmp/c` would put a mask marker in the element and a
// whole-token guard would then skip stage 2 for the rest of that command line,
// publishing a second credential in the clear. That is the exact failure
// `finish` / `maskDetectedPreservingURL` already document one level down.
//
// An earlier version of this comment also claimed the composition is a no-op —
// that the audit rendering, a run of U+2022 bullets, carries no character in
// secretPattern's value class `[a-zA-Z0-9\-_\.]`, so stage 2 provably leaves a
// stage-1 rendering byte-identical. That is FALSE, and review round 2 was right
// that the pinning test used the one fixture that hides it. secretPattern is
// not the only rule in RedactSensitiveData: the sensitiveParams sweep that
// follows it matches `(?i)(<param>=)[^&\s]+`, and `[^&\s]` matches a bullet.
// So `API_KEY=••••` survives stage 2 (`key` is not a sensitiveParam) while
// `SLACK_TOKEN=••••` is rewritten to `SLACK_TOKEN=***REDACTED***` (`token` is).
//
// The composition is therefore not byte-idempotent — it is DISCLOSURE-safe,
// which is the property that matters: what stage 2 rewrites is already a mask,
// both renderings are in MaskMarkers so the write-path net recognises either,
// and no byte of the secret is involved. Both spellings are pinned by
// TestSpawnArgv_CompositionIsDisclosureSafeOnBothEnvSpellings.

// SpawnArgv masks a child process's argument vector for a LOG sink.
//
// The flag names, subcommands, image names, paths and ports survive — an
// operator debugging a spawn still needs to see WHICH flag and WHICH image —
// and only the values are replaced.
//
// Elements that carry embedded whitespace are treated as command STRINGS (the
// `-c` argument of a login-shell wrap) and get the same two rules applied
// token-by-token inside the string, because that is the shape most stdio
// upstreams actually spawn as.
//
// Returns nil for nil.
func (r Redaction) SpawnArgv(argv []string) []string {
	if argv == nil {
		return nil
	}
	// Stage 1: the structural docker-env rule, using THIS policy's mask so the
	// two stages never compose two different renderings (composing
	// secret.MaskSecretValue with RedactSensitiveData yields
	// `API_KEY=***REDACTED*******ue`, which publishes the secret's last two
	// bytes past the marker).
	staged := shellwrap.RedactDockerArgsWith(argv, r.masker())
	// Stage 2: the semantic flag-name + value-shape rule.
	return r.argvWith(staged, true, r.spawnCommandTokens)
}

// SpawnCommandString masks a whole child command LINE for a LOG sink — the
// single `-c "docker run …"` / `-c "npx some-mcp --api-key …"` element a
// login-shell wrap produces, which no []string-only redactor can reach.
func (r Redaction) SpawnCommandString(s string) string {
	if s == "" {
		return s
	}
	return r.spawnCommandTokens(shellwrap.RedactDockerCommandStringWith(s, r.masker()))
}

// spawnCommandTokens applies the argv rules to a command STRING that stage 1
// has already run over, preserving the original spacing so the line stays
// readable.
func (r Redaction) spawnCommandTokens(s string) string {
	segs := splitCommandSegments(s)
	tokens := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg.isToken {
			tokens = append(tokens, seg.text)
		}
	}
	if len(tokens) == 0 {
		return s
	}
	masked := r.argvWith(tokens, true, nil)

	var b strings.Builder
	b.Grow(len(s))
	next := 0
	for _, seg := range segs {
		if !seg.isToken {
			b.WriteString(seg.text)
			continue
		}
		b.WriteString(masked[next])
		next++
	}
	return b.String()
}

type commandSegment struct {
	text    string
	isToken bool
}

func isCommandSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// splitCommandSegments splits a command string into alternating whitespace and
// TOKEN runs so the masked tokens can be spliced back with the original spacing
// intact.
//
// The split is SHELL-AWARE, and it has to be (issue #1158, review round 2
// finding B4). The command string this rule exists for is produced by
// shellwrap.WrapWithUserShell, which runs every element through Shellescape —
// so an argument carrying whitespace arrives single-quoted:
//
//	npx some-mcp --api-key 'sk live secret'
//
// A whitespace-only split makes that FOUR tokens, the flag-name rule masks only
// the first fragment (`'sk`), and `live secret'` is published in the clear on
// the exact form the fix claims to close. Tracking quote state keeps the whole
// quoted run as one token, so the mask covers all of it.
//
// Shellescape also emits the `'"'"'` idiom for an embedded single quote; the
// state machine below handles it for free (close-single, open-double, literal
// quote, close-double, open-single).
//
// UNBALANCED quotes fall back to the whitespace-only split. An apostrophe in a
// path (`/Users/o'brien/x`) would otherwise swallow the rest of the line into
// one token, and a swallowed `--api-key SECRET` is exactly the leak this
// function exists to prevent — degrading to the old behaviour is strictly
// safer than degrading to one giant token.
func splitCommandSegments(s string) []commandSegment {
	segs, ok := splitCommandSegmentsQuoted(s)
	if !ok {
		return splitCommandSegmentsOnSpace(s)
	}
	return segs
}

// splitCommandSegmentsQuoted is the quote-aware split. It reports false when a
// quote is never closed, in which case the caller falls back.
func splitCommandSegmentsQuoted(s string) (segs []commandSegment, balanced bool) {
	segs = make([]commandSegment, 0, 8)
	i := 0
	for i < len(s) {
		j := i
		for j < len(s) && isCommandSpace(s[j]) {
			j++
		}
		if j > i {
			segs = append(segs, commandSegment{text: s[i:j]})
			i = j
		}
		if i >= len(s) {
			break
		}
		j = i
		var quote byte // 0 when outside quotes, else the opening quote char
		for j < len(s) {
			c := s[j]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				j++
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				j++
				continue
			}
			if isCommandSpace(c) {
				break
			}
			j++
		}
		if quote != 0 {
			// Ran off the end inside a quote.
			return nil, false
		}
		if j > i {
			segs = append(segs, commandSegment{text: s[i:j], isToken: true})
		}
		i = j
	}
	return segs, true
}

// splitCommandSegmentsOnSpace is the original whitespace-only split, kept as
// the fallback for command strings whose quoting does not balance.
func splitCommandSegmentsOnSpace(s string) []commandSegment {
	segs := make([]commandSegment, 0, 8)
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && isCommandSpace(s[j]) {
			j++
		}
		if j > i {
			segs = append(segs, commandSegment{text: s[i:j]})
			i = j
		}
		j = i
		for j < len(s) && !isCommandSpace(s[j]) {
			j++
		}
		if j > i {
			segs = append(segs, commandSegment{text: s[i:j], isToken: true})
			i = j
		}
	}
	return segs
}

// URLValueDeep renders a URL for a LOG sink, masking credentials in its own
// query string AND inside any query-parameter value that is itself a URL.
//
// Issue #1158: the OAuth authorization URL carries the configured upstream URL
// verbatim as the RFC 8707 `resource` parameter — autoDetectResource returns
// serverConfig.URL on every fallback branch — and it is logged at Info level
// and printed to stdout on every login attempt. The nested URL arrives
// percent-encoded (`resource=https%3A%2F%2Fhost%2Fmcp%3Ftoken%3DSECRET`), where
// neither the sensitive-query-parameter name rule (which sees one opaque value
// under the name `resource`) nor secretPattern (which needs a literal `=` after
// `token`) can see the credential. Decoding one level is what closes it.
func (r Redaction) URLValueDeep(rawURL string) string {
	return r.urlValueDeep(rawURL, 2)
}

func (r Redaction) urlValueDeep(rawURL string, depth int) string {
	if rawURL == "" {
		return rawURL
	}
	main, fragment, hasFragment := strings.Cut(rawURL, "#")
	base, rawQuery, hasQuery := strings.Cut(main, "?")
	if hasQuery && depth > 0 {
		parts := strings.Split(rawQuery, "&")
		changed := false
		for i, part := range parts {
			key, value, ok := strings.Cut(part, "=")
			if !ok || value == "" {
				continue
			}
			decoded, err := url.QueryUnescape(value)
			if err != nil || !strings.Contains(decoded, "://") {
				continue
			}
			inner := r.urlValueDeep(decoded, depth-1)
			if inner == decoded {
				continue
			}
			parts[i] = key + "=" + escapeNestedURL(inner)
			changed = true
		}
		if changed {
			rawURL = base + "?" + strings.Join(parts, "&")
			if hasFragment {
				rawURL += "#" + fragment
			}
		}
	}
	return r.URLValue(rawURL)
}

// escapeNestedURL re-escapes only the delimiters that would break the enclosing
// query string's framing. The mask marker is left literal on purpose: this
// rendering goes to a log line a human reads, not back onto the wire, and a
// percent-encoded run of bullets is unreadable.
var nestedURLEscaper = strings.NewReplacer("&", "%26", "#", "%23", " ", "%20")

func escapeNestedURL(s string) string { return nestedURLEscaper.Replace(s) }

// ExtraParamValue renders one OAuth extra-parameter VALUE for a LOG field.
//
// Issue #1158: the authorize-URL builders log every extra parameter as
// `zap.String("value", value)` while the sibling summary lines mask the same
// map through maskExtraParams - the map was masked and the individual values
// were not. The `resource` parameter is the sharpest case: it carries the
// configured upstream URL verbatim, credentials included.
func (r Redaction) ExtraParamValue(key, value string) string {
	if isResourceParam(key) {
		return r.URLValueDeep(value)
	}
	return r.Leaf(key, value)
}

// benignFlagQualifiers are flag-name SEGMENTS that make a marker word
// non-secret (issue #1158, review round 2 finding B5).
//
// IsSensitiveKeyName matches its markers as SUBSTRINGS — that is the right
// rule for an env-var name, where over-masking a value costs nothing anyone
// reads. Applying it to a spawn argv on the LOG path is different: the flag's
// VALUE is often the diagnostic the operator opened the log for, and the
// substring rule blanks it for
//
//	--max-tokens 4096          (TOKENS contains TOKEN)
//	--public-key-path /etc/x   (KEY)
//	--num-tokens, --token-limit, --output-tokens, --public-key
//
// none of which is a credential. That is not a safe trade dressed up as a
// cautious one: an operator who cannot read the log stops using it, and the
// property that makes this redaction acceptable at all is that flag names,
// hosts and ordinary values survive.
//
// The rule is therefore: de-dash the flag, split it on `-`, `_` and `.`, and if
// any segment is one of these qualifiers, the flag does not name a secret.
//
// A plain segment-EQUALITY rule (segment == marker) was rejected: it silently
// un-masks `--apikey`, `--authtoken` and `--clientsecret`, the glued spellings
// that are at least as common as the hyphenated ones. Keeping the substring
// match and subtracting a short, explicit list of qualifiers loses no coverage.
var benignFlagQualifiers = map[string]bool{
	"max":    true,
	"min":    true,
	"num":    true,
	"count":  true,
	"total":  true,
	"limit":  true,
	"budget": true,
	"input":  true,
	"output": true,
	"public": true,
	"window": true,
	"size":   true,
}

// isSensitiveSpawnFlag reports whether the token FOLLOWING this flag should be
// masked on a LOG sink. See benignFlagQualifiers.
//
// It is deliberately not used by Redaction.Argv: that function backs the READ
// doors and CheckArgvMaskEcho on the WRITE doors, where relaxing what counts as
// sensitive would newly publish a value on `GET /api/v1/servers`. The
// relaxation is log-only, in the same way the whole spawn rule is.
func isSensitiveSpawnFlag(flag string) bool {
	key := ArgvFlagKey(flag)
	if key == "" {
		return false
	}
	if !IsSensitiveKeyName(key) {
		return false
	}
	for _, seg := range strings.FieldsFunc(key, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	}) {
		if benignFlagQualifiers[strings.ToLower(seg)] {
			return false
		}
	}
	return true
}

// spawnLeaf is Leaf with the log-only flag relaxation applied to the NAME rule.
// Used for the inline `--max-tokens=4096` spelling, so it stays in step with
// the space-separated one — the invariant Redaction.Argv's own doc comment
// insists on.
func (r Redaction) spawnLeaf(flagKey, value string) string {
	if flagKey != "" && IsSensitiveKeyName(flagKey) && !isSensitiveSpawnFlag(flagKey) {
		// The name rule would have fired on a marker inside a QUALIFIED flag
		// name. Judge the value on its own shape instead — the detector still
		// runs, so `--max-tokens ghp_…` is still masked.
		return r.finish(value, RedactSensitiveData(value))
	}
	return r.Leaf(flagKey, value)
}
