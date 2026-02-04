package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
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

	// Spec 027: Data flow security filters
	if flowType := q.Get("flow_type"); flowType != "" {
		filter.FlowType = flowType
	}

	if riskLevel := q.Get("risk_level"); riskLevel != "" {
		filter.RiskLevel = riskLevel
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
// @Param type query string false "Filter by activity type(s), comma-separated for multiple (Spec 024, 027)" Enums(tool_call, policy_decision, quarantine_change, server_change, system_start, system_stop, internal_tool_call, config_change, hook_evaluation, flow_summary)
// @Param server query string false "Filter by server name"
// @Param tool query string false "Filter by tool name"
// @Param session_id query string false "Filter by MCP session ID"
// @Param status query string false "Filter by status" Enums(success, error, blocked)
// @Param intent_type query string false "Filter by intent operation type (Spec 018)" Enums(read, write, destructive)
// @Param request_id query string false "Filter by HTTP request ID for log correlation (Spec 021)"
// @Param include_call_tool query bool false "Include successful call_tool_* internal tool calls (default: false, excluded to avoid duplicates)"
// @Param sensitive_data query bool false "Filter by sensitive data detection (true=has detections, false=no detections)"
// @Param detection_type query string false "Filter by specific detection type (e.g., 'aws_access_key', 'credit_card')"
// @Param severity query string false "Filter by severity level" Enums(critical, high, medium, low)
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
// @Param session_id query string false "Filter by MCP session ID"
// @Param status query string false "Filter by status"
// @Param start_time query string false "Filter activities after this time (RFC3339)"
// @Param end_time query string false "Filter activities before this time (RFC3339)"
// @Success 200 {string} string "Streamed activity records"
// @Failure 401 {object} contracts.APIResponse
// @Failure 500 {object} contracts.APIResponse
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/activity/export [get]
func (s *Server) handleExportActivity(w http.ResponseWriter, r *http.Request) {
	filter := parseActivityFilters(r)
	// Remove pagination limits for export - we want all matching records
	filter.Limit = 0
	filter.Offset = 0

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

	s.logger.Infow("Activity export completed", "format", format, "count", count)
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
