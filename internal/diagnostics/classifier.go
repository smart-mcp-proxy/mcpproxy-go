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

	if c := classifyStdio(err, hints); c != "" {
		return c
	}
	if c := classifyHTTP(err, hints); c != "" {
		return c
	}
	if c := classifyNetwork(err, hints); c != "" {
		return c
	}
	// Domain-specific classifiers for OAUTH/DOCKER/CONFIG/QUARANTINE live in
	// their respective files (to be populated in later phases). For now they
	// fall through to UnknownUnclassified.

	return UnknownUnclassified
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

	// String-match fallback for handshake invalid frames (mcp-go does not
	// expose a structured error type for this at time of writing).
	if hints.Transport == "stdio" {
		msg := err.Error()
		if strings.Contains(msg, "invalid handshake") || strings.Contains(msg, "malformed") {
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
