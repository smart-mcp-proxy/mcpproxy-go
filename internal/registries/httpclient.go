package registries

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	// registryRequestTimeout bounds a SINGLE registry HTTP request (connect + TLS
	// handshake + awaiting response headers + body read). The official registry's
	// deep-cursor pages can be slow under load, so this is deliberately more
	// forgiving than a snappy localhost call. Retries layer on top via
	// registryMaxAttempts so one slow page no longer aborts the whole listing
	// (root fix for the "Official MCP Registry returned no results" timeout).
	registryRequestTimeout = 15 * time.Second

	// registryMaxAttempts is the total number of attempts (1 initial + retries)
	// for an idempotent registry GET before giving up.
	registryMaxAttempts = 3
)

// registryRetryBaseDelay is the first backoff; each subsequent retry doubles it
// (500ms, then 1s). A var (not const) so tests can shrink it.
var registryRetryBaseDelay = 500 * time.Millisecond

var (
	registryHTTPClientOnce sync.Once
	registryHTTPClient     *http.Client
)

// sharedRegistryClient returns a process-wide HTTP client tuned for registry
// fetches: connection keep-alives are reused across the cursor-follow loop, and
// a per-request Timeout caps any single attempt so one slow page cannot stall
// the whole listing. Retries are handled separately by registryGet.
func sharedRegistryClient() *http.Client {
	registryHTTPClientOnce.Do(func() {
		registryHTTPClient = buildRegistryClient()
	})
	return registryHTTPClient
}

// buildRegistryClient constructs the tuned registry HTTP client.
func buildRegistryClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 20
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{
		Timeout:   registryRequestTimeout,
		Transport: transport,
	}
}

// registryGet performs an idempotent GET against a registry endpoint with the
// standard headers (Accept JSON, versioned User-Agent, and any configured key)
// and returns the fully-read response body on a 200. Transient failures are
// retried with exponential backoff: connection errors, per-request timeouts
// (including ones that fire mid-body-read — http.Client.Timeout covers the whole
// request, so the body is read INSIDE the attempt loop), and 5xx/429 responses.
// The parent ctx bounds the whole operation — once it is done, no further
// attempts are made. A non-2xx final status returns an error.
func registryGet(ctx context.Context, reg *RegistryEntry, reqURL string) ([]byte, error) {
	client := sharedRegistryClient()

	var lastErr error
	for attempt := 1; attempt <= registryMaxAttempts; attempt++ {
		if attempt > 1 {
			// Back off before retrying, but bail out immediately if the parent
			// context is already done.
			delay := registryRetryBaseDelay * time.Duration(1<<(attempt-2))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
		if err != nil {
			// A malformed request is not transient — fail fast.
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		// Some registries reject empty/bare User-Agents (issue #566).
		req.Header.Set("User-Agent", registryUserAgent())
		// Opt-in registries (RequiresKey, e.g. Smithery) authenticate via their
		// configured key.
		applyRegistryAuth(req, reg)

		// transientErr is set when a request/response/body-read error is worth
		// retrying; it is decided against the PARENT ctx, never the error value.
		// NOTE: a per-request Client.Timeout (incl. one firing during the body
		// read) surfaces as context.DeadlineExceeded, so inspecting the parent
		// ctx is what distinguishes "this attempt was slow" (retry) from "the
		// whole operation is over" (stop) — the exact slow-page case this fixes.
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, readErr
			}
			lastErr = readErr
			continue
		}

		// Retry server-side failures while attempts remain.
		if isRetryableStatus(resp.StatusCode) && attempt < registryMaxAttempts {
			lastErr = fmt.Errorf("registry returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("registry query returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}

		return body, nil
	}

	return nil, lastErr
}

// isRetryableStatus reports whether an HTTP status warrants a retry: server-side
// failures (5xx) and rate limiting (429) are transient; other 4xx client errors
// are not.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}
