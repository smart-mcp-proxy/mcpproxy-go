// Package launcher manages locally-spawned upstream processes that expose
// their MCP endpoint over HTTP / SSE / streamable-HTTP transports.
//
// Stdio upstreams already spawn a child process — that's how the protocol
// works. HTTP/SSE upstreams have historically required the user to start
// the process themselves before mcpproxy connected. The launcher decouples
// "how to start the process" from "how to talk to it", so the same
// {command, args, env, working_dir, docker isolation} configuration that
// stdio servers use can also drive an HTTP/SSE server's lifecycle.
//
// This file owns URL-readiness probing. See launcher.go for spawn/stop.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// defaultDialPerAttempt is the per-dial timeout while polling. Kept short so
// each retry can fire quickly while the listener is coming up.
const defaultDialPerAttempt = 1 * time.Second

// defaultDialPollInterval is how long we sleep between failed dials. Short
// enough to feel snappy on a fast-starting server, long enough that we don't
// spin the CPU waiting on a slow one.
const defaultDialPollInterval = 200 * time.Millisecond

// WaitForURL blocks until rawURL's host:port accepts a TCP connection, or
// until the context is canceled or timeout elapses (whichever comes first).
//
// The check is deliberately a TCP dial, not an HTTP GET. SSE endpoints serve
// a streaming response that never closes; an HTTP GET against one will
// either hang or return a non-2xx status the moment the server's stream
// handler is hit, neither of which actually proves "the listener is up".
// TCP-dial just proves the bind happened, which is what we need before
// handing off to the transport-level connect.
//
// timeout=0 means "use no overall deadline beyond ctx.Done()". Negative
// timeouts are coerced to 0 to keep callers from accidentally producing an
// already-expired deadline.
func WaitForURL(ctx context.Context, rawURL string, timeout time.Duration) error {
	addr, err := addrFromURL(rawURL)
	if err != nil {
		return err
	}

	if timeout < 0 {
		timeout = 0
	}

	dialCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	dialer := &net.Dialer{Timeout: defaultDialPerAttempt}
	var lastErr error
	for {
		// Honor the deadline / cancel before each attempt so a cancel
		// during sleep is observed promptly.
		if err := dialCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if lastErr != nil {
					return fmt.Errorf("url %s not reachable in %s (last dial error: %w)", rawURL, timeout, lastErr)
				}
				return fmt.Errorf("url %s not reachable in %s", rawURL, timeout)
			}
			return err
		}

		conn, err := dialer.DialContext(dialCtx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err

		select {
		case <-dialCtx.Done():
			// Loop back so the deadline path above formats the error
			// consistently.
			continue
		case <-time.After(defaultDialPollInterval):
		}
	}
}

// addrFromURL extracts host:port from rawURL. If the URL omits a port, a
// default is inferred from the scheme (http→80, https→443). Unsupported
// schemes return an explanatory error so misconfigurations surface early.
func addrFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("url %q has no host component", rawURL)
	}

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("url %q has empty host", rawURL)
	}

	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http", "ws":
			port = "80"
		case "https", "wss":
			port = "443"
		case "":
			return "", fmt.Errorf("url %q has no scheme — cannot infer port", rawURL)
		default:
			return "", fmt.Errorf("url %q has unsupported scheme %q for TCP probe", rawURL, u.Scheme)
		}
	}

	return net.JoinHostPort(host, port), nil
}
