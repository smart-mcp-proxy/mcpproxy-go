package runtime

import (
	"encoding/json"
	"errors"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientrelay"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Client request outcomes (#1964): the vocabulary of the client_request row's
// metadata.outcome.
const (
	ClientRequestForwarded  = "forwarded"  // the calling client answered
	ClientRequestUnanswered = "unanswered" // nobody could be asked; mcpproxy answered
	ClientRequestFailed     = "failed"     // the client was asked and the request failed
)

// EmitActivityClientRequest publishes one request an upstream sent to a client
// (elicitation, sampling, roots) and how it was answered.
func (r *Runtime) EmitActivityClientRequest(e clientrelay.Event) {
	r.publishEvent(newEvent(EventTypeActivityClientRequest, clientRequestPayload(e)))
}

func clientRequestPayload(e clientrelay.Event) map[string]any {
	outcome, errMsg := ClientRequestForwarded, ""
	if e.Err != nil {
		outcome, errMsg = ClientRequestFailed, e.Err.Error()
		if errors.Is(e.Err, clientrelay.ErrNoClient) {
			outcome = ClientRequestUnanswered
		}
	}
	payload := map[string]any{
		"server_name":   e.Server,
		"method":        string(e.Method),
		"session_id":    e.SessionID,
		"outcome":       outcome,
		"error_message": errMsg,
		"duration_ms":   e.Duration.Milliseconds(),
	}
	if args := asMap(e.Params); args != nil {
		payload["arguments"] = args
	}
	if e.Result != nil {
		payload["response"] = e.Result
	}
	return payload
}

// asMap renders request params as the record's arguments map.
func asMap(v any) map[string]interface{} {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal(b, &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

// handleClientRequest persists one client_request row. Like prompt_get, the
// request and the answer are scanned for sensitive data: a user can type a
// secret into an elicitation form, and sampling text is model output.
func (s *ActivityService) handleClientRequest(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	method := getStringPayload(evt.Payload, "method")
	sessionID := getStringPayload(evt.Payload, "session_id")
	outcome := getStringPayload(evt.Payload, "outcome")
	arguments := getMapPayload(evt.Payload, "arguments")

	var responseStr string
	if resp := evt.Payload["response"]; resp != nil {
		if b, err := json.Marshal(resp); err == nil {
			responseStr = string(b)
		}
	}
	detectionSource := responseStr
	responseStr, truncated := s.truncateForStorage(responseStr)

	status := storage.ActivityStatusSuccess
	if outcome != ClientRequestForwarded {
		status = storage.ActivityStatusError
	}

	record := &storage.ActivityRecord{
		Type:              storage.ActivityTypeClientRequest,
		Source:            storage.ActivitySourceMCP,
		ServerName:        serverName,
		ToolName:          method,
		Arguments:         arguments,
		Response:          responseStr,
		ResponseTruncated: truncated,
		Status:            status,
		ErrorMessage:      getStringPayload(evt.Payload, "error_message"),
		DurationMs:        getInt64Payload(evt.Payload, "duration_ms"),
		Timestamp:         evt.Timestamp,
		SessionID:         sessionID,
		WorkSessionID:     s.resolveWorkSession(sessionID),
		Metadata:          s.withClientInfo(map[string]interface{}{"outcome": outcome}, sessionID),
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save client request activity",
			zap.Error(err), zap.String("server_name", serverName), zap.String("method", method))
		return
	}

	if s.detector != nil {
		s.workersWG.Add(1)
		go func() {
			defer s.workersWG.Done()
			s.runAsyncDetection(record.ID, arguments, detectionSource, record.ServerName)
		}()
	}
}
