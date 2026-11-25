package httpapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"net/http/httptest"
)

// detectionEvent builds a sensitive_data.detected payload as
// Runtime.EmitSensitiveDataDetected does.
func detectionEvent(activityID, server string, activityType storage.ActivityType) internalRuntime.Event {
	payload := map[string]any{
		"server_name":     server,
		"activity_id":     activityID,
		"detection_count": 1,
		"max_severity":    "high",
		"detection_types": []string{"aws_access_key"},
	}
	if activityType != "" {
		payload["activity_type"] = string(activityType)
	}
	return internalRuntime.Event{Type: internalRuntime.EventTypeSensitiveDataDetected, Payload: payload, Timestamp: time.Now()}
}

// TestSSE_SensitiveDataDetectedHidesAdminOnlyRows: a scoped caller that can
// see the server still must not learn, through the derived detection event,
// the id and detection categories of an activity row it cannot read
// (storage.IsAdminOnlyActivityType). An admin keeps every such event.
func TestSSE_SensitiveDataDetectedHidesAdminOnlyRows(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond,
		"precondition: both SSE connections must be subscribed to the event bus")

	events := []internalRuntime.Event{
		detectionEvent("skill-row", "alpha", storage.ActivityTypeSkillAccess),
		detectionEvent("client-request-row", "alpha", storage.ActivityTypeClientRequest),
		detectionEvent("skill-quarantine-row", "alpha", storage.ActivityTypeSkillQuarantineChange),
		detectionEvent("untyped-row", "alpha", ""),
		detectionEvent("tool-call-row", "alpha", storage.ActivityTypeToolCall),
	}
	sentinel := internalRuntime.Event{
		Type:      internalRuntime.EventTypeActivityToolCallCompleted,
		Payload:   map[string]any{"server_name": "alpha", "tool_name": "sentinel", "status": "ok"},
		Timestamp: time.Now(),
	}
	isSentinel := func(e sseEvent) bool {
		inner, ok := e.Data["payload"].(map[string]interface{})
		return ok && e.Name == string(internalRuntime.EventTypeActivityToolCallCompleted) && inner["tool_name"] == "sentinel"
	}

	var wg sync.WaitGroup
	var adminFrames, agentFrames []sseEvent
	deadline := time.Now().Add(15 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); adminFrames = readSSEFramesUntil(t, adminBody, deadline, isSentinel) }()
	go func() { defer wg.Done(); agentFrames = readSSEFramesUntil(t, agentBody, deadline, isSentinel) }()
	for _, e := range events {
		ctrl.publishToAll(e)
	}
	ctrl.publishToAll(sentinel)
	wg.Wait()

	detections := func(frames []sseEvent) []string {
		var ids []string
		for _, f := range frames {
			if f.Name != string(internalRuntime.EventTypeSensitiveDataDetected) {
				continue
			}
			inner, _ := f.Data["payload"].(map[string]interface{})
			id, _ := inner["activity_id"].(string)
			ids = append(ids, id)
		}
		return ids
	}
	assert.Equal(t, []string{"tool-call-row"}, detections(agentFrames),
		"a scoped caller must receive detections only for rows it may read")
	assert.Equal(t, []string{"skill-row", "client-request-row", "skill-quarantine-row", "untyped-row", "tool-call-row"},
		detections(adminFrames), "an admin keeps every detection event")
}

func TestEventVisibleToCaller_DetectionActivityType(t *testing.T) {
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	for _, tc := range []struct {
		activityType storage.ActivityType
		scopedSees   bool
	}{
		{storage.ActivityTypeToolCall, true},
		{storage.ActivityTypeInternalToolCall, true},
		{storage.ActivityTypeSkillAccess, false},
		{storage.ActivityTypeClientRequest, false},
		{storage.ActivityTypeSkillQuarantineChange, false},
		{"", false},
	} {
		evt := detectionEvent("row", "alpha", tc.activityType)
		assert.Equal(t, tc.scopedSees, eventVisibleToCaller(scoped, evt), "scoped, type %q", tc.activityType)
		assert.True(t, eventVisibleToCaller(context.Background(), evt), "admin, type %q", tc.activityType)
	}
}
