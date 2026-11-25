package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Shutdown ends event streams once no call is running, and never before that unless the grace
// expires.
func TestCancelRequestsOnShutdown(t *testing.T) {
	const (
		callTime = 600 * time.Millisecond
		margin   = 300 * time.Millisecond // scheduling slack, below callTime
	)
	tests := []struct {
		name      string
		call      bool
		grace     time.Duration
		streamMin time.Duration // the stream must live at least this long after Shutdown starts
		wantCall  string
	}{
		{name: "only a stream", grace: 5 * time.Second},
		{name: "a running call", call: true, grace: 5 * time.Second, streamMin: callTime - margin, wantCall: "done"},
		{name: "a call longer than the grace", call: true, grace: callTime / 4, streamMin: callTime / 4, wantCall: "cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{}, 2)
			streamEnded := make(chan time.Time, 1)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				started <- struct{}{}
				<-r.Context().Done()
				streamEnded <- time.Now()
			})
			mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				select {
				case <-r.Context().Done():
					_, _ = io.WriteString(w, "cancelled")
				case <-time.After(callTime):
					_, _ = io.WriteString(w, "done")
				}
			})
			srv := httptest.NewUnstartedServer(mux)
			cancelRequestsOnShutdown(srv.Config, tt.grace)
			srv.Start()
			t.Cleanup(srv.Close)

			// MCP clients send this Accept on POST as well.
			do := func(method string) string {
				req, err := http.NewRequest(method, srv.URL+"/mcp", http.NoBody)
				if err != nil {
					return err.Error()
				}
				req.Header.Set("Accept", "application/json, text/event-stream")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return err.Error()
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				return string(body)
			}
			go do(http.MethodGet)
			<-started
			call := make(chan string, 1)
			if tt.call {
				go func() { call <- do(http.MethodPost) }()
				<-started
			}

			begin := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			require.NoError(t, srv.Config.Shutdown(ctx))
			lived := (<-streamEnded).Sub(begin)
			require.GreaterOrEqual(t, lived, tt.streamMin)
			require.Less(t, lived, 2*time.Second)
			if tt.call {
				require.Equal(t, tt.wantCall, <-call)
			}
		})
	}
}
