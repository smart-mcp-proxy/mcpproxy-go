package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
)

// assertNoSecretFragment is the assertion the naive "output must not contain the secret"
// check is not strong enough to make.
//
// Composing two different mask renderings produces
// `API_KEY=***REDACTED*******ue`, which publishes the secret's last two bytes
// PAST the marker while still passing a whole-string containment check. So the
// oracle is: no run of >= minRun bytes taken from the secret may appear
// anywhere in the rendered output.
func assertNoSecretFragment(t *testing.T, rendered, secret string, minRun int) {
	t.Helper()
	for i := 0; i+minRun <= len(secret); i++ {
		frag := secret[i : i+minRun]
		assert.NotContains(t, rendered, frag,
			"a %d-byte fragment of the secret survived into the rendered log field", minRun)
	}
}

// TestSpawnArgv_MasksEverySecretClass is the leak half. Each vector is a shape
// that exactly ONE of the two composed rules can see, so a fix that keeps only
// the structural docker rule (which is what every spawn log site had before
// #1158) or only the semantic flag rule fails here.
func TestSpawnArgv_MasksEverySecretClass(t *testing.T) {
	const secret = "SUPERSECRETBETAARGVVALUE"

	cases := []struct {
		name string
		argv []string
		// keep is a substring that MUST survive — the diagnostic half.
		keep string
	}{
		{
			// Only the STRUCTURAL docker rule sees this: the key name is
			// benign, so no name matcher fires, and the value has no
			// vendor shape for the detector.
			name: "docker env under a benign key",
			argv: []string{"run", "--rm", "-e", "BENIGN_NAME=" + secret, "img"},
			keep: "BENIGN_NAME=",
		},
		{
			// Only the SEMANTIC flag rule sees this.
			name: "space-separated sensitive flag",
			argv: []string{"serve", "--api-key", secret},
			keep: "--api-key",
		},
		{
			name: "inline sensitive flag",
			argv: []string{"--api-key=" + secret},
			keep: "--api-key=",
		},
		{
			// The shape the live repro actually leaked: wrapWithUserShell
			// folds the whole command line into ONE argv element, which no
			// []string-only rule can see into.
			name: "login-shell command string with a spaced sensitive flag",
			argv: []string{"-l", "-c", "npx some-mcp --api-key " + secret},
			keep: "npx some-mcp --api-key",
		},
		{
			// The refutation's case: a benign `-e` puts a mask marker in the
			// element, and a whole-token "already masked, skip" guard would
			// then publish this second credential in the clear.
			name: "command string with a benign -e AND a second credential",
			argv: []string{"-l", "-c", "docker run --rm -e UV_CACHE_DIR=/tmp/c img uvx some-mcp --api-token=" + secret},
			keep: "uvx some-mcp --api-token=",
		},
		{
			name: "glued docker env form",
			argv: []string{"run", "-eSLACK_TOKEN=" + secret, "img"},
			keep: "SLACK_TOKEN=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := AuditRedaction.SpawnArgv(tc.argv)
			joined := strings.Join(out, " ")
			assertNoSecretFragment(t, joined, secret, 6)
			assert.Contains(t, joined, tc.keep,
				"masking must not erase the flag/key that says WHICH credential moved: %v", out)
			assert.True(t, ContainsMaskMarker(joined),
				"the element that held the secret must carry a recognised mask marker: %v", out)
		})
	}
}

// TestSpawnArgv_NoComposedDoubleMaskLeak replaces the design's vacuous
// TestSpawnArgv_DoesNotDoubleMask.
//
// The real double-mask defect only manifests when stage 1's output still
// contains `[a-zA-Z0-9]` — i.e. when someone wires the spawn rule to the
// UNPARAMETERIZED shellwrap.RedactDockerArgs (secret.MaskSecretValue, `sk-***ue`)
// instead of this policy's own mask. That composition leaks the secret's last
// two bytes past the marker. Assert byte-exact equality with the intended
// rendering, not a `!Contains("***REDACTED***")` spot check, which holds for the
// broken output too.
func TestSpawnArgv_NoComposedDoubleMaskLeak(t *testing.T) {
	const secret = "supersecretvalue"

	// The reachable-by-mistake composition, kept here as the CONTRAST so the
	// test documents what it is guarding against.
	mistaken := AuditRedaction.Argv(shellwrap.RedactDockerArgs([]string{"-e", "API_KEY=" + secret}))
	require.Len(t, mistaken, 2)
	assert.Contains(t, mistaken[1], secret[len(secret)-2:],
		"sanity: the mistaken composition is what leaks a suffix; if this stops "+
			"holding, the contrast below no longer proves anything")

	got := AuditRedaction.SpawnArgv([]string{"-e", "API_KEY=" + secret})
	require.Len(t, got, 2)
	assert.Equal(t, "-e", got[0])
	assert.Equal(t, "API_KEY="+AuditMaskValue(secret), got[1],
		"one mask rendering, applied once: the key survives and no byte of the value does")
	assertNoSecretFragment(t, strings.Join(got, " "), secret, 2)
}

// TestSpawnArgv_PreservesDiagnostics is the anti-regression half: masking that
// eats the tokens an operator debugs a spawn with has traded one bug for
// another.
func TestSpawnArgv_PreservesDiagnostics(t *testing.T) {
	argv := []string{
		"run", "--rm", "-i",
		"--cidfile", "/tmp/mcpproxy-cid-123456.txt",
		"--memory", "512m",
		"--workdir", "/app",
		"ghcr.io/astral-sh/uv:python3.12-bookworm-slim",
		"myimage@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	assert.Equal(t, argv, AuditRedaction.SpawnArgv(argv),
		"non-secret spawn diagnostics must round-trip byte-identical")

	assert.Nil(t, AuditRedaction.SpawnArgv(nil))
	assert.Equal(t, "", AuditRedaction.SpawnCommandString(""))

	// The command-STRING form is the one every macOS stdio server spawns as,
	// and it is where over-masking would be most visible: a value-shaped
	// detector that ate an image digest, an nvm path or a package name would
	// make the per-server log useless.
	for _, line := range []string{
		"npx -y @modelcontextprotocol/server-filesystem /Users/u/docs",
		"uvx mcp-server-git --repository /Users/u/repos/x",
		"docker run --rm -i --name mcpproxy-github ghcr.io/x/y:1.2.3",
		"/opt/homebrew/bin/uv run --directory /Users/u/p mcp",
		"node /Users/u/.nvm/versions/node/v22.1.0/bin/some-mcp --port 8080",
	} {
		assert.Equal(t, line, AuditRedaction.SpawnCommandString(line),
			"a secret-free command line must round-trip byte-identical")
	}
}

// TestSpawnCommandString_MasksInsideTheCommandLine covers the string form
// directly (the cidfile rewriter logs original_cmd / modified_cmd).
func TestSpawnCommandString_MasksInsideTheCommandLine(t *testing.T) {
	const secret = "SUPERSECRETBETAARGVVALUE"
	in := "docker run --rm -e SLACK_TOKEN=" + secret + " img uvx some-mcp --api-key " + secret
	out := AuditRedaction.SpawnCommandString(in)

	assertNoSecretFragment(t, out, secret, 6)
	assert.Contains(t, out, "docker run --rm -e SLACK_TOKEN=")
	assert.Contains(t, out, "img uvx some-mcp --api-key")
}

// TestArgv_UnchangedByTheSpawnRule pins the constraint the whole design rests
// on: Redaction.Argv backs the READ doors and CheckArgvMaskEcho on the WRITE
// doors, so the log-only spawn rule must be additive. A reviewer who
// "simplifies" the patch by folding SpawnArgv into Argv fails here.
func TestArgv_UnchangedByTheSpawnRule(t *testing.T) {
	argv := []string{"run", "--rm", "-e", "BENIGN_NAME=opaquevalue", "img"}
	assert.Equal(t, argv, LiveRedaction.Argv(argv),
		"Argv must still leave a benign docker env value alone — masking it here "+
			"would make the write doors refuse the client's echo")
	assert.Equal(t, argv, AuditRedaction.Argv(argv))
}
