package contracts

import "time"

// Spec 124: work sessions and their failure timeline.

// WorkSessionNotFoundMessage is the error text of a work-session door for an
// id that matches no session the caller may read. The CLI tells this answer
// from any other 404 (a proxy's, an older daemon's) by this text.
const WorkSessionNotFoundMessage = "Work session not found"

// WorkSessionNoSuchPositionMessage is the timeline door's refusal of a
// position (`at`) that names a failure or a row the session does not have.
// The CLI's walk over every failure reads it as "the session lost rows since
// the last page", which is the one way a number it was just given can be
// refused.
const WorkSessionNoSuchPositionMessage = "Invalid at. No such failure or row in this session"

// WorkSessionFailure describes the last failure of a work session.
type WorkSessionFailure struct {
	At         time.Time `json:"at"`
	ErrorClass string    `json:"error_class,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
	Server     string    `json:"server,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	// Reason is at most 200 characters and never contains argument or
	// response bodies.
	Reason string `json:"reason,omitempty"`
	// PolicyBlock is true when the failure was a refusal by MCPProxy itself.
	PolicyBlock bool `json:"policy_block,omitempty"`
}

// WorkSession is one agent's run of work, as the caller is allowed to see it.
// For a scoped caller every value is derived only from the servers in its
// reach.
type WorkSession struct {
	ID         string `json:"id"`
	TokenName  string `json:"token_name,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	ClientName string `json:"client_name,omitempty"`
	// ProjectName is the basename of the client's workspace, when it
	// disclosed one.
	ProjectName string `json:"project_name,omitempty"`
	Surface     string `json:"surface,omitempty"`
	RunName     string `json:"run_name,omitempty"`
	// Profile is returned to administrators only.
	Profile       string `json:"profile,omitempty"`
	ProfileSource string `json:"profile_source,omitempty"`

	// Status is "active" or "ended".
	Status         string     `json:"status"`
	StartedAt      time.Time  `json:"started_at"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	DurationMs     int64      `json:"duration_ms"`

	// Attempts, Failures and PolicyBlocks are lifetime counts of the session.
	// PolicyBlocks are included in Failures.
	Attempts     int `json:"attempts"`
	Failures     int `json:"failures"`
	PolicyBlocks int `json:"policy_blocks"`
	// StoredFailures counts the failures whose rows are still in the log;
	// it is what the timeline can navigate.
	StoredFailures int `json:"stored_failures"`

	FirstFailureAt *time.Time          `json:"first_failure_at,omitempty"`
	LastFailure    *WorkSessionFailure `json:"last_failure,omitempty"`

	// Administrators only.
	ConnectionCount  int        `json:"connection_count,omitempty"`
	TruncatedStartAt *time.Time `json:"truncated_start_at,omitempty"`
	Rebuilt          bool       `json:"rebuilt,omitempty"`
	Ownerless        bool       `json:"ownerless,omitempty"`
}

// WorkSessionListResponse is the body of GET /api/v1/work-sessions.
type WorkSessionListResponse struct {
	Sessions []WorkSession `json:"sessions"`
	Total    int           `json:"total"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

// Timeline row kinds.
const (
	TimelineKindCall           = "call"
	TimelineKindInternalCall   = "internal_call"
	TimelineKindPolicyDecision = "policy_decision"
	TimelineKindPrompt         = "prompt"
	TimelineKindUpstreamEvent  = "upstream_event"
	// TimelineKindSessionStart and TimelineKindSessionEnd are synthetic rows
	// derived from the session, not stored activity rows. They carry `at` and
	// no activity.
	TimelineKindSessionStart = "session_start"
	TimelineKindSessionEnd   = "session_end"
	TimelineKindOther        = "other"
)

// WorkSessionTimelineRow is one row of a session timeline.
type WorkSessionTimelineRow struct {
	// Index is the row's position in the caller's view of the timeline.
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	// At is the time of the row.
	At time.Time `json:"at"`
	// Failed marks the counted row of a failed attempt; PolicyBlock marks a
	// refusal by MCPProxy itself.
	Failed      bool `json:"failed,omitempty"`
	PolicyBlock bool `json:"policy_block,omitempty"`
	// BodiesRemoved is true when retention removed the row's argument and
	// response bodies to keep the row.
	BodiesRemoved bool `json:"bodies_removed,omitempty"`
	// PrecedesSession marks an upstream event from before the session
	// started: the latest event of a server the session then failed on,
	// still unresolved when the session began (release QA F2, SC-005).
	PrecedesSession bool `json:"precedes_session,omitempty"`
	// Activity is the stored row; absent on a session_start or session_end
	// row.
	Activity *ActivityRecord `json:"activity,omitempty"`
}

// WorkSessionTimelineResponse is the body of
// GET /api/v1/work-sessions/{id}/timeline.
type WorkSessionTimelineResponse struct {
	Session WorkSession              `json:"session"`
	Rows    []WorkSessionTimelineRow `json:"rows"`
	// Position is the index of the row the page is positioned at; TotalRows
	// is the number of rows in the caller's view.
	Position  int `json:"position"`
	TotalRows int `json:"total_rows"`
	// FailureIndex is the 1-based number of the failure at or before the
	// position among the stored failures in reach (0 when none), and
	// FailureTotal their count.
	FailureIndex int `json:"failure_index"`
	FailureTotal int `json:"failure_total"`
	// LifetimeFailures is the session's lifetime failure count in reach.
	// EarlierFailuresNotStored says some of them are no longer in the log.
	LifetimeFailures         int  `json:"lifetime_failures"`
	EarlierFailuresNotStored bool `json:"earlier_failures_not_stored,omitempty"`
	HasBefore                bool `json:"has_before"`
	HasAfter                 bool `json:"has_after"`
}

// The three types below describe the success envelope of the work-session
// doors for the OpenAPI document only; the handlers write the same shape
// through the shared success writer. They exist because the generator keeps
// one shared schema for every inline `SuccessResponse{data=...}` annotation,
// so such an annotation would give all of them the payload of the last one.

// WorkSessionListEnvelope is the response of GET /api/v1/work-sessions.
type WorkSessionListEnvelope struct {
	Success bool                    `json:"success"`
	Data    WorkSessionListResponse `json:"data"`
}

// WorkSessionEnvelope is the response of GET /api/v1/work-sessions/{id}.
type WorkSessionEnvelope struct {
	Success bool        `json:"success"`
	Data    WorkSession `json:"data"`
}

// WorkSessionTimelineEnvelope is the response of
// GET /api/v1/work-sessions/{id}/timeline.
type WorkSessionTimelineEnvelope struct {
	Success bool                        `json:"success"`
	Data    WorkSessionTimelineResponse `json:"data"`
}
