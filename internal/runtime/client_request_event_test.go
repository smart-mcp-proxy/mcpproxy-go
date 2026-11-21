package runtime

import (
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientrelay"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestClientRequestActivity(t *testing.T) {
	tests := []struct {
		name        string
		event       clientrelay.Event
		wantStatus  string
		wantOutcome string
		wantErr     string
	}{
		{
			name: "forwarded elicitation",
			event: clientrelay.Event{
				Server: "ssh", Method: mcp.MethodElicitationCreate, SessionID: "sess-1",
				Params:   mcp.ElicitationParams{Message: "run rm?", RequestedSchema: map[string]any{"type": "object"}},
				Result:   &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}},
				Duration: 1500 * time.Millisecond,
			},
			wantStatus: storage.ActivityStatusSuccess, wantOutcome: ClientRequestForwarded,
		},
		{
			name: "nobody could be asked",
			event: clientrelay.Event{
				Server: "everything", Method: mcp.MethodSamplingCreateMessage,
				Err: fmt.Errorf("%w: client does not support sampling", clientrelay.ErrNoClient),
			},
			wantStatus: storage.ActivityStatusError, wantOutcome: ClientRequestUnanswered,
			wantErr: "no MCP client in this call can answer: client does not support sampling",
		},
		{
			name: "the client failed",
			event: clientrelay.Event{
				Server: "ssh", Method: mcp.MethodElicitationCreate, SessionID: "sess-1",
				Err: fmt.Errorf("context deadline exceeded"),
			},
			wantStatus: storage.ActivityStatusError, wantOutcome: ClientRequestFailed,
			wantErr: "context deadline exceeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, cleanup := setupTestStorage(t)
			defer cleanup()
			svc := NewActivityService(store, zap.NewNop())
			svc.handleEvent(Event{Type: EventTypeActivityClientRequest, Timestamp: time.Now().UTC(), Payload: clientRequestPayload(tt.event)})

			records, _, err := store.ListActivities(storage.DefaultActivityFilter())
			require.NoError(t, err)
			require.Len(t, records, 1)
			rec := records[0]
			assert.Equal(t, storage.ActivityTypeClientRequest, rec.Type)
			assert.Equal(t, tt.event.Server, rec.ServerName)
			assert.Equal(t, string(tt.event.Method), rec.ToolName)
			assert.Equal(t, tt.event.SessionID, rec.SessionID)
			assert.Equal(t, tt.wantStatus, rec.Status)
			assert.Equal(t, tt.wantErr, rec.ErrorMessage)
			assert.Equal(t, tt.wantOutcome, rec.Metadata["outcome"])
			assert.Equal(t, tt.event.Duration.Milliseconds(), rec.DurationMs)
			if tt.event.Params != nil {
				assert.Equal(t, "run rm?", rec.Arguments["message"], "what the upstream asked")
				assert.Contains(t, rec.Response, `"action":"accept"`, "what came back")
			}
		})
	}
}
