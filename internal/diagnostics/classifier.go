package diagnostics

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/client/transport"
)

// Classify maps a raw error to a stable Code. It prefers typed-error inspection
// via errors.Is / errors.As over string matching; falls back to string matching
// only when the underlying library does not expose structured error types.
//
// The hints parameter lets callers nudge the classifier with context ("this
// error came from the stdio spawn path", etc.).
//
// If no specific classification applies, Classify returns UnknownUnclassified.
func Classify(err error, hints ClassifierHints) Code {
	if err == nil {
		return ""
	}

	// Fast path: a producer opted into explicit code attribution via
	// WrapError / CodedError. This is how OAUTH/DOCKER/CONFIG/QUARANTINE
	// producers bypass free-text matching for their terminal errors.
	var coded interface{ Code() Code }
	if errors.As(err, &coded) {
		if c := coded.Code(); c != "" {
			return c
		}
	}

	if c := classifyStdio(err, hints); c != "" {
		return c
	}
	if c := classifyHTTP(err, hints); c != "" {
		return c
	}
	if c := classifyNetwork(err, hints); c != "" {
		return c
	}
	if c := classifyOAuth(err, hints); c != "" {
		return c
	}
	if c := classifyDocker(err, hints); c != "" {
		return c
	}
	if c := classifyConfig(err, hints); c != "" {
		return c
	}
	if c := classifyQuarantine(err, hints); c != "" {
		return c
	}

	return UnknownUnclassified
}

// classifyOAuth recognises OAuth 2.1 / PKCE failure surface-strings emitted
// by the upstream manager and mcp-go. Producers that want deterministic
// classification should wrap their terminal error with WrapError(code, err).
func classifyOAuth(err error, _ ClassifierHints) Code {
	msg := strings.ToLower(err.Error())
	switch {
	// Re-auth (a previously-working stored token broke) is matched BEFORE the
	// login-required backstop because "re-login available" contains the
	// "login available" substring; the order of these two cases is load-bearing.
	case strings.Contains(msg, "re-login available"),
		strings.Contains(msg, "re-authentication required"),
		strings.Contains(msg, "server error with stored token"):
		return OAuthReauthRequired
	// First-time sign-in deferred to the user (ErrOAuthPending text). These are
	// actionable user-states, not faults — keep them out of UNKNOWN.
	case strings.Contains(msg, "oauth authentication required"),
		strings.Contains(msg, "login available"),
		strings.Contains(msg, "mcpproxy auth login"):
		return OAuthLoginRequired
	case strings.Contains(msg, "refresh_token") && strings.Contains(msg, "expired"),
		strings.Contains(msg, "refresh token has expired"),
		strings.Contains(msg, "refresh token is expired"):
		return OAuthRefreshExpired
	case strings.Contains(msg, "refresh") && strings.Contains(msg, "403"),
		strings.Contains(msg, "refresh") && strings.Contains(msg, "invalid_grant"):
		return OAuthRefresh403
	case strings.Contains(msg, "oauth metadata unavailable"),
		strings.Contains(msg, "oauth discovery failed"),
		strings.Contains(msg, "discover") && strings.Contains(msg, "oauth"),
		strings.Contains(msg, ".well-known/oauth"):
		return OAuthDiscoveryFailed
	case strings.Contains(msg, "oauth callback") && strings.Contains(msg, "timeout"),
		strings.Contains(msg, "authorization timeout"):
		return OAuthCallbackTimeout
	case strings.Contains(msg, "redirect_uri") && strings.Contains(msg, "mismatch"),
		strings.Contains(msg, "redirect uri") && strings.Contains(msg, "mismatch"):
		return OAuthCallbackMismatch
	}
	return ""
}

// classifyDocker recognises common Docker isolation failures the runtime
// currently reports as plain errors. Typed opt-in via WrapError is still
// preferred; these string matches are the last-resort fallback.
func classifyDocker(err error, _ ClassifierHints) Code {
	msg := strings.ToLower(err.Error())
	// The same guard classifyDockerIsolatedSpawn applies, on the sibling path a
	// hint-less caller reaches: a handshake timeout means a container is alive,
	// so every arm below is reading the container's own stderr tail rather than
	// a docker-layer failure. classifyStdio has already answered such an error
	// with MCPX_STDIO_HANDSHAKE_TIMEOUT, so nothing is left unclassified.
	if isStdioHandshakeTimeout(msg) {
		return ""
	}
	if c := dockerLayerCode(msg); c != "" {
		return c
	}
	switch {
	// docker CLI unresolved (#696). These shapes are unambiguous about the
	// docker BINARY being missing, so they classify even without the
	// DockerIsolated hint (e.g. shellwrap's resolution-failure error).
	case strings.Contains(msg, "docker not found in path"),
		strings.Contains(msg, "docker not found in login shell"),
		strings.Contains(msg, "docker: command not found"),
		strings.Contains(msg, "command not found: docker"), // zsh: "zsh:1: command not found: docker"
		strings.Contains(msg, "docker: not found"),
		strings.Contains(msg, `"docker": executable file not found`):
		return DockerCLINotFound
	// OCI runtime failures from `docker run`. NOTE: a BARE "exec format error"
	// is intentionally NOT matched here — a non-docker, wrong-architecture host
	// stdio binary emits the same string and must stay STDIO-classified. The
	// docker-isolated path routes bare "exec format error" via the hinted
	// classifyDockerIsolatedSpawn; here we require real OCI/runc context.
	case strings.Contains(msg, "oci runtime"),
		strings.Contains(msg, "runc"):
		return DockerOCIRuntime
	}
	return ""
}

// dockerLayerCode maps a failure of the DOCKER LAYER ITSELF — the daemon
// refusing us, not running, or unable to fetch the image — to a DOCKER code.
// It is shared by classifyDocker and the docker-isolated stdio path so the two
// cannot drift, and so a real daemon failure is not claimed first by a generic
// stdio arm (a socket refusal used to land on MCPX_STDIO_SPAWN_EACCES, i.e.
// chmod advice for a server binary that is fine).
//
// Every arm requires evidence the daemon or its socket actually spoke. See
// dockerDaemonEvidence for why the bare substring "docker" is not evidence.
func dockerLayerCode(msg string) Code {
	switch {
	case strings.Contains(msg, "cannot connect to the docker daemon"),
		strings.Contains(msg, "is the docker daemon running"),
		strings.Contains(msg, "docker daemon is not reachable"),
		strings.Contains(msg, "docker.sock: connect: no such file"):
		return DockerDaemonDown
	case strings.Contains(msg, "snap") && strings.Contains(msg, "apparmor"),
		strings.Contains(msg, "no-new-privileges") && strings.Contains(msg, "apparmor"):
		return DockerSnapAppArmor
	case (strings.Contains(msg, "permission denied") || strings.Contains(msg, "access is denied")) &&
		dockerDaemonEvidence(msg):
		return DockerNoPermission
	case strings.Contains(msg, "pull access denied"),
		strings.Contains(msg, "manifest unknown"),
		dockerDaemonEvidence(msg) && strings.Contains(msg, "image") &&
			strings.Contains(msg, "pull") && strings.Contains(msg, "fail"):
		return DockerImagePullFailed
	}
	return ""
}

// dockerDaemonEvidence reports whether the message carries evidence that the
// docker DAEMON or its SOCKET is what spoke — as opposed to the bare substring
// "docker", which is worthless here: mcpproxy's own stdio wrapper injects it
// into EVERY isolated error it formats
//
//	stdio transport (command="/usr/local/bin/docker", docker_isolation=true): …
//
// so `permission denied && docker` was near-unfalsifiable under isolation. Any
// "permission denied" anywhere in the captured container stderr — a git deploy
// key rejected, a non-executable entrypoint — was diagnosed as a Docker socket
// permission problem and sent the user to fix socket permissions that were
// never broken (#1144).
//
// Everything listed here is text only the docker CLI, the daemon or a socket
// dial can produce; none of it appears in mcpproxy's wrapper.
func dockerDaemonEvidence(msg string) bool {
	return strings.Contains(msg, "docker daemon") ||
		strings.Contains(msg, "docker.sock") ||
		strings.Contains(msg, "docker%2esock") || // URL-escaped in Post "http://%2Fvar%2Frun%2Fdocker.sock/…"
		strings.Contains(msg, "pipe/docker_engine") ||
		strings.Contains(msg, `pipe\docker_engine`) ||
		strings.Contains(msg, "pipe%2fdocker_engine") ||
		strings.Contains(msg, "dockerd") ||
		strings.Contains(msg, "error response from daemon")
}

// classifyConfig recognises configuration parsing / secret resolution failures.
func classifyConfig(err error, _ ClassifierHints) Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deprecated") && strings.Contains(msg, "field"),
		strings.Contains(msg, "deprecated configuration"):
		return ConfigDeprecatedField
	case strings.Contains(msg, "unmarshal") && strings.Contains(msg, "config"),
		strings.Contains(msg, "config parse"),
		strings.Contains(msg, "invalid config"),
		strings.Contains(msg, "config: ") && (strings.Contains(msg, "json") || strings.Contains(msg, "yaml") || strings.Contains(msg, "toml")):
		return ConfigParseError
	case strings.Contains(msg, "missing secret"),
		strings.Contains(msg, "secret reference") && (strings.Contains(msg, "not found") || strings.Contains(msg, "unresolved")),
		strings.Contains(msg, "unresolved secret"):
		return ConfigMissingSecret
	// A package-runner command with nothing to run. This is mcpproxy's OWN
	// pre-spawn validation message (internal/upstream/core/connection_stdio.go),
	// so leaving it unclassified put a "Please file a bug report" CTA on a
	// config typo the user can fix in one line.
	case strings.Contains(msg, "has no args"),
		strings.Contains(msg, "no args") && strings.Contains(msg, "required"):
		return ConfigInvalidCommand
	}
	return ""
}

// classifyQuarantine recognises security-quarantine rejections.
func classifyQuarantine(err error, _ ClassifierHints) Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "quarantine") && (strings.Contains(msg, "pending") || strings.Contains(msg, "requires approval") || strings.Contains(msg, "not approved")):
		return QuarantinePendingApproval
	case strings.Contains(msg, "tool") && strings.Contains(msg, "changed") && (strings.Contains(msg, "re-approval") || strings.Contains(msg, "reapprove") || strings.Contains(msg, "rug pull")):
		return QuarantineToolChanged
	}
	return ""
}

// classifyDockerIsolatedSpawn maps a spawn/exec failure on a Docker-isolated
// server to a specific DOCKER code. Returns "" when the error is not a
// recognised docker-isolation failure (caller falls through to generic stdio
// handling).
//
// Case order is load-bearing:
//  1. The docker BINARY itself is missing (#696) — must win even though its
//     message also contains "command not found" / "executable file not found".
//  2. The in-container interpreter is missing — real docker output nests this
//     inside an "OCI runtime create failed: … exec: \"x\": executable file not
//     found" string, so it must be checked BEFORE the generic OCI case below.
//  3. Any other OCI runtime failure (exec format error / runc).
func classifyDockerIsolatedSpawn(err error) Code {
	// Host couldn't even start the docker binary (direct exec path).
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, syscall.ENOENT) &&
		strings.Contains(strings.ToLower(execErr.Name), "docker") {
		return DockerCLINotFound
	}

	msg := strings.ToLower(err.Error())

	// A handshake TIMEOUT means the container is still running — it simply
	// never answered `initialize`. Whatever a startup script wrote to stderr
	// minutes earlier (dash has no `source`, npm prints notices, …) did not
	// kill anything, so it must not be promoted to the terminal cause and
	// outrank MCPX_STDIO_HANDSHAKE_TIMEOUT. A missing toolchain makes the
	// process EXIT, which is the other wrapper and is unaffected here.
	//
	// The guard covers EVERY arm below that reads the stderr TAIL — which is
	// all of them. The earlier rationale for exempting the docker-CLI and
	// OCI/runc arms ("that wording means no container ever started, so it
	// cannot coexist with a timeout") had the implication backwards: if the
	// handshake timed out, a container IS alive, so the wording did not come
	// from our docker layer at all — it came out of the container, which is
	// free to print "docker: command not found" (docker-in-docker, a script
	// that shells out to docker) or an "OCI runtime" / "exec format error"
	// line of its own. Guarding some arms and not others is decoration: the
	// unguarded arm claims the failure exactly as the guarded ones would have.
	//
	// EXEMPT, and why that is sound:
	//   - the typed *exec.Error check above: it is the host's own failed
	//     execve of `docker`, not text scraped from a child's stderr;
	//   - "docker not found in PATH" / "docker not found in login shell"
	//     (arm 1a): mcpproxy's OWN shellwrap resolution errors, emitted before
	//     any container exists and never present in a container's stderr.
	// Both describe a launch that never happened, so no handshake could have
	// timed out around them.
	toolchainSuppressed := isStdioHandshakeTimeout(msg)

	// A shell reporting a BUILTIN missing (`sh: 1: source: not found`) is a
	// portability bug in the server's own startup script — dash has no
	// `source`, `shopt` or `pushd` — and says nothing about what the image
	// contains. Treating it as evidence sends the user rebuilding a Docker
	// image over a `#!/bin/sh` line, and hijacks the code that would have
	// pointed at the stderr where the real cause is.
	builtinOnly := onlyShellBuiltinsMissing(msg)

	// The docker LAYER failing — daemon refusing, absent, or unable to fetch
	// the image — is decided before any stderr-tail reading.
	//
	// It is subject to toolchainSuppressed for the same reason every other
	// stderr-derived arm is: when both appear, the daemon wording came from the
	// container's OWN stderr tail — an application log line — and reporting
	// MCPX_DOCKER_DAEMON_DOWN sends the user restarting Docker over a message
	// their server printed. A genuine daemon refusal is unaffected: it surfaces
	// on the exit path, where isStdioHandshakeTimeout is false.
	var layerCode Code
	if !toolchainSuppressed {
		layerCode = dockerLayerCode(msg)
	}

	switch {
	// (1a) mcpproxy's OWN docker-resolution failure. Exempt from the guard: we
	// write these ourselves when `docker` cannot be resolved, before any
	// container exists, so they can never be stderr-tail noise.
	case strings.Contains(msg, "docker not found in path"),
		strings.Contains(msg, "docker not found in login shell"):
		return DockerCLINotFound
	// (1b) the shell / Go exec layer reporting `docker` itself missing. Cover
	// both shell wordings: bash/sh `docker: command not found` AND zsh's
	// reversed `zsh:1: command not found: docker` (the common macOS login-shell
	// shape) — the latter must beat the generic "command not found" → EXEC case
	// below. Read out of the stderr tail, so guarded.
	case !toolchainSuppressed && (strings.Contains(msg, `"docker": executable file not found`) ||
		strings.Contains(msg, "docker: command not found") ||
		strings.Contains(msg, "command not found: docker") ||
		strings.Contains(msg, "docker: not found")):
		return DockerCLINotFound
	// (1c) the docker daemon/socket refused us, is down, or could not pull the
	// image. Must precede every stderr-tail arm below: `docker: permission
	// denied while trying to connect to the Docker daemon socket …` otherwise
	// fell through to the generic stdio "permission denied" arm and came back
	// as MCPX_STDIO_SPAWN_EACCES.
	case layerCode != "":
		return layerCode
	// (2) a TOOL the server shells out to is missing from the image. git is
	// the case that matters in practice: the Python default image is slim and
	// has no git, so every `--from …git+https://…` server fails here (#1143).
	// Must precede (4): bash reports it as "git: command not found", which
	// would otherwise be read as the ENTRYPOINT interpreter missing and put
	// the wrong image advice in front of the user.
	case !toolchainSuppressed && isContainerGitMissing(msg):
		return DockerMissingToolchain
	// (3) any other shell "missing command" line on the container's stderr.
	// Anchored to a shell prefix (see shellToolNotFoundRe), so it is specific
	// enough to sit above (4) — which would otherwise answer the bash wording
	// of this exact failure with the ENTRYPOINT-missing code. Real OCI
	// entrypoint errors never carry a shell prefix, so they still fall to (4).
	case !toolchainSuppressed && !builtinOnly && shellToolNotFoundRe.MatchString(msg):
		return DockerMissingToolchain
	// (4) in-container interpreter missing (image lacks uvx/node/python/…).
	// Split in two so the builtin carve-out applies only to the wording a shell
	// builtin line can produce: an OCI "executable file not found" is never one
	// of those, and must still classify even next to a benign `source` line.
	//
	// "no such file or directory" needs the exec/OCI context (see
	// execNoSuchFileRe): on its own it is the most common line in ordinary
	// stderr — an optional cache dir, a config file a server probes and
	// survives — and the container-toolchain arms run before every generic
	// stdio arm, so one such warning claimed a failure whose real cause was
	// three lines further down.
	case !toolchainSuppressed && (strings.Contains(msg, "executable file not found") ||
		execNoSuchFileRe.MatchString(msg) ||
		(strings.Contains(msg, "oci runtime") && strings.Contains(msg, "no such file or directory"))):
		return DockerExecNotFound
	case !toolchainSuppressed && !builtinOnly && strings.Contains(msg, "command not found"):
		return DockerExecNotFound
	// (5) other OCI runtime failures (arch mismatch, runc start failure). Read
	// out of the stderr tail like everything above it, so guarded: a live
	// container is free to log the word "runc" or an "exec format error" of its
	// own while it keeps running.
	case !toolchainSuppressed && (strings.Contains(msg, "oci runtime") ||
		strings.Contains(msg, "exec format error") ||
		strings.Contains(msg, "runc")):
		return DockerOCIRuntime
	}
	return ""
}

// execNoSuchFileRe requires "no such file or directory" to be reported ABOUT an
// exec, which is the only shape that means the container's entrypoint is
// missing:
//
//	exec /usr/local/bin/uvx: no such file or directory        (docker ≥ 20.10)
//	exec: "uvx": … no such file or directory                  (OCI create)
//
// A bare match is worthless — `warn: /var/cache/uv: no such file or directory`
// is a line thousands of programs print about a file they do not need.
var execNoSuchFileRe = regexp.MustCompile(`(?:^|[\s"'(])exec(?:ve)?\b[^\n]{0,200}no such file or directory`)

// isStdioHandshakeTimeout reports whether the error is the "the child is alive
// but silent" wrapper (connection_lifecycle.go) rather than a process that
// died. It is the single source for the MCPX_STDIO_HANDSHAKE_TIMEOUT arm of
// classifyStdio below and for the suppression guard above, so the two cannot
// drift apart.
func isStdioHandshakeTimeout(msg string) bool {
	return isMCPProxyHandshakeTimeout(msg) || strings.Contains(msg, "handshake timeout")
}

// isMCPProxyHandshakeTimeout matches only the sentence mcpproxy itself writes
// (connection_lifecycle.go). Unlike the looser "handshake timeout" substring —
// which Go's own `net/http: TLS handshake timeout` also carries — it is
// unambiguous enough to act on with no transport hint at all.
func isMCPProxyHandshakeTimeout(msg string) bool {
	return strings.Contains(msg, "did not respond to mcp initialize")
}

// isStdioExitBeforeInitialize reports whether the error is mcpproxy's OWN
// premature-exit wrapper (enrichTransportClosedError in connection_lifecycle.go)
// — the child STARTED and then died. It is the single source for the
// MCPX_STDIO_EXIT_BEFORE_INITIALIZE arm below and for the spawn-suppression
// guard, so the two cannot drift apart, exactly as isStdioHandshakeTimeout is
// for the timeout pair.
func isStdioExitBeforeInitialize(msg string) bool {
	return strings.Contains(msg, "exited before completing the mcp initialize") ||
		strings.Contains(msg, "exited before the mcp initialize")
}

// shellToolNotFoundRe matches a shell's "missing command" line, and only that.
// It is anchored on the SHELL PREFIX the line always carries —
//
//	sh: 1: git: not found          (dash / ash / BusyBox)
//	/bin/sh: 1: exec: cmake: not found
//	bash: line 1: cmake: command not found
//
// — because the bare `<word>: not found` shape it used to match occurs all over
// ordinary captured stderr (`ERROR config profile production: not found`, an
// MCP error body, a registry lookup) and the container-toolchain arm runs
// before every generic stdio arm. Unanchored, one such line anywhere in the
// stderr tail claimed the whole failure and sent the user to a docs page
// telling them to change their Docker image.
//
// zsh's reversed wording (`zsh:1: command not found: cmake`) is deliberately
// NOT matched here — it has no space after the shell's colon, and the generic
// "command not found" arm already covers it.
var shellToolNotFoundRe = regexp.MustCompile(
	`(?:^|\s)(?:[\w./+-]*/)?(?:sh|bash|dash|ash|ksh|zsh|busybox)(?:\[\d+\])?: (?:(?:line )?\d+: )?(?:exec: )?([\w.+-]+): (?:command )?not found`)

// shellBuiltins are names a shell resolves ITSELF, so a "<name>: not found"
// line for one of them means the script was written for another shell (almost
// always bash-isms run under dash/ash) — not that the image is missing a
// binary. No normal image ships these as executables, so nothing is lost by
// refusing to read them as a missing toolchain.
var shellBuiltins = map[string]bool{
	"source": true, "shopt": true, "pushd": true, "popd": true, "dirs": true,
	"declare": true, "typeset": true, "local": true, "let": true, "alias": true,
	"unalias": true, "bind": true, "complete": true, "compgen": true,
	"mapfile": true, "readarray": true, "history": true, "caller": true,
	"suspend": true, "enable": true, "builtin": true, "logout": true,
	"disown": true, "coproc": true, "select": true, "function": true,
}

// onlyShellBuiltinsMissing reports whether a shell "missing command" line is
// present AND every command it names is a shell builtin. It is deliberately an
// ALL, not an ANY: a stderr tail carrying both `source: not found` and
// `cmake: not found` really is a missing toolchain, and the benign line must
// not cancel the real one.
func onlyShellBuiltinsMissing(msg string) bool {
	matches := shellToolNotFoundRe.FindAllStringSubmatch(msg, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if !shellBuiltins[strings.ToLower(m[1])] {
			return false
		}
	}
	return true
}

// gitBinaryMissingMarkers are wordings that name the missing `git` BINARY
// outright. They are positive evidence, and they classify on their own.
var gitBinaryMissingMarkers = []string{
	"git executable not found",           // uv (older releases)
	"ensure that git is installed",       // uv (older releases)
	"failed to spawn: `git`",             // uv: exec of git itself failed
	"failed to spawn `git`",              //   (spacing varies by uv version)
	"git: not found",                     // dash/ash
	"git: command not found",             // bash
	"command not found: git",             // zsh
	"\"git\": executable file not found", // Go exec
}

// gitSpokeRe matches a diagnostic line git, ssh or curl emitted THEMSELVES.
// Its presence is proof the git binary ran, whatever it then failed at:
//
//	fatal: could not read from remote repository.
//	error: RPC failed; HTTP 429 …
//	remote: Invalid username or password.
//	warning: redirecting to https://…
//
// This replaces an enumerated list of clone-failure causes. That list decided
// the DEFAULT — anything it did not name was reported as "the image has no
// git" — so every wording missing from it (a redirect, a 429, a protocol
// error, "could not read from remote repository") told the user to rebuild
// their Docker image over a failure that proves git ran. A list can never be
// complete; the burden of proof belongs on the claim that a binary is absent.
var gitSpokeRe = regexp.MustCompile(`(?:^|[\s|>─╰•])(?:fatal|error|remote|warning|hint|ssh|curl|usage):`)

// gitSpokeUnprefixed are the diagnostics git's helpers print with no such
// prefix at all. They are the same evidence as gitSpokeRe, just unlabelled.
var gitSpokeUnprefixed = []string{
	"host key verification failed",
	"permission denied (publickey)",
	"no supported authentication methods available",
	"the remote end hung up unexpectedly",
	"could not read from remote repository",
}

// isContainerGitMissing reports whether the container stderr says the git
// BINARY is absent from the image.
//
// `uv` collapses every git failure — a missing binary, a private repo, an
// offline host — into the same "Git operation failed" headline, so that
// headline alone decides nothing. What separates the two is whether git itself
// said anything: when uv cannot execute git there is no git diagnostic to
// report, and when it can, git's own fatal:/error:/remote: line is right there
// in the tail (#1143 remains covered: the reported failure is exactly the
// headline with no git output under it).
func isContainerGitMissing(msg string) bool {
	for _, marker := range gitBinaryMissingMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	if !strings.Contains(msg, "git operation failed") {
		return false
	}
	if gitSpokeRe.MatchString(msg) {
		return false
	}
	for _, spoke := range gitSpokeUnprefixed {
		if strings.Contains(msg, spoke) {
			return false
		}
	}
	return true
}

// classifyStdio handles os/exec spawn errors and handshake failures.
func classifyStdio(err error, hints ClassifierHints) Code {
	// Docker-isolated servers run `docker run …` over the stdio transport, so
	// ENOENT-class failures here are docker-specific (#696 CLI missing, or an
	// image/interpreter mismatch) rather than a plain host-binary miss. Resolve
	// those to DOCKER codes before the generic stdio matching below.
	if hints.DockerIsolated {
		if c := classifyDockerIsolatedSpawn(err); c != "" {
			return c
		}
	}

	// mcpproxy's OWN handshake-timeout wrapper, matched without any hint. Only
	// the stdio lifecycle writes this sentence, so it is unambiguous, and
	// accepting it here is what lets classifyDocker suppress its stderr-derived
	// arms on a timeout without dropping such a failure to
	// MCPX_UNKNOWN_UNCLASSIFIED and its "file a bug report" CTA. The looser
	// "handshake timeout" substring stays hint-gated below, because Go's own
	// `net/http: TLS handshake timeout` shares it.
	if isMCPProxyHandshakeTimeout(strings.ToLower(err.Error())) {
		return STDIOHandshakeTimeout
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		// exec.Error wraps os.PathError which wraps syscall.Errno; ENOENT/EACCES
		// are the two we care about.
		if errors.Is(execErr.Err, syscall.ENOENT) {
			return STDIOSpawnENOENT
		}
		if errors.Is(execErr.Err, syscall.EACCES) {
			return STDIOSpawnEACCES
		}
		if errors.Is(execErr.Err, syscall.ENOEXEC) {
			return STDIOSpawnExecFormat
		}
	}

	// exec.ExitError — process started but exited non-zero during handshake.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return STDIOExitNonzero
	}

	// Context deadline during handshake → handshake timeout. Only when the
	// hints say we're on the stdio transport (otherwise a generic timeout
	// would be misclassified).
	if hints.Transport == "stdio" && errors.Is(err, context.DeadlineExceeded) {
		return STDIOHandshakeTimeout
	}

	// String-match fallback for stdio failures when the raw error was
	// wrapped by an intermediate layer (e.g. "failed to connect: stdio
	// transport ... recent stderr: no such file or directory"). The upstream
	// manager currently string-wraps spawn failures, so we can't rely on
	// exec.Error being present. These matches are intentionally broad and
	// err toward MCPX_STDIO_SPAWN_ENOENT / MCPX_STDIO_HANDSHAKE_TIMEOUT —
	// both are strictly better than MCPX_UNKNOWN_UNCLASSIFIED for the user.
	// DockerIsolated implies stdio: config.ResolveIsolation only resolves to
	// mode=docker for a server that HAS a Command, i.e. a stdio server, and the
	// isolated launch is `docker run …` over the stdio transport. Accepting the
	// hint on its own keeps a caller that filled only DockerIsolated from
	// skipping every arm below — which left mcpproxy's own wrappers ("exited
	// before completing the MCP initialize handshake", "did not respond to MCP
	// initialize") on MCPX_UNKNOWN_UNCLASSIFIED and its "file a bug report"
	// CTA, with the loose docker arms as the only thing that could fire.
	if hints.Transport == "stdio" || hints.DockerIsolated {
		msg := err.Error()
		lmsg := strings.ToLower(msg)

		// mcpproxy's own EXIT wrapper is written only when the transport closed
		// — i.e. the child process STARTED and then died. A spawn-class code
		// (ENOENT / EACCES / ENOEXEC all mean "we never got the process off the
		// ground") is therefore provably wrong whenever it is present, and the
		// words that produce those codes — "no such file or directory",
		// "permission denied" — are ordinary lines any program writes about its
		// own optional files. Reading one out of the attached stderr TAIL let a
		// warning the child logged and survived outrank the authoritative fact
		// that it ran, and handed the user chmod advice for a binary that is
		// fine. Same guard as toolchainSuppressed on the docker path, on the
		// other wrapper.
		spawnSuppressed := isStdioExitBeforeInitialize(lmsg)

		switch {
		// The handshake-timeout wrapper is mcpproxy's OWN phrasing and means
		// the child is alive: it outranks every arm below, all of which read
		// the attached stderr TAIL and would otherwise let a line the child
		// logged and survived decide the diagnosis. Same reasoning as the
		// toolchainSuppressed guard in classifyDockerIsolatedSpawn — and it
		// has to sit here too, or suppressing the docker arm just hands the
		// same stderr line to this one. It leads the switch for that reason:
		// "exec format error" below is stderr-tail evidence like the rest.
		case isStdioHandshakeTimeout(lmsg):
			return STDIOHandshakeTimeout
		// Wrong-arch / non-executable host binary (ENOEXEC). Guarded against
		// docker OCI context ("oci runtime"/"runc") so a real containerized
		// exec-format failure still falls through to classifyDocker → OCI; a
		// BARE "exec format error" is a host stdio problem, not a Docker one.
		case !spawnSuppressed && strings.Contains(lmsg, "exec format error") &&
			!strings.Contains(lmsg, "oci runtime") && !strings.Contains(lmsg, "runc"):
			return STDIOSpawnExecFormat
		// A git/ssh CREDENTIAL failure during a dependency install. `git@host:
		// Permission denied (publickey).` contains "permission denied", which
		// the EACCES arm below reads as the server's own executable being
		// unreadable — chmod advice for a file that is fine, while the real
		// cause (no key for a private repo) sits in the stderr. git RAN and the
		// child then died, so it belongs with the other git-clone failures on
		// EXIT_BEFORE_INITIALIZE, whose remediation points at that stderr.
		case strings.Contains(lmsg, "permission denied (publickey)"):
			return STDIOExitBeforeInitialize
		case !spawnSuppressed && (strings.Contains(lmsg, "no such file or directory") ||
			strings.Contains(lmsg, "executable file not found")):
			return STDIOSpawnENOENT
		case !spawnSuppressed && strings.Contains(lmsg, "command not found") && !onlyShellBuiltinsMissing(lmsg):
			return STDIOSpawnENOENT
		case !spawnSuppressed && strings.Contains(lmsg, "permission denied"):
			return STDIOSpawnEACCES
		// Subprocess started but the transport closed before the MCP initialize
		// handshake completed — the child exited early (e.g. printed a fatal
		// config error to stderr and died). mcp-go surfaces this as a closed
		// transport, which otherwise falls through to MCPX_UNKNOWN_UNCLASSIFIED
		// even though the real cause is on the child's stderr (MCP-1093 / #599).
		// Gated on the stdio hint so a "transport closed" from another transport
		// is not misattributed.
		case strings.Contains(lmsg, "transport closed"),
			isStdioExitBeforeInitialize(lmsg):
			return STDIOExitBeforeInitialize
		case strings.Contains(lmsg, "invalid handshake"),
			strings.Contains(lmsg, "malformed"):
			return STDIOHandshakeInvalid
		}
	}

	return ""
}

// classifyHTTP handles HTTP/SSE transport errors including TLS, DNS, and
// structured HTTP status errors. HTTP status classification prefers a typed
// statusError (DiagnoseHTTPStatus below) but also falls back to a string match
// because the upstream layer commonly stringifies the error before bubbling it
// up.
func classifyHTTP(err error, hints ClassifierHints) Code {
	// DNS lookup errors are reported as *net.DNSError.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return HTTPDNSFailed
	}

	// TLS verification: mcp-go surfaces these as *tls.CertificateVerificationError
	// in recent releases; we avoid a direct import dependency by string match.
	msg := err.Error()
	lmsg := strings.ToLower(msg)
	if strings.Contains(msg, "x509:") || strings.Contains(msg, "tls: ") || strings.Contains(msg, "certificate") {
		return HTTPTLSFailed
	}

	// Connection refused — syscall.ECONNREFUSED wrapped by net.OpError.
	if errors.Is(err, syscall.ECONNREFUSED) {
		return HTTPConnRefuse
	}

	// HTTP request timeouts. The upstream HTTP transport bubbles
	// context.DeadlineExceeded up wrapped in a free-text "transport error: ...
	// context deadline exceeded" string. Try the typed errors.Is path first
	// (cheap, exact); fall back to substring on the http transport hint to
	// catch the stringified form. Without this, hf.co/mcp slowdowns surface
	// to the UI as MCPX_UNKNOWN_UNCLASSIFIED.
	if errors.Is(err, context.DeadlineExceeded) && hints.Transport == "http" {
		return HTTPTimeout
	}
	if hints.Transport == "http" && strings.Contains(lmsg, "context deadline exceeded") {
		return HTTPTimeout
	}

	// A 4xx on the streamable-HTTP `initialize` POST is the legacy-SSE
	// signature, not an auth or routing failure. Matched BEFORE the generic
	// status-text fallback below, which would otherwise claim it as a bare
	// MCPX_HTTP_404/403 and send the user hunting for a credential problem that
	// does not exist.
	//
	// The typed check first: this error is mcp-go's exported sentinel, NOT one
	// of our own strings, so a library bump can reword it at any time. The
	// substring fallback stays because the upstream layer commonly stringifies
	// the error before it reaches us, which breaks the errors.Is chain.
	// TestLegacySSESentinelStillMatches pins the vendored wording so that bump
	// fails a test instead of silently regressing this code to MCPX_HTTP_404.
	if errors.Is(err, transport.ErrLegacySSEServer) ||
		strings.Contains(lmsg, "likely a legacy sse server") ||
		(strings.Contains(lmsg, "4xx for initialize") && strings.Contains(lmsg, "post")) {
		return HTTPLegacySSE
	}

	// HTTP status text fallback. The upstream layer wraps non-2xx responses
	// as a plain string ("transport error: request failed with status 504: ...").
	// The typed statusError path used by DiagnoseHTTPStatus() never fires for
	// those, so we substring-match the canonical phrasing here.
	if hints.Transport == "http" {
		if code := matchHTTPStatusText(lmsg); code != "" {
			return code
		}
	}

	return ""
}

// matchHTTPStatusText extracts a status code from the canonical
// "request failed with status NNN" / "notification failed with status NNN"
// phrasing emitted by the HTTP transport adapter. Returns empty when no
// recognised status appears.
func matchHTTPStatusText(lmsg string) Code {
	const marker = "status "
	idx := strings.Index(lmsg, marker)
	for idx != -1 {
		rest := lmsg[idx+len(marker):]
		// Need at least three digits.
		if len(rest) >= 3 && isDigit(rest[0]) && isDigit(rest[1]) && isDigit(rest[2]) {
			status := int(rest[0]-'0')*100 + int(rest[1]-'0')*10 + int(rest[2]-'0')
			if c := DiagnoseHTTPStatus(status); c != "" {
				return c
			}
		}
		next := strings.Index(lmsg[idx+1:], marker)
		if next == -1 {
			break
		}
		idx += 1 + next
	}
	return ""
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// classifyNetwork handles host-environment network issues.
func classifyNetwork(err error, hints ClassifierHints) Code {
	_ = hints
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// "network is unreachable" / "no route to host"
		if errors.Is(opErr.Err, syscall.ENETUNREACH) || errors.Is(opErr.Err, syscall.EHOSTUNREACH) {
			return NetworkOffline
		}
	}
	return ""
}

// DiagnoseHTTPStatus maps an HTTP status code to a Code. Returns empty if
// the status is not a known failure.
func DiagnoseHTTPStatus(status int) Code {
	switch {
	case status == 401:
		return HTTPUnauth
	case status == 403:
		return HTTPForbidden
	case status == 404:
		return HTTPNotFound
	case status >= 500 && status <= 599:
		return HTTPServerErr
	}
	return ""
}
