package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
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

// clientRequestPayload never carries what the user, the model or the client
// answered: only the shape of it.
func clientRequestPayload(e clientrelay.Event) map[string]any {
	outcome, errMsg := ClientRequestForwarded, ""
	switch {
	case e.Err == nil:
	case errors.Is(e.Err, clientrelay.ErrNoClient):
		outcome, errMsg = ClientRequestUnanswered, e.Err.Error() // mcpproxy's own text
	case errors.Is(e.Err, context.DeadlineExceeded), errors.Is(e.Err, context.Canceled):
		outcome, errMsg = ClientRequestFailed, "the client did not answer in time"
	default:
		outcome, errMsg = ClientRequestFailed, "the client returned an error"
	}
	payload := map[string]any{
		"server_name":   e.Server,
		"method":        string(e.Method),
		"session_id":    e.SessionID,
		"outcome":       outcome,
		"error_message": errMsg,
		"duration_ms":   e.Duration.Milliseconds(),
	}
	if args := cappedArguments(e.Params); args != nil {
		payload["arguments"] = args
	}
	if answer := answerShape(e.Result); answer != nil {
		payload["answer"] = answer
	}
	return payload
}

const (
	maxClientRequestArguments = 4096 // bytes of JSON kept of what the upstream asked
	maxClientRequestFields    = 50   // form fields named per answer
	maxClientRequestFieldName = 100  // bytes of one field name
)

// answerShape describes an answer without its content.
func answerShape(result any) map[string]any {
	switch r := result.(type) {
	case *mcp.ElicitationResult:
		shape := map[string]any{"action": string(r.Action)}
		if content, ok := r.Content.(map[string]any); ok && len(content) > 0 {
			shape["field_count"] = len(content)
			sizes := make(map[string]any, min(len(content), maxClientRequestFields))
			for name, v := range content {
				if len(sizes) == maxClientRequestFields {
					break
				}
				sizes[truncateUTF8(name, maxClientRequestFieldName)] = jsonSize(v)
			}
			shape["field_bytes"] = sizes
		}
		return shape
	case *mcp.CreateMessageResult:
		return map[string]any{"response_bytes": jsonSize(r.Content)}
	case *mcp.ListRootsResult:
		return map[string]any{"root_count": len(r.Roots)}
	}
	return nil
}

func jsonSize(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "")
}

// cappedArguments renders request params as the record's arguments map, or a
// prefix of their JSON when they are too large.
func cappedArguments(v any) map[string]interface{} {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if len(b) > maxClientRequestArguments {
		return map[string]interface{}{
			"truncated": truncateUTF8(string(b), maxClientRequestArguments),
			"bytes":     len(b),
		}
	}
	var m map[string]interface{}
	if json.Unmarshal(b, &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

// handleClientRequest persists one client_request row. What the upstream asked
// is scanned for sensitive data; what it got back is not in the event.
func (s *ActivityService) handleClientRequest(evt Event) {
	serverName := getStringPayload(evt.Payload, "server_name")
	method := getStringPayload(evt.Payload, "method")
	sessionID := getStringPayload(evt.Payload, "session_id")
	outcome := getStringPayload(evt.Payload, "outcome")
	arguments := getMapPayload(evt.Payload, "arguments")

	metadata := map[string]interface{}{}
	for k, v := range getMapPayload(evt.Payload, "answer") {
		metadata[k] = v
	}
	metadata["outcome"] = outcome

	status := storage.ActivityStatusSuccess
	if outcome != ClientRequestForwarded {
		status = storage.ActivityStatusError
	}

	record := &storage.ActivityRecord{
		Type:          storage.ActivityTypeClientRequest,
		Source:        storage.ActivitySourceMCP,
		ServerName:    serverName,
		ToolName:      method,
		Arguments:     arguments,
		Status:        status,
		ErrorMessage:  getStringPayload(evt.Payload, "error_message"),
		DurationMs:    getInt64Payload(evt.Payload, "duration_ms"),
		Timestamp:     evt.Timestamp,
		SessionID:     sessionID,
		WorkSessionID: s.resolveWorkSession(sessionID),
		Metadata:      s.withClientInfo(metadata, sessionID),
	}

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save client request activity",
			zap.Error(err), zap.String("server_name", serverName), zap.String("method", method))
		return
	}

	if s.detector != nil && len(arguments) > 0 {
		s.workersWG.Add(1)
		go func() {
			defer s.workersWG.Done()
			s.runAsyncDetection(record, arguments, "")
		}()
	}
}
