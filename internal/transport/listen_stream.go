package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"strings"
	"time"

	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// GH #1910: the standalone GET stream of a streamable-HTTP upstream.
//
// The streamable HTTP transport (legacy protocol revisions, which mcpproxy pins
// on the upstream hop: spec 058 FR-027) delivers server-initiated messages
// outside a request, notifications/tools/list_changed among them, only on a GET
// the client keeps open. mcp-go can open one (transport.WithContinuousListening)
// but its listener does not fit an aggregating proxy, so mcpproxy runs its own:
//
//   - the option is fixed when the transport is built, before initialize, so it
//     cannot depend on the server advertising tools.listChanged; every upstream
//     would hold a GET, scale-to-zero ones included;
//   - mcp-go runs it on the Start context, which on the HTTP branches is the
//     connect attempt's context, and on the basic client, whose 180s total
//     timeout cuts a long-lived stream;
//   - it stops only on 405 and 404: any other refusal (400, 406, 401) or failure
//     is retried every second forever, logged at Error each time;
//   - after a 404 it stops for good, so a session re-initialize (Spec 113-e)
//     would silently lose it.
//
// ListenStream is one GET at a time, with the session id, protocol version,
// static headers and OAuth bearer of the transport it belongs to. A refusal ends
// it after one request; a transient failure backs off exponentially to a cap.
// It never touches the mcp-go transport's state: a 404 here does not clear the
// session id the POSTs use.

// ListenEndReason says why ListenStream.Run returned.
type ListenEndReason int

const (
	// ListenStopped: the context was cancelled (close, reconnect, re-init,
	// retirement).
	ListenStopped ListenEndReason = iota
	// ListenUnsupported: the server refused the GET (405, 400, 406, 401, 403,
	// another 4xx, or a non-event-stream answer). Polling covers it.
	ListenUnsupported
	// ListenSessionTerminated: the server answered 404 for this session. A
	// session re-initialize or a reconnect starts a new stream.
	ListenSessionTerminated
)

func (r ListenEndReason) String() string {
	switch r {
	case ListenStopped:
		return "stopped"
	case ListenUnsupported:
		return "unsupported"
	case ListenSessionTerminated:
		return "session_terminated"
	default:
		return fmt.Sprintf("ListenEndReason(%d)", int(r))
	}
}

// ListenEnd is the result of ListenStream.Run.
type ListenEnd struct {
	Reason ListenEndReason
	// Status is the HTTP status of the refusing response, 0 when none.
	Status int
	// Detail is a short, credential-free description for one log line.
	Detail string
}

// ListenStreamOptions tunes a ListenStream. Zero durations take the defaults.
type ListenStreamOptions struct {
	// ProtocolVersion is sent as Mcp-Protocol-Version (the negotiated one).
	ProtocolVersion string
	// OnNotification receives every notification the stream carries. It runs
	// on the stream's goroutine and is never called after Run's context ends.
	OnNotification func(mcp.JSONRPCNotification)
	Logger         *zap.Logger

	// MinBackoff is the first retry delay after a transient failure and the
	// delay before re-opening a stream that ended after being healthy.
	MinBackoff time.Duration
	// MaxBackoff caps the exponential retry delay.
	MaxBackoff time.Duration
	// HealthyAfter is how long a stream must stay open for its end to count
	// as normal (backoff reset) rather than as a failure.
	HealthyAfter time.Duration
}

const (
	defaultListenMinBackoff   = time.Second
	defaultListenMaxBackoff   = 2 * time.Minute
	defaultListenHealthyAfter = 30 * time.Second
	// listenMaxEventBytes bounds one SSE event (and so one line); a
	// notification is tiny. A larger event is dropped whole.
	listenMaxEventBytes = 1 << 20
	// listenResponseTimeout bounds the POST answering a request on the stream.
	listenResponseTimeout = 10 * time.Second
)

// ListenStream holds the standalone GET stream of one streamable-HTTP
// transport. Build it with HTTPTransportConfig.NewListenStream; Run it once.
type ListenStream struct {
	url             string
	client          *http.Client
	closeIdle       func()
	headers         map[string]string
	sessionID       func() string
	authorization   func(context.Context) (string, error)
	protocolVersion string
	onNotification  func(mcp.JSONRPCNotification)
	logger          *zap.Logger
	minBackoff      time.Duration
	maxBackoff      time.Duration
	healthyAfter    time.Duration
}

// listenHTTPClient is the client of the GET stream. It has no total timeout,
// which would cut the stream (the plain branch's POST client has 180s); a
// response-header timeout bounds a server that never answers. It omits the
// Retry-After recorder: a rate-limited GET must not park the next reconnect
// (#1040). The transport is the stream's own, so closeIdle (run when Run
// returns) leaves no idle connection behind.
func (cfg *HTTPTransportConfig) listenHTTPClient(logger *zap.Logger) (client *http.Client, closeIdle func()) {
	tuned := defaultHTTPTransport()
	tuned.ResponseHeaderTimeout = 30 * time.Second
	base := cfg.baseTransport(tuned)
	closeIdle = func() {}
	if t, ok := base.(*http.Transport); ok {
		closeIdle = t.CloseIdleConnections
	}
	noRecorder := *cfg
	noRecorder.RetryAfter = nil
	return &http.Client{
		Transport:     noRecorder.upstreamRoundTripper(base, logger),
		CheckRedirect: listenRedirectPolicy,
	}, closeIdle
}

// listenRedirectPolicy follows a same-origin redirect only. Go strips just
// Authorization and Cookie on a cross-origin hop, so the transport's static
// headers (an API key, say) would reach the other origin; the GET stream is
// optional, so a cross-origin redirect ends it (the 3xx reads as a refusal)
// and polling covers the server.
func listenRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && !sameOrigin(req, via[0]) {
		return http.ErrUseLastResponse
	}
	return forwardedRedirectFilter(req, via)
}

func defaultHTTPTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment}
}

// NewListenStream builds the GET stream for sh, the mcp-go transport this
// config produced (CreateHTTPClient). The session id is read from sh on every
// attempt and the bearer comes from sh's own OAuth handler, so the stream
// authenticates exactly like the transport's POSTs; cfg.Headers are the static
// headers that transport sends.
func (cfg *HTTPTransportConfig) NewListenStream(sh *mcptransport.StreamableHTTP, opts ListenStreamOptions) *ListenStream {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	httpClient, closeIdle := cfg.listenHTTPClient(logger)
	ls := &ListenStream{
		url:             cfg.URL,
		client:          httpClient,
		closeIdle:       closeIdle,
		headers:         cfg.Headers,
		sessionID:       sh.GetSessionId,
		protocolVersion: opts.ProtocolVersion,
		onNotification:  opts.OnNotification,
		logger:          logger,
		minBackoff:      orDefault(opts.MinBackoff, defaultListenMinBackoff),
		maxBackoff:      orDefault(opts.MaxBackoff, defaultListenMaxBackoff),
		healthyAfter:    orDefault(opts.HealthyAfter, defaultListenHealthyAfter),
	}
	if ls.maxBackoff < ls.minBackoff {
		ls.maxBackoff = ls.minBackoff
	}
	if h := sh.GetOAuthHandler(); h != nil {
		ls.authorization = h.GetAuthorizationHeader
	}
	return ls
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// attemptOutcome classifies one GET.
type attemptOutcome int

const (
	attemptTransient attemptOutcome = iota // retry after backoff
	attemptHealthy                         // stream ended after HealthyAfter: reset backoff
	attemptFinal                           // Run returns
)

// Run holds the stream until ctx ends or the server refuses it, re-opening it
// after a transient failure or a normal end. It returns why it stopped.
func (s *ListenStream) Run(ctx context.Context) ListenEnd {
	defer s.closeIdle()
	delay := s.minBackoff
	for {
		if ctx.Err() != nil {
			return ListenEnd{Reason: ListenStopped}
		}
		outcome, end, retryAfter := s.attempt(ctx)
		switch outcome {
		case attemptFinal:
			return end
		case attemptHealthy:
			delay = s.minBackoff
		}
		if ctx.Err() != nil {
			return ListenEnd{Reason: ListenStopped}
		}
		wait := jitter(delay)
		if retryAfter > wait {
			wait = retryAfter
		}
		if wait > s.maxBackoff {
			wait = s.maxBackoff
		}
		if outcome == attemptTransient {
			delay *= 2
			if delay > s.maxBackoff {
				delay = s.maxBackoff
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ListenEnd{Reason: ListenStopped}
		case <-t.C:
		}
	}
}

// jitter spreads d over [0.75d, 1.25d) so many upstreams dropped together do
// not reconnect in lockstep.
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.75 + rand.Float64()*0.5)) //nolint:gosec // G404: timing jitter, not security
}

// attempt opens one GET and reads it until it ends.
func (s *ListenStream) attempt(ctx context.Context) (attemptOutcome, ListenEnd, time.Duration) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return attemptFinal, ListenEnd{Reason: ListenUnsupported, Detail: "invalid upstream URL"}, 0
	}
	// The session this stream belongs to: a request received on it is
	// answered on the same session even if the transport re-initializes.
	sid := s.sessionID()
	if err := s.setHeaders(ctx, req.Header, "text/event-stream", sid); err != nil {
		if ctx.Err() != nil {
			return attemptFinal, ListenEnd{Reason: ListenStopped}, 0
		}
		if errors.Is(err, mcptransport.ErrOAuthAuthorizationRequired) {
			return attemptFinal, ListenEnd{Reason: ListenUnsupported, Detail: "authorization required"}, 0
		}
		s.logger.Debug("Upstream GET stream: no authorization header, retrying", zap.String("error", scrubErr(err)))
		return attemptTransient, ListenEnd{}, 0
	}

	opened := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return attemptFinal, ListenEnd{Reason: ListenStopped}, 0
		}
		s.logger.Debug("Upstream GET stream request failed, retrying", zap.String("error", scrubErr(err)))
		return attemptTransient, ListenEnd{}, 0
	}
	// Close without draining: a refusal whose body never finishes must not
	// hold the listener, and the connection is not reused (closeIdle).
	defer resp.Body.Close()

	switch code := resp.StatusCode; {
	case code == http.StatusOK:
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if mediaType != "text/event-stream" {
			return attemptFinal, ListenEnd{Reason: ListenUnsupported, Status: code,
				Detail: "GET answered without an event stream"}, 0
		}
	case code == http.StatusNotFound:
		return attemptFinal, ListenEnd{Reason: ListenSessionTerminated, Status: code}, 0
	case code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500:
		ra, _ := ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		s.logger.Debug("Upstream GET stream: transient refusal, retrying", zap.Int("status", code))
		return attemptTransient, ListenEnd{}, ra
	default:
		return attemptFinal, ListenEnd{Reason: ListenUnsupported, Status: code,
			Detail: fmt.Sprintf("GET answered HTTP %d", code)}, 0
	}

	s.logger.Debug("Upstream GET stream open")
	s.read(ctx, resp.Body, sid)
	if ctx.Err() != nil {
		return attemptFinal, ListenEnd{Reason: ListenStopped}, 0
	}
	if time.Since(opened) >= s.healthyAfter {
		return attemptHealthy, ListenEnd{}, 0
	}
	return attemptTransient, ListenEnd{}, 0
}

// setHeaders applies the transport's session, protocol, static and OAuth
// headers, in the order mcp-go's sendHTTP applies them.
func (s *ListenStream) setHeaders(ctx context.Context, h http.Header, accept, sid string) error {
	h.Set("Accept", accept)
	if sid != "" {
		h.Set(mcptransport.HeaderKeySessionID, sid)
	}
	if s.protocolVersion != "" {
		h.Set(mcptransport.HeaderKeyProtocolVersion, s.protocolVersion)
	}
	for k, v := range s.headers {
		h.Set(k, v)
	}
	if s.authorization != nil {
		v, err := s.authorization(ctx)
		if err != nil {
			return err
		}
		h.Set("Authorization", v)
	}
	return nil
}

// read parses the SSE stream until it ends or ctx is cancelled (the request
// context: cancelling it fails the pending read).
func (s *ListenStream) read(ctx context.Context, body io.Reader, sid string) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 4096), listenMaxEventBytes)
	var data strings.Builder
	oversized := false // the current event passed listenMaxEventBytes: drop it
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			if data.Len() > 0 && !oversized {
				s.handle(ctx, []byte(data.String()), sid)
			}
			data.Reset()
			oversized = false
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok && !oversized {
			v = strings.TrimPrefix(v, " ")
			if data.Len()+1+len(v) > listenMaxEventBytes {
				s.logger.Debug("Upstream GET stream: dropping an oversized event")
				data.Reset()
				oversized = true
				continue
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(v)
		}
		// event:, id:, retry: and comments carry nothing the handler needs.
	}
	if data.Len() > 0 && !oversized && sc.Err() == nil {
		s.handle(ctx, []byte(data.String()), sid)
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		s.logger.Debug("Upstream GET stream ended with a read error", zap.String("error", scrubErr(err)))
	}
}

// handle dispatches one JSON-RPC message from the stream: a notification goes
// to OnNotification, a request is answered, a response is ignored (nothing is
// sent on this stream, so nothing can be answered on it).
func (s *ListenStream) handle(ctx context.Context, raw []byte, sid string) {
	if ctx.Err() != nil {
		return
	}
	var head struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Method == "" {
		return
	}
	hasID := len(head.ID) > 0 && !bytes.Equal(bytes.TrimSpace(head.ID), []byte("null"))
	if !hasID {
		var n mcp.JSONRPCNotification
		if err := json.Unmarshal(raw, &n); err != nil {
			return
		}
		if s.onNotification != nil {
			s.onNotification(n)
		}
		return
	}
	s.answer(ctx, head.ID, head.Method, sid)
}

// answer replies to a server-to-client request received on the stream, on the
// session that opened the stream (sid).
// mcpproxy declares no client capabilities on the upstream hop, so ping is
// the only request it serves; anything else gets method-not-found rather than
// leaving the server waiting.
func (s *ListenStream) answer(ctx context.Context, id json.RawMessage, method, sid string) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if method == string(mcp.MethodPing) {
		msg["result"] = map[string]any{}
	} else {
		msg["error"] = map[string]any{"code": mcp.METHOD_NOT_FOUND, "message": "method not supported by mcpproxy: " + method}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, listenResponseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if err := s.setHeaders(rctx, req.Header, "application/json, text/event-stream", sid); err != nil {
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Debug("Upstream GET stream: answering a server request failed",
			zap.String("method", method), zap.String("error", scrubErr(err)))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
}

// scrubErr renders a transport error without the URL credentials it may quote
// (#1148).
func scrubErr(err error) string {
	if err == nil {
		return ""
	}
	return oauth.ScrubUpstreamText(err.Error())
}
