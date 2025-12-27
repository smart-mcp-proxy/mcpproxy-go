package runtime

import "time"

const defaultEventBuffer = 16

// SubscribeEvents registers a new subscriber and returns a channel that will receive runtime events.
// Callers must not close the returned channel; use UnsubscribeEvents when finished.
func (r *Runtime) SubscribeEvents() chan Event {
	ch := make(chan Event, defaultEventBuffer)
	r.eventMu.Lock()
	r.eventSubs[ch] = struct{}{}
	r.eventMu.Unlock()
	return ch
}

// UnsubscribeEvents removes the subscriber and closes the channel.
func (r *Runtime) UnsubscribeEvents(ch chan Event) {
	r.eventMu.Lock()
	if _, ok := r.eventSubs[ch]; ok {
		delete(r.eventSubs, ch)
		close(ch)
	}
	r.eventMu.Unlock()
}

func (r *Runtime) publishEvent(evt Event) {
	r.eventMu.RLock()
	for ch := range r.eventSubs {
		select {
		case ch <- evt:
		default:
		}
	}
	r.eventMu.RUnlock()
}

func (r *Runtime) emitServersChanged(reason string, extra map[string]any) {
	payload := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		payload[k] = v
	}
	payload["reason"] = reason
	r.publishEvent(newEvent(EventTypeServersChanged, payload))
}

func (r *Runtime) emitConfigReloaded(path string) {
	payload := map[string]any{"path": path}
	r.publishEvent(newEvent(EventTypeConfigReloaded, payload))
}

func (r *Runtime) emitConfigSaved(path string) {
	payload := map[string]any{"path": path}
	r.publishEvent(newEvent(EventTypeConfigSaved, payload))
}

func (r *Runtime) emitSecretsChanged(operation string, secretName string, extra map[string]any) {
	payload := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		payload[k] = v
	}
	payload["operation"] = operation
	payload["secret_name"] = secretName
	r.publishEvent(newEvent(EventTypeSecretsChanged, payload))
}

// EmitOAuthTokenRefreshed emits an event when proactive token refresh succeeds.
// This is used by the RefreshManager to notify subscribers of successful token refresh.
func (r *Runtime) EmitOAuthTokenRefreshed(serverName string, expiresAt time.Time) {
	payload := map[string]any{
		"server_name": serverName,
		"expires_at":  expiresAt.Format(time.RFC3339),
	}
	r.publishEvent(newEvent(EventTypeOAuthTokenRefreshed, payload))
}

// EmitOAuthRefreshFailed emits an event when proactive token refresh fails after retries.
// This is used by the RefreshManager to notify subscribers that re-authentication is needed.
func (r *Runtime) EmitOAuthRefreshFailed(serverName string, errorMsg string) {
	payload := map[string]any{
		"server_name": serverName,
		"error":       errorMsg,
	}
	r.publishEvent(newEvent(EventTypeOAuthRefreshFailed, payload))
}

// EmitActivityToolCallStarted emits an event when a tool execution begins.
// This is used to track activity for observability and debugging.
func (r *Runtime) EmitActivityToolCallStarted(serverName, toolName, sessionID, requestID string, args map[string]any) {
	payload := map[string]any{
		"server_name": serverName,
		"tool_name":   toolName,
		"session_id":  sessionID,
		"request_id":  requestID,
		"arguments":   args,
	}
	r.publishEvent(newEvent(EventTypeActivityToolCallStarted, payload))
}

// EmitActivityToolCallCompleted emits an event when a tool execution finishes.
// This is used to track activity for observability and debugging.
func (r *Runtime) EmitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, status, errorMsg string, durationMs int64, response string, responseTruncated bool) {
	payload := map[string]any{
		"server_name":        serverName,
		"tool_name":          toolName,
		"session_id":         sessionID,
		"request_id":         requestID,
		"status":             status,
		"error_message":      errorMsg,
		"duration_ms":        durationMs,
		"response":           response,
		"response_truncated": responseTruncated,
	}
	r.publishEvent(newEvent(EventTypeActivityToolCallCompleted, payload))
}

// EmitActivityPolicyDecision emits an event when a policy blocks a tool call.
func (r *Runtime) EmitActivityPolicyDecision(serverName, toolName, sessionID, decision, reason string) {
	payload := map[string]any{
		"server_name": serverName,
		"tool_name":   toolName,
		"session_id":  sessionID,
		"decision":    decision,
		"reason":      reason,
	}
	r.publishEvent(newEvent(EventTypeActivityPolicyDecision, payload))
}

// EmitActivityQuarantineChange emits an event when a server's quarantine state changes.
func (r *Runtime) EmitActivityQuarantineChange(serverName string, quarantined bool, reason string) {
	payload := map[string]any{
		"server_name": serverName,
		"quarantined": quarantined,
		"reason":      reason,
	}
	r.publishEvent(newEvent(EventTypeActivityQuarantineChange, payload))
}
