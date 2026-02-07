package runtime

import "time"

const defaultEventBuffer = 256 // Increased from 16 to prevent event dropping when many servers.changed events flood the bus

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
// source indicates how the call was triggered: "mcp", "cli", or "api"
func (r *Runtime) EmitActivityToolCallStarted(serverName, toolName, sessionID, requestID, source string, args map[string]any) {
	payload := map[string]any{
		"server_name": serverName,
		"tool_name":   toolName,
		"session_id":  sessionID,
		"request_id":  requestID,
		"source":      source,
		"arguments":   args,
	}
	r.publishEvent(newEvent(EventTypeActivityToolCallStarted, payload))
}

// EmitActivityToolCallCompleted emits an event when a tool execution finishes.
// This is used to track activity for observability and debugging.
// source indicates how the call was triggered: "mcp", "cli", or "api"
// arguments is the input parameters passed to the tool call
// toolVariant is the MCP tool variant used (call_tool_read/write/destructive) - optional
// intent is the intent declaration metadata - optional
func (r *Runtime) EmitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, source, status, errorMsg string, durationMs int64, arguments map[string]interface{}, response string, responseTruncated bool, toolVariant string, intent map[string]interface{}) {
	payload := map[string]any{
		"server_name":        serverName,
		"tool_name":          toolName,
		"session_id":         sessionID,
		"request_id":         requestID,
		"source":             source,
		"status":             status,
		"error_message":      errorMsg,
		"duration_ms":        durationMs,
		"response":           response,
		"response_truncated": responseTruncated,
	}
	// Add arguments if provided
	if arguments != nil {
		payload["arguments"] = arguments
	}
	// Add intent metadata if provided (Spec 018)
	if toolVariant != "" {
		payload["tool_variant"] = toolVariant
	}
	if intent != nil {
		payload["intent"] = intent
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

// EmitActivitySystemStart emits an event when MCPProxy server starts (Spec 024).
func (r *Runtime) EmitActivitySystemStart(version, listenAddress string, startupDurationMs int64, configPath string) {
	payload := map[string]any{
		"version":             version,
		"listen_address":      listenAddress,
		"startup_duration_ms": startupDurationMs,
		"config_path":         configPath,
	}
	r.publishEvent(newEvent(EventTypeActivitySystemStart, payload))
}

// EmitActivitySystemStop emits an event when MCPProxy server stops (Spec 024).
func (r *Runtime) EmitActivitySystemStop(reason, signal string, uptimeSeconds int64, errorMsg string) {
	payload := map[string]any{
		"reason":         reason,
		"signal":         signal,
		"uptime_seconds": uptimeSeconds,
		"error_message":  errorMsg,
	}
	r.publishEvent(newEvent(EventTypeActivitySystemStop, payload))
}

// EmitActivityInternalToolCall emits an event when an internal tool is called (Spec 024).
// internalToolName is the name of the internal tool (retrieve_tools, call_tool_read, etc.)
// targetServer and targetTool are used for call_tool_* handlers
// arguments contains the input parameters, response contains the output
// intent is the intent declaration metadata
func (r *Runtime) EmitActivityInternalToolCall(internalToolName, targetServer, targetTool, toolVariant, sessionID, requestID, status, errorMsg string, durationMs int64, arguments map[string]interface{}, response interface{}, intent map[string]interface{}) {
	payload := map[string]any{
		"internal_tool_name": internalToolName,
		"session_id":         sessionID,
		"request_id":         requestID,
		"status":             status,
		"error_message":      errorMsg,
		"duration_ms":        durationMs,
	}
	if targetServer != "" {
		payload["target_server"] = targetServer
	}
	if targetTool != "" {
		payload["target_tool"] = targetTool
	}
	if toolVariant != "" {
		payload["tool_variant"] = toolVariant
	}
	if arguments != nil {
		payload["arguments"] = arguments
	}
	if response != nil {
		payload["response"] = response
	}
	if intent != nil {
		payload["intent"] = intent
	}
	r.publishEvent(newEvent(EventTypeActivityInternalToolCall, payload))
}

// EmitActivityConfigChange emits an event when configuration changes (Spec 024).
// action is one of: server_added, server_removed, server_updated, settings_changed
// source indicates how the change was triggered: "mcp", "cli", or "api"
func (r *Runtime) EmitActivityConfigChange(action, affectedEntity, source string, changedFields []string, previousValues, newValues map[string]interface{}) {
	payload := map[string]any{
		"action":          action,
		"affected_entity": affectedEntity,
		"source":          source,
	}
	if len(changedFields) > 0 {
		payload["changed_fields"] = changedFields
	}
	if previousValues != nil {
		payload["previous_values"] = previousValues
	}
	if newValues != nil {
		payload["new_values"] = newValues
	}
	r.publishEvent(newEvent(EventTypeActivityConfigChange, payload))
}

// EmitActivityHookEvaluation emits an event when a hook evaluation completes (Spec 027).
// This creates an activity record of type "hook_evaluation" in the activity log.
func (r *Runtime) EmitActivityHookEvaluation(toolName, sessionID, event, classification, flowType, riskLevel, policyDecision, policyReason, coverageMode string) {
	payload := map[string]any{
		"tool_name":       toolName,
		"session_id":      sessionID,
		"event":           event,
		"classification":  classification,
		"flow_type":       flowType,
		"risk_level":      riskLevel,
		"policy_decision": policyDecision,
		"policy_reason":   policyReason,
		"coverage_mode":   coverageMode,
	}
	r.publishEvent(newEvent(EventTypeActivityHookEvaluation, payload))
}

// EmitActivityFlowSummary emits an event when a flow session expires with aggregate statistics (Spec 027).
func (r *Runtime) EmitActivityFlowSummary(sessionID, coverageMode string, durationMinutes, totalOrigins, totalFlows int, flowTypeDistribution, riskLevelDistribution map[string]int, linkedMCPSessions, toolsUsed []string, hasSensitiveFlows bool) {
	payload := map[string]any{
		"session_id":              sessionID,
		"coverage_mode":           coverageMode,
		"duration_minutes":        durationMinutes,
		"total_origins":           totalOrigins,
		"total_flows":             totalFlows,
		"flow_type_distribution":  flowTypeDistribution,
		"risk_level_distribution": riskLevelDistribution,
		"linked_mcp_sessions":     linkedMCPSessions,
		"tools_used":              toolsUsed,
		"has_sensitive_flows":     hasSensitiveFlows,
	}
	r.publishEvent(newEvent(EventTypeActivityFlowSummary, payload))
}

// EmitFlowAlert emits a flow.alert SSE event when risk is high or critical (Spec 027).
// This enables real-time monitoring of dangerous data flows.
func (r *Runtime) EmitFlowAlert(activityID, sessionID, flowType, riskLevel, toolName string, hasSensitiveData bool) {
	payload := map[string]any{
		"activity_id":        activityID,
		"session_id":         sessionID,
		"flow_type":          flowType,
		"risk_level":         riskLevel,
		"tool_name":          toolName,
		"has_sensitive_data": hasSensitiveData,
	}
	r.publishEvent(newEvent(EventTypeFlowAlert, payload))
}

// EmitSensitiveDataDetected emits an event when sensitive data is detected in a tool call (Spec 026).
// activityID is the ID of the activity record where sensitive data was detected.
// detectionCount is the number of sensitive data detections found.
// maxSeverity is the highest severity level among detections (e.g., "high", "medium", "low").
// detectionTypes is a list of detection type names (e.g., "credit_card", "api_key").
func (r *Runtime) EmitSensitiveDataDetected(activityID string, detectionCount int, maxSeverity string, detectionTypes []string) {
	payload := map[string]any{
		"activity_id":     activityID,
		"detection_count": detectionCount,
		"max_severity":    maxSeverity,
		"detection_types": detectionTypes,
	}
	r.publishEvent(newEvent(EventTypeSensitiveDataDetected, payload))
}
