package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientrelay"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type recordingDetectionEmitter struct {
	mu    sync.Mutex
	ids   []string
	types []storage.ActivityType
}

func (e *recordingDetectionEmitter) EmitSensitiveDataDetected(id string, activityType storage.ActivityType, _ int, _ string, _ []string, _ string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ids = append(e.ids, id)
	e.types = append(e.types, activityType)
}

// TestRunAsyncDetection_EventCarriesActivityType: the detection event names
// the scanned row's type, so the SSE layer can withhold detections on rows a
// scoped caller may not read (storage.IsAdminOnlyActivityType).
func TestRunAsyncDetection_EventCarriesActivityType(t *testing.T) {
	const awsKey = "AKIA1234567890ABCDEF"
	for _, tc := range []struct {
		name string
		evt  Event
		want storage.ActivityType
	}{
		{"tool call", Event{Type: EventTypeActivityToolCallCompleted, Timestamp: time.Now().UTC(), Payload: map[string]any{
			"server_name": "github", "tool_name": "t", "status": "success", "response": "key " + awsKey,
		}}, storage.ActivityTypeToolCall},
		{"skill access", Event{Type: EventTypeActivitySkillAccess, Timestamp: time.Now().UTC(), Payload: map[string]any{
			"server_name": "github", "action": "load", "outcome": SkillAccessSuccess, "tool": "load_skill",
			"skill_id": "github:s", "detection_text": "key " + awsKey,
		}}, storage.ActivityTypeSkillAccess},
		{"client request", Event{Type: EventTypeActivityClientRequest, Timestamp: time.Now().UTC(), Payload: clientRequestPayload(clientrelay.Event{
			Server: "github", Method: mcp.MethodElicitationCreate, Params: map[string]any{"message": "key " + awsKey},
		})}, storage.ActivityTypeClientRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := setupTestStorage(t)
			defer cleanup()
			svc := NewActivityService(store, zap.NewNop())
			svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))
			emitter := &recordingDetectionEmitter{}
			svc.SetEventEmitter(emitter)

			svc.handleEvent(tc.evt)
			svc.workersWG.Wait()

			emitter.mu.Lock()
			defer emitter.mu.Unlock()
			require.Len(t, emitter.types, 1)
			assert.Equal(t, tc.want, emitter.types[0])
			// The emitted type is the type of the row the emitted id names.
			saved, err := store.GetActivity(emitter.ids[0])
			require.NoError(t, err)
			assert.Equal(t, saved.Type, emitter.types[0])
		})
	}
}
