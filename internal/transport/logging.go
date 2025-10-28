package transport

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// LoggingTransport wraps http.RoundTripper to log all HTTP traffic including SSE frames
type LoggingTransport struct {
	base   http.RoundTripper
	logger *zap.Logger
	mu     sync.Mutex
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

	// Log request
	fmt.Printf("📤 HTTP REQUEST: %s %s\n", req.Method, req.URL.String())
	fmt.Printf("   Headers: %v\n", req.Header)

	// Log request body if present (for non-SSE requests)
	if req.Body != nil && req.Method != "GET" {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if len(bodyBytes) > 0 && len(bodyBytes) < 10000 {
				t.logger.Debug("📤 REQUEST BODY", zap.String("body", string(bodyBytes)))
			}
		}
	}

	// Execute request
	resp, err := t.base.RoundTrip(req)
	duration := time.Since(startTime)

	if err != nil {
		fmt.Printf("❌ HTTP REQUEST FAILED: %v (duration: %v)\n", err, duration)
		t.logger.Error("❌ HTTP REQUEST FAILED",
			zap.Error(err),
			zap.Duration("duration", duration))
		return nil, err
	}

	// Log response using fmt.Printf
	fmt.Printf("📥 HTTP RESPONSE: %d %s (duration: %v)\n", resp.StatusCode, resp.Status, duration)
	fmt.Printf("   Response Headers: %v\n", resp.Header)

	t.logger.Info("📥 HTTP RESPONSE",
		zap.Int("status", resp.StatusCode),
		zap.String("status_text", resp.Status),
		zap.Any("headers", resp.Header),
		zap.Duration("duration", duration))

	// Check if this is an SSE connection
	contentType := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(contentType, "text/event-stream")

	if isSSE {
		fmt.Println("🌊 SSE STREAM DETECTED - Starting frame-by-frame logging")
		t.logger.Info("🌊 SSE STREAM DETECTED - Starting frame-by-frame logging")
		resp.Body = newSSELoggingReader(resp.Body, t.logger)
	} else {
		// For regular HTTP responses, log body
		resp.Body = newLoggingReader(resp.Body, t.logger, false)
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
}

func newLoggingReader(rc io.ReadCloser, logger *zap.Logger, isSSE bool) io.ReadCloser {
	return &loggingReader{
		rc:     rc,
		logger: logger,
		isSSE:  isSSE,
		buffer: &bytes.Buffer{},
	}
}

func newSSELoggingReader(rc io.ReadCloser, logger *zap.Logger) io.ReadCloser {
	// Create a pipe to tee the SSE stream
	pr, pw := io.Pipe()

	// Tee reader sends data to both the original consumer (mcp-go) and our logger
	teeReader := io.TeeReader(rc, pw)

	lr := &loggingReader{
		rc:     io.NopCloser(teeReader),
		logger: logger,
		isSSE:  true,
		buffer: &bytes.Buffer{},
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
		fmt.Printf("   📜 Raw SSE line: %q\n", line)

		// Empty line indicates end of frame
		if line == "" {
			if currentFrame.Len() > 0 {
				lr.frameID++
				frameDuration := time.Since(frameStartTime)

				frameContent := currentFrame.String()
				fmt.Printf("🔵 SSE FRAME #%d (event: %s, data: %s, duration since prev: %v)\n%s\n",
					lr.frameID, eventType, dataContent, frameDuration, frameContent)
				lr.logger.Info(fmt.Sprintf("🔵 SSE FRAME #%d", lr.frameID),
					zap.String("event", eventType),
					zap.String("data", dataContent),
					zap.String("content", frameContent),
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
		lr.logger.Error("❌ SSE STREAM ERROR", zap.Error(err))
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
			lr.logger.Debug("📥 RESPONSE BODY", zap.String("body", body))
		} else {
			lr.logger.Debug("📥 RESPONSE BODY (truncated)",
				zap.Int("total_size", len(body)),
				zap.String("preview", body[:1000]+"..."))
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
