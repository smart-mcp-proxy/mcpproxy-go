package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 124 D6: work sessions and their failure timeline.
//
// These doors are separate from /api/v1/sessions, which lists MCP connections
// and stays admin-only. A work session has server attribution (its facts are
// kept per server), so it can be shown to a scoped caller as exactly the part
// in its reach: the caller must own the session, and every value it reads,
// sorts or filters by is recomputed from the servers it may see. A session
// that is not the caller's, or has nothing in the caller's reach, answers
// like one that does not exist.

const (
	workSessionListDefault = 25
	workSessionListMax     = 100
	timelineContextDefault = 20
	timelineContextMax     = 250
	// workSessionIdleWindow mirrors the tracker's idle window: a session with
	// no activity for this long is over.
	workSessionIdleWindow = 30 * time.Minute

	workSessionNotFound = "Work session not found"
)

// workSessionController is the storage a work-session door reads. It is an
// optional interface of the controller so that the many controller fakes of
// unrelated tests need not grow five methods.
type workSessionController interface {
	ListWorkSessionSummaries() ([]*storage.WorkSessionSummary, error)
	GetWorkSessionSummary(id string) (*storage.WorkSessionSummary, error)
	WorkSessionRowRefs(id string) ([]storage.WorkSessionRowRef, error)
	UpstreamEventRefs(server string, from, to time.Time) ([]storage.WorkSessionRowRef, error)
	GetActivitiesByKeys(keys [][]byte) ([]*storage.ActivityRecord, error)
}

// workSessions returns the controller's work-session storage. A controller
// without it (a fake in an unrelated test) has no work sessions: the list is
// empty and every id is not found.
func (s *Server) workSessions() workSessionController {
	if c, ok := s.controller.(workSessionController); ok {
		return c
	}
	return noWorkSessions{}
}

type noWorkSessions struct{}

func (noWorkSessions) ListWorkSessionSummaries() ([]*storage.WorkSessionSummary, error) {
	return nil, nil
}
func (noWorkSessions) GetWorkSessionSummary(string) (*storage.WorkSessionSummary, error) {
	return nil, nil
}
func (noWorkSessions) WorkSessionRowRefs(string) ([]storage.WorkSessionRowRef, error) {
	return nil, nil
}
func (noWorkSessions) UpstreamEventRefs(string, time.Time, time.Time) ([]storage.WorkSessionRowRef, error) {
	return nil, nil
}
func (noWorkSessions) GetActivitiesByKeys(keys [][]byte) ([]*storage.ActivityRecord, error) {
	return make([]*storage.ActivityRecord, len(keys)), nil
}

// workSessionReach is the caller's view of work sessions.
type workSessionReach struct {
	scoped bool
	ac     *auth.AuthContext
	ctx    context.Context
}

func newWorkSessionReach(ctx context.Context) workSessionReach {
	return workSessionReach{scoped: auth.IsScopedCaller(ctx), ac: auth.AuthContextFromContext(ctx), ctx: ctx}
}

// owns reports whether the session is the caller's own. An administrator owns
// nothing and needs to own nothing.
func (v workSessionReach) owns(s *storage.WorkSessionSummary) bool {
	if !v.scoped {
		return true
	}
	if s.Ownerless || v.ac == nil {
		return false
	}
	if v.ac.Type == auth.AuthTypeAgent {
		// Name and prefix both: names are unique per owner only, and the
		// prefix alone carries few random bits.
		return v.ac.TokenPrefix != "" && s.TokenPrefix == v.ac.TokenPrefix && s.TokenName == v.ac.AgentName
	}
	return v.ac.UserID != "" && s.UserID == v.ac.UserID
}

// allows reports whether a server's part of a session is in reach. The empty
// name is MCPProxy's own tools, which a scoped caller's activity view never
// includes.
func (v workSessionReach) allows(server string) bool {
	if !v.scoped {
		return true
	}
	return server != "" && canSeeServer(v.ctx, server)
}

// project returns the session as the caller may see it, or false when it must
// read as not found.
func (v workSessionReach) project(s *storage.WorkSessionSummary, now time.Time) (contracts.WorkSession, bool) {
	if s == nil || !v.owns(s) {
		return contracts.WorkSession{}, false
	}
	var allow func(string) bool
	if v.scoped {
		allow = v.allows
		inReach := false
		for name := range s.Servers {
			if v.allows(name) {
				inReach = true
				break
			}
		}
		if !inReach {
			return contracts.WorkSession{}, false
		}
	}
	t := s.Totals(allow)

	out := contracts.WorkSession{
		ID:             s.ID,
		TokenName:      s.TokenName,
		ClientID:       s.ClientID,
		ClientName:     s.ClientName,
		ProjectName:    s.ProjectName,
		Surface:        s.Surface,
		RunName:        s.RunName,
		Attempts:       t.Attempts,
		Failures:       t.Failures,
		PolicyBlocks:   t.Blocks,
		StoredFailures: t.StoredFailures,
	}

	if v.scoped {
		// Start, end and status come from the part in reach only, so that
		// activity on a hidden server changes nothing the caller reads.
		out.StartedAt, out.LastActivityAt = t.FirstActivityAt, t.LastActivityAt
		if !s.EndedAt.IsZero() || now.Sub(t.LastActivityAt) > workSessionIdleWindow {
			end := t.LastActivityAt
			out.EndedAt = &end
		}
	} else {
		out.StartedAt, out.LastActivityAt = s.StartedAt, s.LastActivityAt
		if !s.EndedAt.IsZero() {
			end := s.EndedAt
			out.EndedAt = &end
		}
		out.Profile, out.ProfileSource = s.Profile, s.ProfileSource
		out.ConnectionCount = s.ConnectionCount
		out.Rebuilt, out.Ownerless = s.Rebuilt, s.Ownerless
		if !s.TruncatedStartAt.IsZero() {
			ts := s.TruncatedStartAt
			out.TruncatedStartAt = &ts
		}
	}
	out.Status = "active"
	if out.EndedAt != nil {
		out.Status = "ended"
	}
	out.DurationMs = out.LastActivityAt.Sub(out.StartedAt).Milliseconds()

	if !t.FirstFailureAt.IsZero() {
		ff := t.FirstFailureAt
		out.FirstFailureAt = &ff
	}
	if !t.LastFailureAt.IsZero() {
		server, _ := s.LastFailureServer(allow)
		out.LastFailure = &contracts.WorkSessionFailure{
			At: t.LastFailureAt, ErrorClass: t.LastErrorClass, ErrorCode: t.LastErrorCode,
			Server: server, Tool: t.LastTool, Reason: t.LastReason, PolicyBlock: t.LastFailureBlock,
		}
	}
	return out, true
}

func parseBoolParam(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		return def
	}
}

func parseTimeParam(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}

// handleListWorkSessions godoc
// @Summary List work sessions
// @Description Lists work sessions (one agent's run of work) with their failure facts. An agent token sees only its own sessions, and only the part of each that touched servers in its reach.
// @Tags activity
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param failed query bool false "Only sessions with at least one failure"
// @Param include_blocked query bool false "With failed: count policy blocks as failures (default true)"
// @Param token query string false "Filter by agent token name"
// @Param client query string false "Filter by client id"
// @Param profile query string false "Filter by profile (administrators only)"
// @Param run query string false "Filter by run name"
// @Param error_class query string false "Filter by the error class of the last failure"
// @Param since query string false "Only sessions active at or after this time (RFC3339)"
// @Param until query string false "Only sessions active at or before this time (RFC3339)"
// @Param sort query string false "last_activity (default) or last_failure" Enums(last_activity, last_failure)
// @Param limit query int false "Page size (default 25, max 100)"
// @Param offset query int false "Page offset"
// @Success 200 {object} contracts.SuccessResponse{data=contracts.WorkSessionListResponse}
// @Failure 400 {object} contracts.ErrorResponse
// @Router /api/v1/work-sessions [get]
func (s *Server) handleListWorkSessions(w http.ResponseWriter, r *http.Request) {
	c := s.workSessions()
	q := r.URL.Query()

	limit := workSessionListDefault
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > workSessionListMax {
			s.writeError(w, r, http.StatusBadRequest, "Invalid limit. Use a number from 1 to 100")
			return
		}
		limit = n
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			s.writeError(w, r, http.StatusBadRequest, "Invalid offset")
			return
		}
		offset = n
	}
	sortBy := q.Get("sort")
	if sortBy == "" {
		sortBy = "last_activity"
	}
	if sortBy != "last_activity" && sortBy != "last_failure" {
		s.writeError(w, r, http.StatusBadRequest, "Invalid sort. Use 'last_activity' or 'last_failure'")
		return
	}
	since, okSince := parseTimeParam(q.Get("since"))
	until, okUntil := parseTimeParam(q.Get("until"))
	if !okSince || !okUntil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid since or until. Use RFC3339 time")
		return
	}
	failedOnly := parseBoolParam(q.Get("failed"), false)
	includeBlocked := parseBoolParam(q.Get("include_blocked"), true)
	token, client, profile := q.Get("token"), q.Get("client"), q.Get("profile")
	run, errorClass := q.Get("run"), q.Get("error_class")

	summaries, err := c.ListWorkSessionSummaries()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list work sessions")
		return
	}

	reach := newWorkSessionReach(r.Context())
	now := time.Now().UTC()
	sessions := make([]contracts.WorkSession, 0, len(summaries))
	for _, sum := range summaries {
		ws, visible := reach.project(sum, now)
		if !visible {
			continue
		}
		if failedOnly {
			failures := ws.Failures
			if !includeBlocked {
				failures -= ws.PolicyBlocks
			}
			if failures <= 0 {
				continue
			}
		}
		if token != "" && ws.TokenName != token {
			continue
		}
		if client != "" && ws.ClientID != client {
			continue
		}
		// The profile is not part of a scoped caller's view, so it cannot be
		// filtered on there: the filter would be an oracle.
		if profile != "" && (reach.scoped || ws.Profile != profile) {
			continue
		}
		if run != "" && ws.RunName != run {
			continue
		}
		if errorClass != "" && (ws.LastFailure == nil || ws.LastFailure.ErrorClass != errorClass) {
			continue
		}
		if !since.IsZero() && ws.LastActivityAt.Before(since) {
			continue
		}
		if !until.IsZero() && ws.StartedAt.After(until) {
			continue
		}
		sessions = append(sessions, ws)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if sortBy == "last_failure" {
			var at, bt time.Time
			if a.LastFailure != nil {
				at = a.LastFailure.At
			}
			if b.LastFailure != nil {
				bt = b.LastFailure.At
			}
			if !at.Equal(bt) {
				return at.After(bt)
			}
		}
		if !a.LastActivityAt.Equal(b.LastActivityAt) {
			return a.LastActivityAt.After(b.LastActivityAt)
		}
		return a.ID < b.ID
	})

	total := len(sessions)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	s.writeSuccess(w, contracts.WorkSessionListResponse{Sessions: sessions[offset:end], Total: total, Limit: limit, Offset: offset})
}

// handleGetWorkSession godoc
// @Summary Get one work session
// @Tags activity
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Work session id"
// @Success 200 {object} contracts.SuccessResponse{data=contracts.WorkSession}
// @Failure 404 {object} contracts.ErrorResponse
// @Router /api/v1/work-sessions/{id} [get]
func (s *Server) handleGetWorkSession(w http.ResponseWriter, r *http.Request) {
	c := s.workSessions()
	sum, err := c.GetWorkSessionSummary(chi.URLParam(r, "id"))
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read work session")
		return
	}
	ws, visible := newWorkSessionReach(r.Context()).project(sum, time.Now().UTC())
	if !visible {
		s.writeError(w, r, http.StatusNotFound, workSessionNotFound)
		return
	}
	s.writeSuccess(w, ws)
}

func timelineKind(t storage.ActivityType) string {
	switch t {
	case storage.ActivityTypeToolCall:
		return contracts.TimelineKindCall
	case storage.ActivityTypeInternalToolCall:
		return contracts.TimelineKindInternalCall
	case storage.ActivityTypePolicyDecision:
		return contracts.TimelineKindPolicyDecision
	case storage.ActivityTypePromptGet:
		return contracts.TimelineKindPrompt
	case storage.ActivityTypeUpstreamEvent:
		return contracts.TimelineKindUpstreamEvent
	default:
		return contracts.TimelineKindOther
	}
}

// activityKeyID is the activity id inside an activity key
// ("<20-digit time>_<id>").
func activityKeyID(key []byte) string {
	if len(key) < 22 {
		return ""
	}
	return string(key[21:])
}

// handleWorkSessionTimeline godoc
// @Summary Get a work session's timeline
// @Description Returns the session's activity rows and the upstream events of the servers it called, in time order, positioned at a failure, a row or the end. Bodies are omitted unless include_bodies is set.
// @Tags activity
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Work session id"
// @Param at query string false "first_failure (default), failure:<n>, row:<activity id> or end"
// @Param before query int false "Rows before the position (default 20, max 250)"
// @Param after query int false "Rows after the position (default 20, max 250)"
// @Param include_bodies query bool false "Include argument and response bodies"
// @Success 200 {object} contracts.SuccessResponse{data=contracts.WorkSessionTimelineResponse}
// @Failure 400 {object} contracts.ErrorResponse
// @Failure 404 {object} contracts.ErrorResponse
// @Router /api/v1/work-sessions/{id}/timeline [get]
func (s *Server) handleWorkSessionTimeline(w http.ResponseWriter, r *http.Request) {
	c := s.workSessions()
	ctx := r.Context()
	q := r.URL.Query()
	id := chi.URLParam(r, "id")

	contextParam := func(name string) (int, bool) {
		v := q.Get(name)
		if v == "" {
			return timelineContextDefault, true
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > timelineContextMax {
			return 0, false
		}
		return n, true
	}
	before, okBefore := contextParam("before")
	after, okAfter := contextParam("after")
	if !okBefore || !okAfter {
		s.writeError(w, r, http.StatusBadRequest, "Invalid before or after. Use a number from 0 to 250")
		return
	}
	includeBodies := parseBoolParam(q.Get("include_bodies"), false)

	sum, err := c.GetWorkSessionSummary(id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read work session")
		return
	}
	reach := newWorkSessionReach(ctx)
	now := time.Now().UTC()
	session, visible := reach.project(sum, now)
	if !visible {
		s.writeError(w, r, http.StatusNotFound, workSessionNotFound)
		return
	}

	// The session's own rows, narrowed to the caller's reach by the server
	// stored in the index, before any row is loaded.
	rowRefs, err := c.WorkSessionRowRefs(id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read work session")
		return
	}
	refs := make([]storage.WorkSessionRowRef, 0, len(rowRefs))
	for _, ref := range rowRefs {
		if reach.allows(ref.Server) {
			refs = append(refs, ref)
		}
	}

	// Upstream events of the servers the session called, inside the span the
	// caller sees (FR-023), and only those the caller may read (FR-025).
	spanEnd := now
	if session.EndedAt != nil {
		spanEnd = *session.EndedAt
	}
	viewer := activityEventViewer(ctx)
	var events []storage.WorkSessionRowRef
	for server := range sum.Servers {
		if server == "" || !reach.allows(server) {
			continue
		}
		evs, err := c.UpstreamEventRefs(server, session.StartedAt, spanEnd)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "Failed to read upstream events")
			return
		}
		for _, ev := range evs {
			if ev.Owner != "" && viewer != nil && viewer.UserID != ev.Owner {
				continue
			}
			events = append(events, ev)
		}
	}
	all := storage.MergeTimelineRefs(refs, events)

	// Failures the caller can navigate: counted failed rows still stored.
	var failures []int
	for i, ref := range all {
		if ref.Counted && ref.Failed {
			failures = append(failures, i)
		}
	}

	position := 0
	at := q.Get("at")
	switch {
	case at == "" || at == "first_failure":
		if len(failures) > 0 {
			position = failures[0]
		}
	case at == "end":
		if len(all) > 0 {
			position = len(all) - 1
		}
	case strings.HasPrefix(at, "failure:"):
		n, err := strconv.Atoi(strings.TrimPrefix(at, "failure:"))
		if err != nil || n < 1 || n > len(failures) {
			s.writeError(w, r, http.StatusBadRequest, "Invalid at. No such failure in this session")
			return
		}
		position = failures[n-1]
	case strings.HasPrefix(at, "row:"):
		want := strings.TrimPrefix(at, "row:")
		found := false
		for i, ref := range all {
			if activityKeyID(ref.ActivityKey) == want {
				position, found = i, true
				break
			}
		}
		if !found {
			s.writeError(w, r, http.StatusBadRequest, "Invalid at. No such row in this session")
			return
		}
	default:
		s.writeError(w, r, http.StatusBadRequest, "Invalid at. Use first_failure, failure:<n>, row:<activity id> or end")
		return
	}

	from := position - before
	if from < 0 {
		from = 0
	}
	to := position + after + 1
	if to > len(all) {
		to = len(all)
	}
	page := all[from:to]
	keys := make([][]byte, len(page))
	for i, ref := range page {
		keys[i] = ref.ActivityKey
	}
	records, err := c.GetActivitiesByKeys(keys)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read activity rows")
		return
	}

	rows := make([]contracts.WorkSessionTimelineRow, 0, len(page))
	for i, rec := range records {
		if rec == nil {
			continue // removed by retention since the index was read
		}
		// Defence in depth: the index said the row is in reach; the row must
		// agree, and an owned event must be the caller's.
		if !reach.allows(rec.ServerName) || !viewer.MaySee(rec) {
			continue
		}
		record := storageToContractActivity(rec)
		s.maskActivityPayloads(&record)
		redactForeignIdentity(ctx, rec, &record)
		if !includeBodies {
			record.Arguments, record.Response = nil, ""
		}
		rows = append(rows, contracts.WorkSessionTimelineRow{
			Index:         from + i,
			Kind:          timelineKind(rec.Type),
			Failed:        page[i].Counted && page[i].Failed,
			PolicyBlock:   page[i].Block,
			BodiesRemoved: rec.BodiesRemoved,
			Activity:      record,
		})
	}

	failureIndex := 0
	for n, idx := range failures {
		if idx <= position {
			failureIndex = n + 1
		}
	}
	s.writeSuccess(w, contracts.WorkSessionTimelineResponse{
		Session:                  session,
		Rows:                     rows,
		Position:                 position,
		TotalRows:                len(all),
		FailureIndex:             failureIndex,
		FailureTotal:             len(failures),
		LifetimeFailures:         session.Failures,
		EarlierFailuresNotStored: session.Failures > len(failures),
		HasBefore:                from > 0,
		HasAfter:                 to < len(all),
	})
}
