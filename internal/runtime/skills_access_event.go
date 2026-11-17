package runtime

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Skill access outcomes (Spec 125 FR-024). The vocabulary is closed: the
// activity row, its filters and the Web UI label switch on these values.
const (
	SkillAccessSuccess      = "success"
	SkillAccessNotFound     = "not_found"
	SkillAccessBinary       = "binary"
	SkillAccessDamaged      = "damaged"
	SkillAccessStaleVersion = "stale_version"
	SkillAccessBlocked      = "blocked" // the output sanitisation policy withheld the text
)

// Skill access actions.
const (
	SkillActionSearch = "search"
	SkillActionLoad   = "load"
	SkillActionRead   = "read"
)

// SkillAccessActivity describes one agent search, load or file read of a skill.
// It carries sizes and the content hash, never skill text: DetectionText is the
// one exception, it is scanned for sensitive data by the activity service and
// dropped.
type SkillAccessActivity struct {
	Action  string // search | load | read
	Tool    string // retrieve_skills | load_skill | read_skill_file
	Outcome string // one of the SkillAccess* outcomes
	// Server is the skill's origin server for a load or read the caller may
	// reach, and empty otherwise (a search, or an id out of the caller's reach).
	// Rows with no server are administrator-only on every activity read door.
	Server    string
	SkillID   string // the id as the caller gave it (bounded by the caller)
	SkillName string
	File      string // read_skill_file: the relative path asked for
	Query     string // retrieve_skills: the search text
	Hash      string // approved content hash of the skill that answered
	Offset    int64
	Bytes     int64 // bytes of skill text returned
	TotalSize int64 // size of the file or skill answered from
	Results   int   // retrieve_skills: skills returned
	Matches   int   // retrieve_skills: matches available to the caller

	SessionID  string
	RequestID  string
	DurationMs int64
	// DetectionText is the text delivered to the agent, for the sensitive-data
	// scan. It is not stored.
	DetectionText string
	Attribution   ActivityAttribution
}

// EmitActivitySkillAccess publishes one skill_access activity event.
func (r *Runtime) EmitActivitySkillAccess(a SkillAccessActivity) {
	payload := map[string]any{
		// Always present, even empty: the SSE door treats this event type as
		// identity-bearing and drops an unnamed frame for a scoped subscriber.
		"server_name": a.Server,
		"action":      a.Action,
		"tool":        a.Tool,
		"outcome":     a.Outcome,
		"session_id":  a.SessionID,
		"request_id":  a.RequestID,
		"duration_ms": a.DurationMs,
		"offset":      a.Offset,
		"bytes":       a.Bytes,
		"total_bytes": a.TotalSize,
		"results":     a.Results,
		"matches":     a.Matches,
	}
	for k, v := range map[string]string{
		"skill_id": a.SkillID, "skill": a.SkillName, "file": a.File, "query": a.Query,
		"hash": a.Hash, "detection_text": a.DetectionText,
	} {
		if v != "" {
			payload[k] = v
		}
	}
	if at := a.Attribution.payload(); at != nil {
		payload[attributionPayloadKey] = at
	}
	r.publishEvent(newEvent(EventTypeActivitySkillAccess, payload))
}

// handleSkillAccess persists one skill access row (Spec 125 FR-024).
func (s *ActivityService) handleSkillAccess(evt Event) {
	action := getStringPayload(evt.Payload, "action")
	outcome := getStringPayload(evt.Payload, "outcome")
	sessionID := getStringPayload(evt.Payload, "session_id")
	serverName := getStringPayload(evt.Payload, "server_name")

	metadata := map[string]interface{}{
		"action":      action,
		"outcome":     outcome,
		"tool":        getStringPayload(evt.Payload, "tool"),
		"offset":      getInt64Payload(evt.Payload, "offset"),
		"bytes":       getInt64Payload(evt.Payload, "bytes"),
		"total_bytes": getInt64Payload(evt.Payload, "total_bytes"),
	}
	for _, k := range []string{"skill_id", "skill", "file", "hash"} {
		if v := getStringPayload(evt.Payload, k); v != "" {
			metadata[k] = v
		}
	}
	if action == SkillActionSearch {
		metadata["results"] = getInt64Payload(evt.Payload, "results")
		metadata["matches"] = getInt64Payload(evt.Payload, "matches")
	}
	metadata = s.withClientInfo(metadata, sessionID)

	arguments := map[string]interface{}{}
	if q := getStringPayload(evt.Payload, "query"); q != "" {
		arguments["query"] = q
	}
	if id := getStringPayload(evt.Payload, "skill_id"); id != "" {
		arguments["id"] = id
	}
	if f := getStringPayload(evt.Payload, "file"); f != "" {
		arguments["path"] = f
	}

	status := storage.ActivityStatusSuccess
	errMsg := ""
	if outcome != SkillAccessSuccess {
		status = storage.ActivityStatusError
		errMsg = outcome
	}

	record := &storage.ActivityRecord{
		Type:          storage.ActivityTypeSkillAccess,
		Source:        storage.ActivitySourceMCP,
		ToolName:      getStringPayload(evt.Payload, "tool"),
		ServerName:    serverName,
		Arguments:     arguments,
		Status:        status,
		ErrorMessage:  errMsg,
		DurationMs:    getInt64Payload(evt.Payload, "duration_ms"),
		Metadata:      metadata,
		Timestamp:     evt.Timestamp,
		SessionID:     sessionID,
		WorkSessionID: s.resolveWorkSession(sessionID),
		RequestID:     getStringPayload(evt.Payload, "request_id"),
		ResponseBytes: int(getInt64Payload(evt.Payload, "bytes")),
	}
	applyAttribution(record, attributionFromPayload(evt.Payload))

	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save skill access activity",
			zap.Error(err), zap.String("action", action), zap.String("server_name", serverName))
		return
	}

	// The skill text was delivered to an agent: run the same sensitive-data scan
	// the upstream tool results get, tracked like the tool path.
	if text := getStringPayload(evt.Payload, "detection_text"); text != "" && s.detector != nil {
		s.workersWG.Add(1)
		go func() {
			defer s.workersWG.Done()
			s.runAsyncDetection(record.ID, nil, text, record.ServerName)
		}()
	}
}
