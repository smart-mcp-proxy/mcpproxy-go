package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCodeExecutionParentDetection(t *testing.T) {
	for _, location := range []string{"input", "output"} {
		t.Run(location, func(t *testing.T) {
			store, cleanup := setupTestStorage(t)
			defer cleanup()
			svc := NewActivityService(store, zap.NewNop())
			svc.SetMaxResponseSize(1024)
			svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))
			arguments := map[string]interface{}{"code": "input", "input": "benign"}
			response := strings.Repeat("x", 4096)
			if location == "input" {
				arguments["input"] = "AKIA1234567890ABCDEF"
			} else {
				response += " secret AKIA1234567890ABCDEF"
			}
			svc.handleEvent(Event{Type: EventTypeActivityInternalToolCall, Timestamp: time.Now(), Payload: map[string]any{
				"internal_tool_name": "code_execution", "arguments": arguments, "response": response, "status": "success",
			}})
			svc.workersWG.Wait()
			records := allActivities(t, store)
			require.Len(t, records, 1)
			assert.NotContains(t, records[0].Response, "AKIA1234567890ABCDEF")
			require.Contains(t, records[0].Metadata, "sensitive_data_detection")
		})
	}
}
