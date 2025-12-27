package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/storage"
)

// parseActivityFilters extracts activity filter parameters from the request query string.
func parseActivityFilters(r *http.Request) storage.ActivityFilter {
	filter := storage.DefaultActivityFilter()
	q := r.URL.Query()

	// Type filter
	if typeStr := q.Get("type"); typeStr != "" {
		filter.Type = typeStr
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

	filter.Validate()
	return filter
}

// handleListActivity handles GET /api/v1/activity
// @Summary List activity records
// @Description Returns paginated list of activity records with optional filtering
// @Tags Activity
// @Accept json
// @Produce json
// @Param type query string false "Filter by activity type" Enums(tool_call, policy_decision, quarantine_change, server_change)
// @Param server query string false "Filter by server name"
// @Param tool query string false "Filter by tool name"
// @Param session_id query string false "Filter by MCP session ID"
// @Param status query string false "Filter by status" Enums(success, error, blocked)
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
		s.writeError(w, http.StatusInternalServerError, "Failed to list activities")
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
		s.writeError(w, http.StatusBadRequest, "Activity ID is required")
		return
	}

	activity, err := s.controller.GetActivity(id)
	if err != nil {
		s.logger.Errorw("Failed to get activity", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get activity")
		return
	}

	if activity == nil {
		s.writeError(w, http.StatusNotFound, "Activity not found")
		return
	}

	response := contracts.ActivityDetailResponse{
		Activity: storageToContractActivity(activity),
	}

	s.writeSuccess(w, response)
}

// storageToContractActivity converts a storage ActivityRecord to a contracts ActivityRecord.
func storageToContractActivity(a *storage.ActivityRecord) contracts.ActivityRecord {
	return contracts.ActivityRecord{
		ID:                a.ID,
		Type:              contracts.ActivityType(a.Type),
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
	}
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

	// Validate format
	if format != "json" && format != "csv" {
		s.writeError(w, http.StatusBadRequest, "Invalid format. Use 'json' or 'csv'")
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
		csvHeader := "id,type,server_name,tool_name,status,error_message,duration_ms,timestamp,session_id,request_id,response_truncated\n"
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
			contractActivity := storageToContractActivity(activity)
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
