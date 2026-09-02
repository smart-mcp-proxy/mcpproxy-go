package oauth

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
)

// posixShellescape mirrors the POSIX branch of shellwrap.Shellescape.
//
// The fixtures below must be POSIX-quoted on EVERY host, not just on POSIX
// ones. shellwrap.Shellescape switches on runtime.GOOS and emits cmd.exe
// double-quoting on Windows, so building the fixture with it directly made
// these tests assert Windows quoting against a tokenizer whose whole job is the
// POSIX `-c "<command line>"` form that WrapWithUserShell produces. That is not
// a Windows-only code path from the reader's side: a Windows core reads
// per-server logs and configs that a POSIX host wrote, and the masker must
// still cover them.
//
// TestPosixShellescape_MatchesProduction below pins this against the real
// implementation on POSIX, so the copy cannot drift from the quoting the
// product actually emits — which is what building the fixture from
// shellwrap.Shellescape was protecting in the first place.
func posixShellescape(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`;&|<>(){}[]?*~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func TestPosixShellescape_MatchesProduction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shellwrap.Shellescape emits cmd.exe quoting here; the POSIX equivalence cannot be checked")
	}
	for _, in := range []string{
		"", "plain", "SUPERSECRET PASSPHRASE WITH SPACES", "pass'word with spaces",
		`double"quote`, "semi;colon", "tilde~expand",
	} {
		assert.Equal(t, shellwrap.Shellescape(in), posixShellescape(in),
			"the test's POSIX escaper has drifted from shellwrap.Shellescape for %q", in)
	}
}

// Issue #1158, review round 2, finding B4.
//
// The spawn rule's command-STRING tokenizer split on whitespace only, while the
// command string it is fed is produced by shellwrap.WrapWithUserShell — which
// runs every element through Shellescape, so any argument carrying whitespace
// arrives SINGLE-QUOTED. A credential with a space in it therefore became four
// tokens, the flag-name rule masked the first fragment, and the rest was
// published in the clear — on the `-c "<whole command line>"` form the fix's
// own commit message calls "the form that actually leaked".
func TestSpawnArgv_MasksAWhitespaceBearingSecretInTheDashCForm(t *testing.T) {
	const secret = "SUPERSECRET PASSPHRASE WITH SPACES"

	// Built the way the product builds it on a POSIX host, not by hand: the
	// escaper decides the quoting, and TestPosixShellescape_MatchesProduction
	// pins it against shellwrap.Shellescape, so the test cannot drift from the
	// real shape.
	commandLine := strings.Join([]string{
		posixShellescape("npx"),
		posixShellescape("some-mcp"),
		posixShellescape("--api-key"),
		posixShellescape(secret),
	}, " ")
	require.Contains(t, commandLine, "'", "sanity: the spaced value must be quoted")

	for name, got := range map[string]string{
		"SpawnCommandString": AuditRedaction.SpawnCommandString(commandLine),
		"SpawnArgv/-c":       strings.Join(AuditRedaction.SpawnArgv([]string{"-l", "-c", commandLine}), " "),
	} {
		t.Run(name, func(t *testing.T) {
			for _, word := range strings.Fields(secret) {
				assert.NotContains(t, got, word,
					"a whitespace-separated fragment of the credential survived: %q", got)
			}
			assert.Contains(t, got, "npx some-mcp --api-key",
				"the command and the flag NAME are the diagnostic and must survive: %q", got)
		})
	}
}

// The embedded-single-quote idiom Shellescape emits ('"'"') must not confuse
// the tokenizer into ending the quoted run early.
func TestSpawnCommandString_HandlesTheEmbeddedQuoteIdiom(t *testing.T) {
	const secret = "pass'word with spaces"
	commandLine := "npx some-mcp --api-key " + posixShellescape(secret)
	require.Contains(t, commandLine, `'"'"'`, "sanity: this is the idiom under test")

	got := AuditRedaction.SpawnCommandString(commandLine)
	assert.NotContains(t, got, "word with spaces")
	assert.NotContains(t, got, "pass")
	assert.Contains(t, got, "--api-key")
}

// An UNBALANCED quote (an apostrophe in a path) must degrade to the old
// whitespace-only split rather than swallowing the rest of the line into one
// token — a swallowed `--api-key SECRET` would be a new leak.
func TestSpawnCommandString_UnbalancedQuoteStillMasks(t *testing.T) {
	const secret = "sk-live-0f2a91cc77d4"
	got := AuditRedaction.SpawnCommandString("node /Users/o'brien/app.js --api-key " + secret)

	assert.NotContains(t, got, secret)
	assert.Contains(t, got, "--api-key")
	assert.Contains(t, got, "/Users/o'brien/app.js")
}

// Issue #1158, review round 2, finding B5.
//
// IsSensitiveKeyName matches its markers as SUBSTRINGS. Applying it to a spawn
// argv on the LOG path blanked `--max-tokens 4096` (TOKENS contains TOKEN) and
// `--public-key-path /etc/x` (KEY) in main.log and in the per-server log. The
// property that makes this whole redaction acceptable to an operator is that
// flag names, hosts and ordinary values survive; eating the value of every flag
// whose name happens to contain a marker word trades one bug for another.
func TestSpawnArgv_KeepsQualifiedFlagValues(t *testing.T) {
	keep := [][]string{
		{"serve", "--max-tokens", "4096"},
		{"serve", "--max-tokens=4096"},
		{"serve", "--public-key-path", "/etc/ssl/pub.pem"},
		{"serve", "--public-key-path=/etc/ssl/pub.pem"},
		{"serve", "--num-tokens", "128"},
		{"serve", "--output-tokens", "512"},
		{"serve", "--token-limit", "8000"},
	}
	for _, argv := range keep {
		assert.Equal(t, argv, AuditRedaction.SpawnArgv(argv),
			"a qualified flag names a budget or a public path, not a credential")
	}
}

// …and the relaxation must not cost coverage. Both the hyphenated and the
// GLUED spellings still lose their values — a plain segment-equality rule would
// have silently un-masked the glued ones.
func TestSpawnArgv_StillMasksRealCredentialFlags(t *testing.T) {
	const secret = "sk-live-99aa11bb22cc33dd"
	for _, flag := range []string{
		"--api-key", "--apikey", "--auth-token", "--authtoken",
		"--client-secret", "--clientsecret", "--password", "--bearer-token",
	} {
		spaced := AuditRedaction.SpawnArgv([]string{"serve", flag, secret})
		assert.NotContains(t, strings.Join(spaced, " "), secret, "spaced form of %s leaked", flag)
		assert.Contains(t, strings.Join(spaced, " "), flag, "the flag name is the audit signal")

		inline := AuditRedaction.SpawnArgv([]string{"serve", flag + "=" + secret})
		assert.NotContains(t, strings.Join(inline, " "), secret, "inline form of %s leaked", flag)
	}
}

// The value-shaped detector still runs under a qualified flag, so a real
// credential handed to `--max-tokens` is masked on its own shape.
func TestSpawnArgv_QualifiedFlagStillMasksAShapedCredential(t *testing.T) {
	const ghToken = "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	for _, argv := range [][]string{
		{"serve", "--max-tokens", ghToken},
		{"serve", "--max-tokens=" + ghToken},
	} {
		got := strings.Join(AuditRedaction.SpawnArgv(argv), " ")
		assert.NotContains(t, got, ghToken,
			"relaxing the NAME rule must not turn off the VALUE rule: %v", argv)
	}
}

// The READ/WRITE contract is unchanged: the relaxation is log-only. Redaction.
// Argv backs GET /api/v1/servers and CheckArgvMaskEcho on the write doors, and
// relaxing it there would newly publish a value on a read door.
func TestArgv_KeepsTheStrictNameRule(t *testing.T) {
	argv := []string{"serve", "--max-tokens", "4096"}
	got := LiveRedaction.Argv(argv)
	require.Len(t, got, 3)
	assert.NotEqual(t, "4096", got[2],
		"Argv must keep the strict substring rule — the spawn relaxation is log-only")
}

// The design comment used to claim that stage 2 "provably leaves a stage-1
// rendering byte-identical". It does not, and the original pinning test used
// the one env-key spelling that hides it: `key` is not in sensitiveParams, so
// `API_KEY=••••` survives stage 2 untouched, while `SLACK_TOKEN=••••` is
// rewritten by RedactSensitiveData's `(?i)(token=)[^&\s]+` sweep — whose value
// class DOES match the bullet marker — into `SLACK_TOKEN=***REDACTED***`.
//
// That is a rendering change, not a disclosure: what stage 2 re-masks is
// already a mask. This test asserts BOTH spellings so the composition's real
// behaviour is pinned rather than the flattering half of it.
func TestSpawnArgv_CompositionIsDisclosureSafeOnBothEnvSpellings(t *testing.T) {
	const secret = "supersecretvalue"

	apiKey := AuditRedaction.SpawnArgv([]string{"-e", "API_KEY=" + secret})
	require.Len(t, apiKey, 2)
	assert.Equal(t, "API_KEY="+AuditMaskValue(secret), apiKey[1],
		"a key spelling outside sensitiveParams is untouched by stage 2")

	slack := AuditRedaction.SpawnArgv([]string{"-e", "SLACK_TOKEN=" + secret})
	require.Len(t, slack, 2)
	assert.Equal(t, "SLACK_TOKEN="+redactedMarker, slack[1],
		"a key spelling INSIDE sensitiveParams is re-marked by stage 2 — a mask over a mask")

	for _, got := range [][]string{apiKey, slack} {
		joined := strings.Join(got, " ")
		assertNoSecretFragment(t, joined, secret, 2)
		assert.True(t, ContainsMaskMarker(joined),
			"whichever rendering wins, the write-path net must recognise it: %v", got)
	}
}
