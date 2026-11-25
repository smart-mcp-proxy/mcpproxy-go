package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
				Err: fmt.Errorf("elicitation: %w", context.DeadlineExceeded),
			},
			wantStatus: storage.ActivityStatusError, wantOutcome: ClientRequestFailed,
			wantErr: "the client did not answer in time",
		},
		{
			name: "the client's own error text is not stored",
			event: clientrelay.Event{
				Server: "ssh", Method: mcp.MethodElicitationCreate, SessionID: "sess-1",
				Err: fmt.Errorf("the user typed hunter2"),
			},
			wantStatus: storage.ActivityStatusError, wantOutcome: ClientRequestFailed,
			wantErr: "the client returned an error",
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
				assert.Equal(t, "accept", rec.Metadata["action"])
			}
			assert.Empty(t, rec.Response)
		})
	}
}

// Neither the stored row nor the published event carries what the user, the
// model or the client answered, and what the upstream asked is capped.
func TestClientRequestActivity_KeepsNoAnswer(t *testing.T) {
	const secret = "hunter2-answer"
	events := map[string]clientrelay.Event{
		"elicitation": {Server: "s", Method: mcp.MethodElicitationCreate, Result: &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
			Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"password": secret},
		}}},
		"sampling": {Server: "s", Method: mcp.MethodSamplingCreateMessage, Result: &mcp.CreateMessageResult{
			Model: "m", SamplingMessage: mcp.SamplingMessage{Role: mcp.RoleAssistant, Content: mcp.NewTextContent(secret)},
		}},
		"roots": {Server: "s", Method: mcp.MethodListRoots, Result: &mcp.ListRootsResult{Roots: []mcp.Root{{URI: "file:///" + secret}}}},
	}
	want := map[string]map[string]any{
		"elicitation": {"action": "accept", "field_count": float64(1), "field_bytes": map[string]any{"password": float64(len(secret) + 2)}},
		"sampling":    {},
		"roots":       {"root_count": float64(1)},
	}
	for name, e := range events {
		t.Run(name, func(t *testing.T) {
			e.Params = map[string]any{"message": strings.Repeat("é", 100_000)}
			payload := clientRequestPayload(e)
			b, err := json.Marshal(payload)
			require.NoError(t, err)
			assert.NotContains(t, string(b), secret)
			assert.Less(t, len(b), 2*maxClientRequestArguments, "arguments are capped")

			store, cleanup := setupTestStorage(t)
			defer cleanup()
			svc := NewActivityService(store, zap.NewNop())
			svc.handleEvent(Event{Type: EventTypeActivityClientRequest, Timestamp: time.Now().UTC(), Payload: payload})
			records, _, err := store.ListActivities(storage.DefaultActivityFilter())
			require.NoError(t, err)
			require.Len(t, records, 1)
			b, err = json.Marshal(records[0])
			require.NoError(t, err)
			assert.NotContains(t, string(b), secret)
			for k, v := range want[name] {
				assert.Equal(t, v, records[0].Metadata[k], k)
			}
			if name == "sampling" {
				assert.Greater(t, records[0].Metadata["response_bytes"], float64(len(secret)))
			}
		})
	}
}
