package transport

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// captureStdout runs fn with os.Stdout replaced by a pipe and returns what was
// written. The Printf copy is half the leak in this file: a test that only
// watches zap passes vacuously against a fix that only patches the zap fields.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	require.NoError(t, w.Close())
	out := <-done
	require.NoError(t, r.Close())
	return out
}

func observedText(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, e := range logs.All() {
		b.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			fmt.Fprintf(&b, " %s=%v", k, v)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestLoggingTransport_RedactsURLHeadersAndBodies drives the real RoundTrip
// against an httptest server and watches BOTH sinks.
func TestLoggingTransport_RedactsURLHeadersAndBodies(t *testing.T) {
	const (
		urlSecret     = "SUPERSECRETURLQUERYVALUE"
		reqHdrSecret  = "SUPERSECRETREQHEADERVALUE"
		reqBodySecret = "SUPERSECRETREQBODYVALUE"
		respHdrSecret = "SUPERSECRETRESPHEADERVALUE"
		respBodySecre = "SUPERSECRETRESPBODYVALUE"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "session="+respHdrSecret)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"` + respBodySecre + `"}`))
	}))
	defer srv.Close()

	obsCore, logs := observer.New(zapcore.DebugLevel)
	tr := NewLoggingTransport(srv.Client().Transport, zap.New(obsCore))

	stdout := captureStdout(t, func() {
		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/mcp?token="+urlSecret,
			strings.NewReader("client_secret="+reqBodySecret))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+reqHdrSecret)

		resp, err := tr.RoundTrip(req)
		require.NoError(t, err)
		_, _ = io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
	})

	zapText := observedText(logs)
	for _, secret := range []string{urlSecret, reqHdrSecret, reqBodySecret, respHdrSecret, respBodySecre} {
		assert.NotContains(t, stdout, secret, "secret reached the stdout trace sink")
		assert.NotContains(t, zapText, secret, "secret reached the zap trace sink")
	}

	// Positive controls: masking that erases the diagnostic line is the other
	// failure mode this test has to catch.
	assert.Contains(t, stdout, "HTTP REQUEST: POST", "the method must survive")
	assert.Contains(t, stdout, "/mcp", "the request path must survive")
	assert.Contains(t, stdout, "Authorization", "the header NAME must survive")
	assert.Contains(t, zapText, "status=200", "the response status must survive")
}

// TestLoggingTransport_RedactsSSEFrames covers the SSE path, whose logging is
// asynchronous.
func TestLoggingTransport_RedactsSSEFrames(t *testing.T) {
	const frameSecret = "SUPERSECRETSSEFRAMEVALUE"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message\ndata: {\"access_token\":\"" + frameSecret + "\"}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	obsCore, logs := observer.New(zapcore.DebugLevel)
	tr := NewLoggingTransport(srv.Client().Transport, zap.New(obsCore))

	stdout := captureStdout(t, func() {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/sse", http.NoBody)
		require.NoError(t, err)
		resp, err := tr.RoundTrip(req)
		require.NoError(t, err)
		_, _ = io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())

		// The frame reader is a goroutine; wait for it rather than asserting
		// against an empty observer.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(observedText(logs), "SSE FRAME") {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})

	zapText := observedText(logs)
	require.Contains(t, zapText, "SSE FRAME",
		"the SSE frame log line under test was never emitted — the assertions below would be vacuous")
	assert.NotContains(t, zapText, frameSecret)
	assert.NotContains(t, stdout, frameSecret)
	assert.Contains(t, zapText, "event=message", "the frame's event type must survive")
}
