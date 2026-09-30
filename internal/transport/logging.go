package transport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// LoggingTransport wraps http.RoundTripper to log all HTTP traffic including SSE frames
type LoggingTransport struct {
	base   http.RoundTripper
	logger *zap.Logger
	mu     sync.Mutex
	// maskNames returns the live forward_headers allowlist of the server this
	// transport serves (Spec 112 FR-018). May be nil.
	maskNames func() []string
}

// forwardedMask is the placeholder printed instead of a forwarded value.
const forwardedMask = "[forwarded]"

// maskForwarded returns a copy of h in which every header whose name is in the
// request's forwarded set (key B) or in the server's allowlist carries the
// placeholder instead of its value. Names stay visible (FR-015a). It runs
// before oauth.RedactHeaders so arbitrary allowlisted names RedactHeaders does
// not know (X-Tenant-Id) are still masked.
func (t *LoggingTransport) maskForwarded(ctx context.Context, h http.Header) http.Header {
	names := map[string]struct{}{}
	if out, ok := headerfwd.OutboundFrom(ctx); ok {
		for _, n := range out.Names() {
			names[http.CanonicalHeaderKey(n)] = struct{}{}
		}
	}
	if t.maskNames != nil {
		for _, n := range t.maskNames() {
			names[http.CanonicalHeaderKey(strings.TrimSpace(n))] = struct{}{}
		}
	}
	if len(names) == 0 {
		return h
	}
	c := h.Clone()
	for n := range names {
		if _, ok := c[n]; ok {
			c[n] = []string{forwardedMask}
		}
	}
	return c
}

// scrubHeaderValues runs every value of h through scrub, so a forwarded value
// echoed under a header NAME maskForwarded does not know (X-Echo-User,
// Location, WWW-Authenticate) is still removed. h is cloned only when a value
// changes.
func scrubHeaderValues(h http.Header, scrub func(string) string) http.Header {
	var c http.Header
	for k, vs := range h {
		for i, v := range vs {
			if nv := scrub(v); nv != v {
				if c == nil {
					c = h.Clone()
				}
				c[k][i] = nv
			}
		}
	}
	if c == nil {
		return h
	}
	return c
}

// bodyScrubber returns the text scrubber for the bodies and SSE frames of ONE
// round trip: forwarded values (key B of ctx) and the server's allowlisted
// names first, then the shape-based credential scrubber. An upstream that
// echoes a forwarded header into a result or an error body would otherwise
// have the value written to the log verbatim (Spec 112 FR-015b, FR-016).
func (t *LoggingTransport) bodyScrubber(ctx context.Context) func(string) string {
	out, ok := headerfwd.OutboundFrom(ctx)
	if !ok || out.IsEmpty() {
		return oauth.ScrubUpstreamText
	}
	var allow []string
	if t.maskNames != nil {
		allow = t.maskNames()
	}
	return func(text string) string {
		return oauth.ScrubUpstreamText(headerfwd.Scrub(text, out, allow))
	}
}

// NewLoggingTransport creates a new logging HTTP transport
func NewLoggingTransport(base http.RoundTripper, logger *zap.Logger) *LoggingTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &LoggingTransport{
		base:   base,
		logger: logger.Named("http-trace"),
	}
}

// RoundTrip implements http.RoundTripper with comprehensive logging
func (t *LoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	startTime := time.Now()

	// Log request.
	//
	// Issue #1158: BOTH sinks in this file get the same scrubbed string, never
	// one and not the other — the Printf copy goes to the operator's terminal
	// and an observer-only test would show green against a zap-only fix.
	// The method, host and path survive; only credentials are replaced.
	fmt.Printf("📤 HTTP REQUEST: %s %s\n", req.Method, oauth.AuditRedaction.URLValueDeep(req.URL.String()))
	scrub := t.bodyScrubber(req.Context())
	fmt.Printf("   Headers: %v\n", oauth.RedactHeaders(scrubHeaderValues(t.maskForwarded(req.Context(), req.Header), scrub)))

	// Log request body if present (for non-SSE requests)
	if req.Body != nil && req.Method != "GET" {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if len(bodyBytes) > 0 && len(bodyBytes) < 10000 {
				// An OAuth token-exchange body carries client_secret and
				// refresh_token; ScrubUpstreamText rewrites only the
				// recognised credential shapes, so the rest of the JSON stays
				// byte-identical and trace mode keeps its purpose.
				t.logger.Debug("📤 REQUEST BODY", zap.String("body", scrub(string(bodyBytes))))
			}
		}
	}

	// Execute request
	resp, err := t.base.RoundTrip(req)
	duration := time.Since(startTime)

	if err != nil {
		// #1148: the transport error quotes the request URL, credentials and
		// all. Both sinks get the redacted rendering; the error itself is
		// returned untouched to the caller.
		// Spec 112: the per-request scrubber also masks forwarded values.
		safeErr := scrub(err.Error())
		fmt.Printf("❌ HTTP REQUEST FAILED: %v (duration: %v)\n", safeErr, duration)
		t.logger.Error("❌ HTTP REQUEST FAILED",
			zap.String("error", safeErr),
			zap.Duration("duration", duration))
		return nil, err
	}

	// Log response using fmt.Printf
	fmt.Printf("📥 HTTP RESPONSE: %d %s (duration: %v)\n", resp.StatusCode, resp.Status, duration)
	safeRespHeaders := oauth.RedactHeaders(scrubHeaderValues(t.maskForwarded(req.Context(), resp.Header), scrub))
	fmt.Printf("   Response Headers: %v\n", safeRespHeaders)

	t.logger.Info("📥 HTTP RESPONSE",
		zap.Int("status", resp.StatusCode),
		zap.String("status_text", resp.Status),
		zap.Any("headers", safeRespHeaders),
		zap.Duration("duration", duration))

	// Check if this is an SSE connection
	contentType := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(contentType, "text/event-stream")

	if isSSE {
		fmt.Println("🌊 SSE STREAM DETECTED - Starting frame-by-frame logging")
		t.logger.Info("🌊 SSE STREAM DETECTED - Starting frame-by-frame logging")
		resp.Body = newSSELoggingReader(resp.Body, t.logger, scrub)
	} else {
		// For regular HTTP responses, log body
		resp.Body = newLoggingReader(resp.Body, t.logger, false, scrub)
	}

	return resp, nil
}

// loggingReader wraps io.ReadCloser to log response body
type loggingReader struct {
	rc      io.ReadCloser
	logger  *zap.Logger
	isSSE   bool
	frameID int
	buffer  *bytes.Buffer
	// scrub redacts every body byte and SSE frame before it reaches stdout or
	// zap. Never nil.
	scrub func(string) string
}

func newLoggingReader(rc io.ReadCloser, logger *zap.Logger, isSSE bool, scrub func(string) string) io.ReadCloser {
	if scrub == nil {
		scrub = oauth.ScrubUpstreamText
	}
	return &loggingReader{
		rc:     rc,
		logger: logger,
		isSSE:  isSSE,
		buffer: &bytes.Buffer{},
		scrub:  scrub,
	}
}

func newSSELoggingReader(rc io.ReadCloser, logger *zap.Logger, scrub func(string) string) io.ReadCloser {
	if scrub == nil {
		scrub = oauth.ScrubUpstreamText
	}
	// Create a pipe to tee the SSE stream
	pr, pw := io.Pipe()

	// Tee reader sends data to both the original consumer (mcp-go) and our logger
	teeReader := io.TeeReader(rc, pw)

	lr := &loggingReader{
		rc:     io.NopCloser(teeReader),
		logger: logger,
		isSSE:  true,
		buffer: &bytes.Buffer{},
		scrub:  scrub,
	}

	// Start background goroutine to read and log SSE frames from the tee'd pipe
	go func() {
		defer pw.Close()
		lr.readSSEFramesFromPipe(pr)
	}()

	return lr
}

func (lr *loggingReader) readSSEFramesFromPipe(pr *io.PipeReader) {
	fmt.Println("🌊 SSE frame reader goroutine started")
	defer pr.Close()
	scanner := bufio.NewScanner(pr)
	var currentFrame strings.Builder
	var eventType string
	var dataContent string
	frameStartTime := time.Now()

	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("   📜 Raw SSE line: %q\n", lr.scrub(line))

		// Empty line indicates end of frame
		if line == "" {
			if currentFrame.Len() > 0 {
				lr.frameID++
				frameDuration := time.Since(frameStartTime)

				frameContent := currentFrame.String()
				safeData := lr.scrub(dataContent)
				safeEvent := lr.scrub(eventType)
				safeContent := lr.scrub(frameContent)
				fmt.Printf("🔵 SSE FRAME #%d (event: %s, data: %s, duration since prev: %v)\n%s\n",
					lr.frameID, safeEvent, safeData, frameDuration, safeContent)
				lr.logger.Info(fmt.Sprintf("🔵 SSE FRAME #%d", lr.frameID),
					zap.String("event", safeEvent),
					zap.String("data", safeData),
					zap.String("content", safeContent),
					zap.Duration("time_since_prev", frameDuration),
					zap.Time("timestamp", time.Now()))

				// Reset for next frame
				currentFrame.Reset()
				eventType = ""
				dataContent = ""
				frameStartTime = time.Now()
			}
			continue
		}

		// Parse SSE fields
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			currentFrame.WriteString(line + "\n")
		} else if strings.HasPrefix(line, "data:") {
			dataContent = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			currentFrame.WriteString("data: " + dataContent + "\n")
		} else if strings.HasPrefix(line, "id:") {
			currentFrame.WriteString(line + "\n")
		} else if strings.HasPrefix(line, "retry:") {
			currentFrame.WriteString(line + "\n")
		} else if strings.HasPrefix(line, ":") {
			// Comment line
			currentFrame.WriteString(line + "\n")
		} else {
			currentFrame.WriteString(line + "\n")
		}
	}

	if err := scanner.Err(); err != nil {
		// #1148 round 4: a stream read can fail with a *url.Error that quotes
		// the request URL, credentials and all.
		lr.logger.Error("❌ SSE STREAM ERROR", zap.String("error", lr.scrub(err.Error())))
	}

	lr.logger.Info("🔴 SSE STREAM CLOSED",
		zap.Int("total_frames", lr.frameID),
		zap.Duration("total_duration", time.Since(frameStartTime)))
}

func (lr *loggingReader) Read(p []byte) (n int, err error) {
	// For SSE, the background goroutine handles logging
	// For regular responses, log the body
	n, err = lr.rc.Read(p)

	if !lr.isSSE && n > 0 {
		lr.buffer.Write(p[:n])
	}

	if err == io.EOF && !lr.isSSE && lr.buffer.Len() > 0 {
		body := lr.buffer.String()
		if len(body) < 10000 {
			lr.logger.Debug("📥 RESPONSE BODY", zap.String("body", lr.scrub(body)))
		} else {
			lr.logger.Debug("📥 RESPONSE BODY (truncated)",
				zap.Int("total_size", len(body)),
				zap.String("preview", scrubbedPreviewWith(body, 1000, lr.scrub)+"..."))
		}
	}

	return n, err
}

func (lr *loggingReader) Close() error {
	if lr.isSSE {
		lr.logger.Info("🔴 Closing SSE stream")
	}
	return lr.rc.Close()
}

// scrubbedPreview redacts a response body and THEN truncates it (issue #1158,
// review round 2 minor).
//
// The order matters and the old one was wrong: `ScrubUpstreamText(body[:1000])`
// cut the body first, so a credential straddling byte 1000 was handed to the
// detectors as a FRAGMENT. Every vendor-shaped matcher is anchored on a
// complete token — a `ghp_` of the right length, a JWT with three segments, a
// `<name>=<value>` pair with a value — so the fragment matched nothing, and the
// leading bytes of a real secret were published while the log line claimed to
// be redacted. Scrubbing the whole body first means the matchers see the
// complete token; only the masked rendering is then cut.
//
// The cut is moved back to a rune boundary because the mask rendering is a run
// of multi-byte U+2022 bullets, and slicing one in half produces invalid UTF-8
// that zap escapes into noise.
func scrubbedPreview(body string, limit int) string {
	return scrubbedPreviewWith(body, limit, oauth.ScrubUpstreamText)
}

// scrubbedPreviewWith is scrubbedPreview with the caller's scrubber (the
// per-round-trip one that also removes forwarded header values).
func scrubbedPreviewWith(body string, limit int, scrub func(string) string) string {
	scrubbed := scrub(body)
	if len(scrubbed) <= limit {
		return scrubbed
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(scrubbed[cut]) {
		cut--
	}
	return scrubbed[:cut]
}
