package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// parseActivityFilters extracts activity filter parameters from the request query string.
func parseActivityFilters(r *http.Request) storage.ActivityFilter {
	filter := storage.DefaultActivityFilter()
	q := r.URL.Query()

	// Type filter (Spec 024: supports comma-separated multiple types)
	if typeStr := q.Get("type"); typeStr != "" {
		filter.Types = strings.Split(typeStr, ",")
	}

	// Server filter
	if server := q.Get("server"); server != "" {
		filter.Server = server
	}

	// Tool filter
	if tool := q.Get("tool"); tool != "" {
		filter.Tool = tool
	}

	// Session filter
	// Spec 082: filter by a unit of user work (one client, one project, across
	// reconnects). This is what the UI's Session filter now sends.
	if ws := q.Get("work_session_id"); ws != "" {
		filter.WorkSessionID = ws
	}
	if sessionID := q.Get("session_id"); sessionID != "" {
		filter.SessionID = sessionID
	}

	// Status filter
	if status := q.Get("status"); status != "" {
		filter.Status = status
	}

	// Time range filters
	if startTimeStr := q.Get("start_time"); startTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			filter.StartTime = t
		}
	}

	if endTimeStr := q.Get("end_time"); endTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			filter.EndTime = t
		}
	}

	// Pagination
	if limitStr := q.Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = limit
		}
	}

	if offsetStr := q.Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil {
			filter.Offset = offset
		}
	}

	// Intent type filter (Spec 018)
	if intentType := q.Get("intent_type"); intentType != "" {
		filter.IntentType = intentType
	}

	// Request ID filter (Spec 021)
	if requestID := q.Get("request_id"); requestID != "" {
		filter.RequestID = requestID
	}

	// Include call_tool_* internal tool calls (default: exclude successful ones)
	// Set include_call_tool=true to show all internal tool calls including successful call_tool_*
	if q.Get("include_call_tool") == "true" {
		filter.ExcludeCallToolSuccess = false
	}

	// Sensitive data detection filters (Spec 026)
	if sensitiveDataStr := q.Get("sensitive_data"); sensitiveDataStr != "" {
		sensitiveData := sensitiveDataStr == "true"
		filter.SensitiveData = &sensitiveData
	}

	if detectionType := q.Get("detection_type"); detectionType != "" {
		filter.DetectionType = detectionType
	}

	if severity := q.Get("severity"); severity != "" {
		filter.Severity = severity
	}

	// Agent token identity filters (Spec 028)
	if agent := q.Get("agent"); agent != "" {
		filter.AgentName = agent
	}
	if authType := q.Get("auth_type"); authType != "" {
		filter.AuthType = authType
	}

	filter.Validate()
	return filter
}

// handleListActivity handles GET /api/v1/activity
// @Summary List activity records
// @Description Returns paginated list of activity records with optional filtering
// @Tags Activity
// @Accept json
// @Produce json
// @Param type query string false "Filter by activity type(s), comma-separated for multiple (Spec 024)" Enums(tool_call, policy_decision, quarantine_change, server_change, system_start, system_stop, internal_tool_call, config_change)
// @Param server query string false "Filter by server name"
// @Param tool query string false "Filter by tool name"
// @Param session_id query string false "Filter by MCP transport session ID"
// @Param work_session_id query string false "Filter by work session (one client, one project, across reconnects)"
// @Param status query string false "Filter by status" Enums(success, error, blocked)
// @Param intent_type query string false "Filter by intent operation type (Spec 018)" Enums(read, write, destructive)
// @Param request_id query string false "Filter by HTTP request ID for log correlation (Spec 021)"
// @Param include_call_tool query bool false "Include successful call_tool_* internal tool calls (default: false, excluded to avoid duplicates)"
// @Param sensitive_data query bool false "Filter by sensitive data detection (true=has detections, false=no detections)"
// @Param detection_type query string false "Filter by specific detection type (e.g., 'aws_access_key', 'credit_card')"
// @Param severity query string false "Filter by severity level" Enums(critical, high, medium, low)
// @Param agent query string false "Filter by agent token name (Spec 028)"
// @Param auth_type query string false "Filter by auth type (Spec 028)" Enums(admin, agent)
// @Param start_time query string false "Filter activities after this time (RFC3339)"
// @Param end_time query string false "Filter activities before this time (RFC3339)"
// @Param limit query int false "Maximum records to return (1-100, default 50)"
// @Param offset query int false "Pagination offset (default 0)"
// @Success 200 {object} contracts.APIResponse{data=contracts.ActivityListResponse}
// @Failure 400 {object} contracts.APIResponse
// @Failure 401 {object} contracts.APIResponse
// @Failure 500 {object} contracts.APIResponse
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/activity [get]
func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	filter := parseActivityFilters(r)

	activities, total, err := s.controller.ListActivities(filter)
	if err != nil {
		s.logger.Errorw("Failed to list activities", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list activities")
		return
	}

	// Convert storage records to contract records
	contractActivities := make([]contracts.ActivityRecord, len(activities))
	for i, a := range activities {
		contractActivities[i] = storageToContractActivity(a)
	}

	response := contracts.ActivityListResponse{
		Activities: contractActivities,
		Total:      total,
		Limit:      filter.Limit,
		Offset:     filter.Offset,
	}

	s.writeSuccess(w, response)
}

// handleGetActivityDetail handles GET /api/v1/activity/{id}
// @Summary Get activity record details
// @Description Returns full details for a single activity record
// @Tags Activity
// @Accept json
// @Produce json
// @Param id path string true "Activity record ID (ULID)"
// @Success 200 {object} contracts.APIResponse{data=contracts.ActivityDetailResponse}
// @Failure 404 {object} contracts.APIResponse
// @Failure 401 {object} contracts.APIResponse
// @Failure 500 {object} contracts.APIResponse
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/activity/{id} [get]
func (s *Server) handleGetActivityDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "Activity ID is required")
		return
	}

	activity, err := s.controller.GetActivity(id)
	if err != nil {
		s.logger.Errorw("Failed to get activity", "id", id, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get activity")
		return
	}

	if activity == nil {
		s.writeError(w, r, http.StatusNotFound, "Activity not found")
		return
	}

	response := contracts.ActivityDetailResponse{
		Activity: storageToContractActivity(activity),
	}

	s.writeSuccess(w, response)
}

// storageToContractActivity converts a storage ActivityRecord to a contracts ActivityRecord.
func storageToContractActivity(a *storage.ActivityRecord) contracts.ActivityRecord {
	hasSensitiveData, detectionTypes, maxSeverity := extractSensitiveDataInfo(a)

	return contracts.ActivityRecord{
		ID:                a.ID,
		Type:              contracts.ActivityType(a.Type),
		Source:            contracts.ActivitySource(a.Source),
		ServerName:        a.ServerName,
		ToolName:          a.ToolName,
		Arguments:         a.Arguments,
		Response:          a.Response,
		ResponseTruncated: a.ResponseTruncated,
		Status:            a.Status,
		ErrorMessage:      a.ErrorMessage,
		DurationMs:        a.DurationMs,
		Timestamp:         a.Timestamp,
		SessionID:         a.SessionID,
		WorkSessionID:     a.WorkSessionID,
		RequestID:         a.RequestID,
		Metadata:          a.Metadata,
		// Sensitive data detection fields (Spec 026)
		HasSensitiveData: hasSensitiveData,
		DetectionTypes:   detectionTypes,
		MaxSeverity:      maxSeverity,
	}
}

// extractSensitiveDataInfo extracts sensitive data detection info from activity metadata.
// Returns (hasSensitiveData bool, detectionTypes []string, maxSeverity string).
func extractSensitiveDataInfo(a *storage.ActivityRecord) (bool, []string, string) {
	if a.Metadata == nil {
		return false, nil, ""
	}

	detection, ok := a.Metadata["sensitive_data_detection"].(map[string]interface{})
	if !ok {
		return false, nil, ""
	}

	detected, _ := detection["detected"].(bool)
	if !detected {
		return false, nil, ""
	}

	// Extract unique detection types
	var detectionTypes []string
	typeSet := make(map[string]struct{})

	if detections, ok := detection["detections"].([]interface{}); ok {
		for _, d := range detections {
			if det, ok := d.(map[string]interface{}); ok {
				if dtype, ok := det["type"].(string); ok {
					if _, exists := typeSet[dtype]; !exists {
						typeSet[dtype] = struct{}{}
						detectionTypes = append(detectionTypes, dtype)
					}
				}
			}
		}
	}

	// Calculate max severity
	maxSeverity := calculateMaxSeverity(detection)

	return detected, detectionTypes, maxSeverity
}

// calculateMaxSeverity determines the highest severity from detection results.
// Severity order: critical > high > medium > low
func calculateMaxSeverity(detection map[string]interface{}) string {
	severityOrder := map[string]int{
		"critical": 4,
		"high":     3,
		"medium":   2,
		"low":      1,
	}

	maxLevel := 0
	maxSeverity := ""

	if detections, ok := detection["detections"].([]interface{}); ok {
		for _, d := range detections {
			if det, ok := d.(map[string]interface{}); ok {
				if sev, ok := det["severity"].(string); ok {
					if level, exists := severityOrder[sev]; exists && level > maxLevel {
						maxLevel = level
						maxSeverity = sev
					}
				}
			}
		}
	}

	return maxSeverity
}

// storageToContractActivityForExport converts a storage ActivityRecord to a contracts ActivityRecord
// with optional inclusion of request/response bodies for export.
func storageToContractActivityForExport(a *storage.ActivityRecord, includeBodies bool) contracts.ActivityRecord {
	hasSensitiveData, detectionTypes, maxSeverity := extractSensitiveDataInfo(a)

	record := contracts.ActivityRecord{
		ID:                a.ID,
		Type:              contracts.ActivityType(a.Type),
		Source:            contracts.ActivitySource(a.Source),
		ServerName:        a.ServerName,
		ToolName:          a.ToolName,
		ResponseTruncated: a.ResponseTruncated,
		Status:            a.Status,
		ErrorMessage:      a.ErrorMessage,
		DurationMs:        a.DurationMs,
		Timestamp:         a.Timestamp,
		SessionID:         a.SessionID,
		WorkSessionID:     a.WorkSessionID,
		RequestID:         a.RequestID,
		Metadata:          a.Metadata,
		// Sensitive data detection fields (Spec 026)
		HasSensitiveData: hasSensitiveData,
		DetectionTypes:   detectionTypes,
		MaxSeverity:      maxSeverity,
	}

	// Only include request/response bodies when explicitly requested
	if includeBodies {
		record.Arguments = a.Arguments
		record.Response = a.Response
	}

	return record
}

// handleExportActivity handles GET /api/v1/activity/export
// @Summary Export activity records
// @Description Exports activity records in JSON Lines or CSV format for compliance
// @Tags Activity
// @Accept json
// @Produce application/x-ndjson,text/csv
// @Param format query string false "Export format: json (default) or csv"
// @Param type query string false "Filter by activity type"
// @Param server query string false "Filter by server name"
// @Param tool query string false "Filter by tool name"
// @Param session_id query string false "Filter by MCP transport session ID"
// @Param work_session_id query string false "Filter by work session (one client, one project, across reconnects)"
// @Param status query string false "Filter by status"
// @Param start_time query string false "Filter activities after this time (RFC3339)"
// @Param end_time query string false "Filter activities before this time (RFC3339)"
// @Param limit query int false "Maximum records to export (1-50000, default 10000)"
// @Param offset query int false "Pagination offset (default 0)"
// @Success 200 {string} string "Streamed activity records"
// @Failure 401 {object} contracts.APIResponse
// @Failure 500 {object} contracts.APIResponse
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/activity/export [get]
func (s *Server) handleExportActivity(w http.ResponseWriter, r *http.Request) {
	filter := parseActivityFilters(r)

	// Re-parse limit/offset from query for export — parseActivityFilters caps at 100 via Validate(),
	// but export supports up to 50000. Re-read raw values and apply export-specific validation.
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = limit
		}
	} else {
		filter.Limit = 0 // Not specified — let ValidateForExport set the default (10000)
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil {
			filter.Offset = offset
		}
	}
	filter.ValidateForExport()

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	// Check if request/response bodies should be included
	includeBodies := r.URL.Query().Get("include_bodies") == "true"

	// Validate format
	if format != "json" && format != "csv" {
		s.writeError(w, r, http.StatusBadRequest, "Invalid format. Use 'json' or 'csv'")
		return
	}

	// Set appropriate content type and headers
	filename := "activity-export"
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		filename += ".csv"
	} else {
		w.Header().Set("Content-Type", "application/x-ndjson")
		filename += ".jsonl"
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Cache-Control", "no-cache")

	// Stream activities
	activityCh := s.controller.StreamActivities(filter)

	// Write CSV header if format is CSV
	if format == "csv" {
		csvHeader := "id,type,source,server_name,tool_name,status,error_message,duration_ms,timestamp,session_id,request_id,response_truncated\n"
		if _, err := w.Write([]byte(csvHeader)); err != nil {
			s.logger.Errorw("Failed to write CSV header", "error", err)
			return
		}
	}

	// Flush headers
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	count := 0
	for activity := range activityCh {
		var line string
		if format == "csv" {
			line = activityToCSVRow(activity)
		} else {
			// JSON Lines format - one JSON object per line
			contractActivity := storageToContractActivityForExport(activity, includeBodies)
			jsonBytes, err := json.Marshal(contractActivity)
			if err != nil {
				s.logger.Errorw("Failed to marshal activity for export", "error", err, "id", activity.ID)
				continue
			}
			line = string(jsonBytes) + "\n"
		}

		if _, err := w.Write([]byte(line)); err != nil {
			s.logger.Errorw("Failed to write activity export line", "error", err)
			return
		}

		count++
		// Flush periodically for streaming
		if count%100 == 0 {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}

	// Final flush
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	s.logger.Infow("Activity export completed", "format", format, "count", count, "limit", filter.Limit, "offset", filter.Offset)
}

// activityToCSVRow converts an ActivityRecord to a CSV row string.
func activityToCSVRow(a *storage.ActivityRecord) string {
	// Escape CSV fields that might contain commas, quotes, or newlines
	escapeCSV := func(s string) string {
		if strings.ContainsAny(s, ",\"\n\r") {
			return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
		}
		return s
	}

	return strings.Join([]string{
		escapeCSV(a.ID),
		escapeCSV(string(a.Type)),
		escapeCSV(string(a.Source)),
		escapeCSV(a.ServerName),
		escapeCSV(a.ToolName),
		escapeCSV(a.Status),
		escapeCSV(a.ErrorMessage),
		strconv.FormatInt(a.DurationMs, 10),
		a.Timestamp.Format(time.RFC3339),
		escapeCSV(a.SessionID),
		escapeCSV(a.RequestID),
		strconv.FormatBool(a.ResponseTruncated),
	}, ",") + "\n"
}

// parsePeriodDuration converts a period string to a time.Duration.
func parsePeriodDuration(period string) (time.Duration, error) {
	switch period {
	case "1h":
		return time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid period: %s", period)
	}
}

// handleActivitySummary handles GET /api/v1/activity/summary
// @Summary Get activity summary statistics
// @Description Returns aggregated activity statistics for a time period
// @Tags Activity
// @Accept json
// @Produce json
// @Param period query string false "Time period: 1h, 24h (default), 7d, 30d"
// @Param group_by query string false "Group by: server, tool (optional)"
// @Success 200 {object} contracts.APIResponse{data=contracts.ActivitySummaryResponse}
// @Failure 400 {object} contracts.APIResponse
// @Failure 401 {object} contracts.APIResponse
// @Failure 500 {object} contracts.APIResponse
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/activity/summary [get]
func (s *Server) handleActivitySummary(w http.ResponseWriter, r *http.Request) {
	// Parse period parameter
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}

	duration, err := parsePeriodDuration(period)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Calculate time range
	endTime := time.Now().UTC()
	startTime := endTime.Add(-duration)

	// Build filter for the time range
	filter := storage.DefaultActivityFilter()
	filter.StartTime = startTime
	filter.EndTime = endTime
	filter.Limit = 0 // Get all records

	// Get all activities in the time range
	activities, _, err := s.controller.ListActivities(filter)
	if err != nil {
		s.logger.Errorw("Failed to list activities for summary", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get activity summary")
		return
	}

	// Calculate summary statistics
	var totalCount, successCount, errorCount, blockedCount int
	serverCounts := make(map[string]int)
	toolCounts := make(map[string]int)

	for _, a := range activities {
		totalCount++
		switch a.Status {
		case "success":
			successCount++
		case "error":
			errorCount++
		case "blocked":
			blockedCount++
		}

		// Count by server
		if a.ServerName != "" {
			serverCounts[a.ServerName]++
		}

		// Count by tool (server:tool)
		if a.ServerName != "" && a.ToolName != "" {
			key := a.ServerName + ":" + a.ToolName
			toolCounts[key]++
		}
	}

	// Build top servers list (top 5)
	topServers := buildTopServers(serverCounts, 5)

	// Build top tools list (top 5)
	topTools := buildTopTools(toolCounts, 5)

	response := contracts.ActivitySummaryResponse{
		Period:       period,
		TotalCount:   totalCount,
		SuccessCount: successCount,
		ErrorCount:   errorCount,
		BlockedCount: blockedCount,
		TopServers:   topServers,
		TopTools:     topTools,
		StartTime:    startTime.Format(time.RFC3339),
		EndTime:      endTime.Format(time.RFC3339),
	}

	s.writeSuccess(w, response)
}

// buildTopServers returns top N servers by activity count.
func buildTopServers(counts map[string]int, limit int) []contracts.ActivityTopServer {
	// Convert map to slice for sorting
	type serverCount struct {
		name  string
		count int
	}
	var servers []serverCount
	for name, count := range counts {
		servers = append(servers, serverCount{name, count})
	}

	// Sort by count descending
	for i := 0; i < len(servers)-1; i++ {
		for j := i + 1; j < len(servers); j++ {
			if servers[j].count > servers[i].count {
				servers[i], servers[j] = servers[j], servers[i]
			}
		}
	}

	// Take top N
	if len(servers) > limit {
		servers = servers[:limit]
	}

	result := make([]contracts.ActivityTopServer, len(servers))
	for i, s := range servers {
		result[i] = contracts.ActivityTopServer{
			Name:  s.name,
			Count: s.count,
		}
	}
	return result
}

// buildTopTools returns top N tools by activity count.
func buildTopTools(counts map[string]int, limit int) []contracts.ActivityTopTool {
	// Convert map to slice for sorting
	type toolCount struct {
		key   string
		count int
	}
	var tools []toolCount
	for key, count := range counts {
		tools = append(tools, toolCount{key, count})
	}

	// Sort by count descending
	for i := 0; i < len(tools)-1; i++ {
		for j := i + 1; j < len(tools); j++ {
			if tools[j].count > tools[i].count {
				tools[i], tools[j] = tools[j], tools[i]
			}
		}
	}

	// Take top N
	if len(tools) > limit {
		tools = tools[:limit]
	}

	result := make([]contracts.ActivityTopTool, len(tools))
	for i, t := range tools {
		// Split server:tool key
		parts := strings.SplitN(t.key, ":", 2)
		if len(parts) == 2 {
			result[i] = contracts.ActivityTopTool{
				Server: parts[0],
				Tool:   parts[1],
				Count:  t.count,
			}
		}
	}
	return result
}

// =============================================================================
// Spec 069 A3 (MCP-750): GET /api/v1/activity/usage
// =============================================================================

const (
	usageDefaultTop    = 20
	usageDefaultSort   = "resp_bytes"
	usageDefaultWindow = "24h"
	usageTokenSource   = "bytes" // size-based proxy (FR-006); FR-010 → "estimated_tokens"
)

// usageParams holds the validated query parameters for the usage endpoint.
type usageParams struct {
	window string // "24h" | "7d" | "all"
	server string
	tool   string
	status string // "" | "success" | "error" | "blocked"
	top    int
	sort   string // "calls" | "resp_bytes" | "error_rate" | "p95"
}

// cacheKey is a stable identity for the params, used by the short-TTL cache.
func (p usageParams) cacheKey() string {
	return strings.Join([]string{p.window, p.server, p.tool, p.status, p.sort, strconv.Itoa(p.top)}, "|")
}

// windowStart returns the lower time bound for the window relative to now, plus
// whether a bound applies (false for "all").
func (p usageParams) windowStart(now time.Time) (time.Time, bool) {
	switch p.window {
	case "24h":
		return now.Add(-24 * time.Hour), true
	case "7d":
		return now.Add(-7 * 24 * time.Hour), true
	default: // "all"
		return time.Time{}, false
	}
}

// parseUsageParams validates the usage query string, returning a 400-style error
// message for any bad enum / non-int top.
func parseUsageParams(r *http.Request) (usageParams, error) {
	q := r.URL.Query()
	p := usageParams{
		window: usageDefaultWindow,
		server: q.Get("server"),
		tool:   q.Get("tool"),
		status: q.Get("status"),
		top:    usageDefaultTop,
		sort:   usageDefaultSort,
	}

	if v := q.Get("window"); v != "" {
		switch v {
		case "24h", "7d", "all":
			p.window = v
		default:
			return p, fmt.Errorf("invalid window %q (expected 24h, 7d, or all)", v)
		}
	}

	if v := q.Get("sort"); v != "" {
		switch v {
		case "calls", "resp_bytes", "error_rate", "p95":
			p.sort = v
		default:
			return p, fmt.Errorf("invalid sort %q (expected calls, resp_bytes, error_rate, or p95)", v)
		}
	}

	if p.status != "" {
		switch p.status {
		case "success", "error", "blocked":
		default:
			return p, fmt.Errorf("invalid status %q (expected success, error, or blocked)", p.status)
		}
	}

	if v := q.Get("top"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, fmt.Errorf("invalid top %q (expected a positive integer)", v)
		}
		p.top = n
	}

	return p, nil
}

// handleActivityUsage handles GET /api/v1/activity/usage
// @Summary Get usage statistics aggregate
// @Description Returns the actor-owned usage aggregate (per-tool rollup + timeline + tokens-saved headline) for the Web UI usage graphs (Spec 069). Served from an in-memory snapshot — never a per-request full-log scan. Per-tool metrics are lifetime-cumulative; `window` scopes the timeline and filters the tool list to tools active within the span.
// @Tags Activity
// @Accept json
// @Produce json
// @Param window query string false "Time window for timeline + tool-list membership" Enums(24h, 7d, all)
// @Param server query string false "Filter to one server"
// @Param tool query string false "Filter to one tool"
// @Param status query string false "Filter to tools with activity of this status" Enums(success, error, blocked)
// @Param top query int false "Top-N tools by sort key; remainder folded into 'other' (default 20)"
// @Param sort query string false "Ranking key for the per-tool list" Enums(calls, resp_bytes, error_rate, p95)
// @Success 200 {object} contracts.APIResponse{data=contracts.UsageAggregateResponse}
// @Failure 400 {object} contracts.APIResponse
// @Failure 401 {object} contracts.APIResponse
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/activity/usage [get]
func (s *Server) handleActivityUsage(w http.ResponseWriter, r *http.Request) {
	params, err := parseUsageParams(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	ttl := s.usageCacheTTL()
	key := params.cacheKey()
	if cached := s.getUsageCache(key, ttl); cached != nil {
		s.writeSuccess(w, cached)
		return
	}

	snap := s.controller.UsageSnapshot()
	tokens, _ := s.controller.GetTokenSavings()

	resp := buildUsageResponse(snap, tokens, params, time.Now().UTC())
	s.putUsageCache(key, resp, ttl)
	s.writeSuccess(w, resp)
}

// usageCacheTTL reads the configured read-cache freshness bound (FR-005),
// falling back to the default when config is unavailable. Read per request so
// the value hot-reloads with config.
func (s *Server) usageCacheTTL() time.Duration {
	def := time.Duration(config.DefaultObservabilityConfig().UsageCacheTTL)
	cfgIface := s.controller.GetCurrentConfig()
	cfg, ok := cfgIface.(*config.Config)
	if !ok || cfg == nil || cfg.Observability == nil {
		return def
	}
	if d := time.Duration(cfg.Observability.UsageCacheTTL); d > 0 {
		return d
	}
	return def
}

// buildUsageResponse projects the usage snapshot into the API contract, applying
// window/filter/sort/top-N. It performs no I/O and never scans the activity log
// (SC-005): the actor-owned snapshot is the incremental-precompute path (FR-005).
func buildUsageResponse(snap *internalRuntime.UsageAggregate, tokens *contracts.ServerTokenMetrics, p usageParams, now time.Time) *contracts.UsageAggregateResponse {
	resp := &contracts.UsageAggregateResponse{
		Window:      p.window,
		GeneratedAt: now,
		TokenSource: usageTokenSource,
		Tools:       make([]contracts.UsageToolStat, 0),
		Timeline:    make([]contracts.UsageTimeBucket, 0),
	}
	if tokens != nil {
		resp.TokensSaved = tokens.SavedTokens
		resp.TokensSavedPercentage = tokens.SavedTokensPercentage
	}
	if snap == nil {
		return resp
	}
	if !snap.UpdatedAt.IsZero() {
		if age := now.Sub(snap.UpdatedAt); age > 0 {
			resp.FreshnessMs = age.Milliseconds()
		}
	}

	start, bounded := p.windowStart(now)

	// Per-tool rollup: filter by membership, project to contract rows.
	rows := make([]contracts.UsageToolStat, 0, len(snap.Tools))
	for _, tu := range snap.Tools {
		if p.server != "" && tu.Server != p.server {
			continue
		}
		if p.tool != "" && tu.Tool != p.tool {
			continue
		}
		if !usageMatchesStatus(tu, p.status) {
			continue
		}
		if bounded && tu.LastUsed.Before(start) {
			continue // tool idle for the whole window
		}
		rows = append(rows, usageToolStat(tu))
	}

	sortUsageRows(rows, p.sort)

	// Top-N + 'other' fold.
	if len(rows) > p.top {
		other := &contracts.UsageOtherBucket{}
		for _, row := range rows[p.top:] {
			other.ToolsFolded++
			other.Calls += row.Calls
			other.TotalRespBytes += row.TotalRespBytes
		}
		resp.Other = other
		rows = rows[:p.top]
	}
	resp.Tools = rows

	// Timeline: global buckets trimmed to the window span.
	for _, b := range snap.Timeline() {
		if bounded && b.Start.Before(start) {
			continue
		}
		resp.Timeline = append(resp.Timeline, contracts.UsageTimeBucket{
			Start:          b.Start,
			Calls:          b.Calls,
			Errors:         b.Errors,
			TotalRespBytes: b.RespBytesSum,
		})
	}

	return resp
}

// usageMatchesStatus reports whether a tool has any activity of the requested
// status. Status filters operate as membership filters on the cumulative
// per-tool rollup (the aggregate does not retain per-status byte breakdowns).
func usageMatchesStatus(tu *internalRuntime.ToolUsage, status string) bool {
	switch status {
	case "":
		return true
	case "error":
		return tu.Errors > 0
	case "blocked":
		return tu.Blocked > 0
	case "success":
		return tu.Calls-tu.Errors > 0
	default:
		return true
	}
}

// usageToolStat projects a runtime ToolUsage into the API contract row.
func usageToolStat(tu *internalRuntime.ToolUsage) contracts.UsageToolStat {
	row := contracts.UsageToolStat{
		Server:         tu.Server,
		Tool:           tu.Tool,
		Calls:          tu.Calls,
		Errors:         tu.Errors,
		ErrorRate:      tu.ErrorRate(),
		Blocked:        tu.Blocked,
		TotalRespBytes: tu.RespBytesSum,
		TotalReqBytes:  tu.ReqBytesSum,
		SizedCalls:     tu.SizedRespCalls,
		P50Ms:          tu.Percentile(0.50),
		P95Ms:          tu.Percentile(0.95),
		LastUsed:       tu.LastUsed,
	}
	if avg, ok := tu.AvgRespBytes(); ok {
		row.AvgRespBytes = &avg
	}
	if avg, ok := tu.AvgReqBytes(); ok {
		row.AvgReqBytes = &avg
	}
	return row
}

// sortUsageRows orders rows descending by the requested key, breaking ties by
// server:tool for a deterministic response.
func sortUsageRows(rows []contracts.UsageToolStat, key string) {
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch key {
		case "calls":
			if a.Calls != b.Calls {
				return a.Calls > b.Calls
			}
		case "error_rate":
			if a.ErrorRate != b.ErrorRate {
				return a.ErrorRate > b.ErrorRate
			}
		case "p95":
			if a.P95Ms != b.P95Ms {
				return a.P95Ms > b.P95Ms
			}
		default: // resp_bytes
			if a.TotalRespBytes != b.TotalRespBytes {
				return a.TotalRespBytes > b.TotalRespBytes
			}
		}
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		return a.Tool < b.Tool
	}
	sort.Slice(rows, less)
}
