package diagnostics

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"syscall"
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

// classifyStdio handles os/exec spawn errors and handshake failures.
func classifyStdio(err error, hints ClassifierHints) Code {
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
		case strings.Contains(lmsg, "no such file or directory"),
			strings.Contains(lmsg, "executable file not found"),
			strings.Contains(lmsg, "command not found"):
			return STDIOSpawnENOENT
		case strings.Contains(lmsg, "permission denied"):
			return STDIOSpawnEACCES
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
// structured HTTP status errors. HTTP status classification requires the
// caller to wrap a statusError — see DiagnoseHTTPStatus below.
func classifyHTTP(err error, hints ClassifierHints) Code {
	_ = hints

	// DNS lookup errors are reported as *net.DNSError.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return HTTPDNSFailed
	}

	// TLS verification: mcp-go surfaces these as *tls.CertificateVerificationError
	// in recent releases; we avoid a direct import dependency by string match.
	msg := err.Error()
	if strings.Contains(msg, "x509:") || strings.Contains(msg, "tls: ") || strings.Contains(msg, "certificate") {
		return HTTPTLSFailed
	}

	// Connection refused — syscall.ECONNREFUSED wrapped by net.OpError.
	if errors.Is(err, syscall.ECONNREFUSED) {
		return HTTPConnRefuse
	}

	return ""
}

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
