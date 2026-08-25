package transport

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// MaxRetryAfterDelay caps how long an upstream-supplied Retry-After may park a
// server. RFC 7231 puts no ceiling on the value, so a misconfigured (or hostile)
// upstream could hand us days. An hour is long enough to respect a real vendor
// rate-limit window and short enough that a bogus hint self-heals without a
// human having to hit "reconnect".
const MaxRetryAfterDelay = time.Hour

// RetryAfterRecorder captures the rate-limit hints observed on one upstream's
// HTTP responses.
//
// It exists because mcp-go flattens every non-2xx response into a bare string
// (`request failed with status %d: %s` in client/transport/streamable_http.go),
// so by the time a connect error reaches the state machine the `Retry-After`
// header is gone. The only layer that still holds the *http.Response is the
// RoundTripper underneath the MCP client — hence RetryAfterTransport below,
// which feeds this recorder (#1040).
//
// A nil *RetryAfterRecorder is usable and inert, so callers that do not care
// about rate-limit hints can pass nil everywhere.
type RetryAfterRecorder struct {
	mu       sync.Mutex
	deadline time.Time
	status   int
}

// NewRetryAfterRecorder returns an empty recorder.
func NewRetryAfterRecorder() *RetryAfterRecorder {
	return &RetryAfterRecorder{}
}

// Record notes that the upstream asked us to wait `delay` as of `now`. The
// delay is clamped to MaxRetryAfterDelay and only ever extends the current
// park window: a later, shorter hint must not shorten a wait the upstream
// already asked for. It returns the effective deadline.
func (r *RetryAfterRecorder) Record(now time.Time, delay time.Duration, status int) time.Time {
	if r == nil || delay <= 0 {
		return time.Time{}
	}
	if delay > MaxRetryAfterDelay {
		delay = MaxRetryAfterDelay
	}

	deadline := now.Add(delay)

	r.mu.Lock()
	defer r.mu.Unlock()
	if deadline.After(r.deadline) {
		r.deadline = deadline
		r.status = status
	}
	return r.deadline
}

// Deadline returns the instant before which no automatic reconnect should be
// attempted, or the zero time when no hint has been observed.
func (r *RetryAfterRecorder) Deadline() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deadline
}

// Status returns the HTTP status that produced the current deadline (0 when
// there is none). Used for logging.
func (r *RetryAfterRecorder) Status() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Clear drops any recorded hint. Called once a connection succeeds so a stale
// window cannot hold back a later, unrelated reconnect.
func (r *RetryAfterRecorder) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deadline = time.Time{}
	r.status = 0
}

// ParseRetryAfter parses an RFC 7231 `Retry-After` value — either delta-seconds
// or an HTTP-date — into a delay relative to now.
//
// ok is false for an absent, malformed, or already-elapsed value; the caller
// then falls back to its normal exponential backoff.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	// delta-seconds
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}

	// HTTP-date
	if t, err := http.ParseTime(value); err == nil {
		if delay := t.Sub(now); delay > 0 {
			return delay, true
		}
	}

	return 0, false
}

// RetryAfterTransport is an http.RoundTripper wrapper that records `Retry-After`
// hints from rate-limited upstream responses into a RetryAfterRecorder. It never
// alters the request or the response — it only observes the status line and one
// header, leaving the body untouched for mcp-go to consume.
type RetryAfterTransport struct {
	base     http.RoundTripper
	recorder *RetryAfterRecorder
	logger   *zap.Logger
}

// NewRetryAfterTransport wraps base so 429 (and 503-with-a-hint) responses feed
// recorder. A nil base means http.DefaultTransport; a nil logger is tolerated.
func NewRetryAfterTransport(base http.RoundTripper, recorder *RetryAfterRecorder, logger *zap.Logger) *RetryAfterTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RetryAfterTransport{
		base:     base,
		recorder: recorder,
		logger:   logger.Named("retry-after"),
	}
}

// RoundTrip implements http.RoundTripper.
func (t *RetryAfterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		// A 429 is always a rate limit, with or without a parseable hint.
	case http.StatusServiceUnavailable:
		// 503 only counts when the upstream actually tells us how long to wait;
		// a bare 503 is an outage, which the normal backoff ladder already paces.
	default:
		return resp, nil
	}

	now := time.Now()
	delay, ok := ParseRetryAfter(resp.Header.Get("Retry-After"), now)
	if !ok {
		return resp, nil
	}

	deadline := t.recorder.Record(now, delay, resp.StatusCode)
	if !deadline.IsZero() {
		t.logger.Info("Upstream asked us to back off",
			zap.Int("status", resp.StatusCode),
			zap.String("url", req.URL.Redacted()),
			zap.Duration("retry_after", delay),
			zap.Time("retry_not_before", deadline))
	}

	return resp, nil
}
