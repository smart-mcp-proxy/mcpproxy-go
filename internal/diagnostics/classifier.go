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
	switch {
	case strings.Contains(msg, "cannot connect to the docker daemon"),
		strings.Contains(msg, "is the docker daemon running"),
		strings.Contains(msg, "docker daemon is not reachable"),
		strings.Contains(msg, "docker.sock: connect: no such file"):
		return DockerDaemonDown
	case strings.Contains(msg, "snap") && strings.Contains(msg, "apparmor"),
		strings.Contains(msg, "no-new-privileges") && strings.Contains(msg, "apparmor"):
		return DockerSnapAppArmor
	case strings.Contains(msg, "permission denied") && strings.Contains(msg, "docker"),
		strings.Contains(msg, "got permission denied while trying to connect to the docker"):
		return DockerNoPermission
	case strings.Contains(msg, "pull access denied"),
		strings.Contains(msg, "docker") && strings.Contains(msg, "image") && strings.Contains(msg, "pull") && strings.Contains(msg, "fail"),
		strings.Contains(msg, "manifest unknown"):
		return DockerImagePullFailed
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
	toolchainSuppressed := isStdioHandshakeTimeout(msg)

	switch {
	// (1) docker binary unresolved: shellwrap resolution failure, or the shell
	// / Go exec layer reporting `docker` itself missing. Cover both shell
	// wordings: bash/sh `docker: command not found` AND zsh's reversed
	// `zsh:1: command not found: docker` (the common macOS login-shell shape) —
	// the latter must beat the generic "command not found" → EXEC case below.
	case strings.Contains(msg, `"docker": executable file not found`),
		strings.Contains(msg, "docker: command not found"),
		strings.Contains(msg, "command not found: docker"),
		strings.Contains(msg, "docker: not found"),
		strings.Contains(msg, "docker not found in path"),
		strings.Contains(msg, "docker not found in login shell"):
		return DockerCLINotFound
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
	case !toolchainSuppressed && shellToolNotFoundRe.MatchString(msg):
		return DockerMissingToolchain
	// (4) in-container interpreter missing (image lacks uvx/node/python/…).
	case strings.Contains(msg, "executable file not found"),
		strings.Contains(msg, "no such file or directory"),
		strings.Contains(msg, "command not found"):
		return DockerExecNotFound
	// (5) other OCI runtime failures (arch mismatch, runc start failure).
	case strings.Contains(msg, "oci runtime"),
		strings.Contains(msg, "exec format error"),
		strings.Contains(msg, "runc"):
		return DockerOCIRuntime
	}
	return ""
}

// isStdioHandshakeTimeout reports whether the error is the "the child is alive
// but silent" wrapper (connection_lifecycle.go) rather than a process that
// died. Kept in lockstep with the MCPX_STDIO_HANDSHAKE_TIMEOUT arm of
// classifyStdio below.
func isStdioHandshakeTimeout(msg string) bool {
	return strings.Contains(msg, "did not respond to mcp initialize") ||
		strings.Contains(msg, "handshake timeout")
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
	`(?:^|\s)(?:[\w./+-]*/)?(?:sh|bash|dash|ash|ksh|zsh|busybox)(?:\[\d+\])?: (?:(?:line )?\d+: )?(?:exec: )?[\w.+-]+: (?:command )?not found`)

// gitCloneFailureCauses are causes of a failed git clone that are NOT a missing
// git binary. `uv` collapses them all into "Git operation failed", so without
// this guard a private-repo or offline clone would be diagnosed as a
// missing-toolchain problem and send the user changing images for nothing.
//
// Every entry is text only git/ssh/curl can produce, which is itself the proof
// that git RAN. The list has to cover the wordings a private repository
// actually produces — an incomplete one is not a near-miss, it is the wrong
// diagnosis with a confident docs link attached.
var gitCloneFailureCauses = []string{
	// Credentials.
	"authentication failed",
	"could not read username",
	"could not read password",
	"terminal prompts disabled",
	"invalid username or password",
	"support for password authentication was removed",
	"permission denied (publickey)",
	"no supported authentication methods available",
	"host key verification failed",
	"returned error: 401",
	"returned error: 403",
	// Repository addressing.
	"repository not found",
	"' not found", // git: fatal: repository 'https://…/' not found
	"does not appear to be a git repository",
	// Network / TLS. "unable to access" is git's own prefix for this whole
	// family, so it catches the wordings not spelled out individually.
	"unable to access",
	"could not resolve host",
	"temporary failure in name resolution",
	"connection timed out",
	"connection refused",
	"network is unreachable",
	"failed to connect to",
	"ssl certificate problem",
	"server certificate verification failed",
	"the remote end hung up unexpectedly",
	"early eof",
}

// isContainerGitMissing reports whether the container stderr says git is absent
// from the image. It covers uv's own wording ("Git executable not found…" and
// the terser "Git operation failed" of newer releases) plus the shell wordings
// for a missing `git` binary.
func isContainerGitMissing(msg string) bool {
	switch {
	case strings.Contains(msg, "git executable not found"),
		strings.Contains(msg, "ensure that git is installed"),
		strings.Contains(msg, "git: not found"),
		strings.Contains(msg, "git: command not found"),
		strings.Contains(msg, "command not found: git"):
		return true
	case strings.Contains(msg, "git operation failed"):
		for _, cause := range gitCloneFailureCauses {
			if strings.Contains(msg, cause) {
				return false
			}
		}
		return true
	}
	return false
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
	if hints.Transport == "stdio" {
		msg := err.Error()
		lmsg := strings.ToLower(msg)
		switch {
		// Wrong-arch / non-executable host binary (ENOEXEC). Guarded against
		// docker OCI context ("oci runtime"/"runc") so a real containerized
		// exec-format failure still falls through to classifyDocker → OCI; a
		// BARE "exec format error" is a host stdio problem, not a Docker one.
		case strings.Contains(lmsg, "exec format error") &&
			!strings.Contains(lmsg, "oci runtime") && !strings.Contains(lmsg, "runc"):
			return STDIOSpawnExecFormat
		case strings.Contains(lmsg, "no such file or directory"),
			strings.Contains(lmsg, "executable file not found"),
			strings.Contains(lmsg, "command not found"):
			return STDIOSpawnENOENT
		case strings.Contains(lmsg, "permission denied"):
			return STDIOSpawnEACCES
		// Subprocess started but the transport closed before the MCP initialize
		// handshake completed — the child exited early (e.g. printed a fatal
		// config error to stderr and died). mcp-go surfaces this as a closed
		// transport, which otherwise falls through to MCPX_UNKNOWN_UNCLASSIFIED
		// even though the real cause is on the child's stderr (MCP-1093 / #599).
		// Gated on the stdio hint so a "transport closed" from another transport
		// is not misattributed.
		case strings.Contains(lmsg, "transport closed"),
			strings.Contains(lmsg, "exited before completing the mcp initialize"),
			strings.Contains(lmsg, "exited before the mcp initialize"):
			return STDIOExitBeforeInitialize
		case strings.Contains(lmsg, "did not respond to mcp initialize"),
			strings.Contains(lmsg, "handshake timeout"):
			return STDIOHandshakeTimeout
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
