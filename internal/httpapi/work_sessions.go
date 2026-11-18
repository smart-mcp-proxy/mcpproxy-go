package httpapi

import (
	"bytes"
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
	// timelinePageMax is the most rows one page returns (FR-044), the
	// positioned row included.
	timelinePageMax = 500
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
	WalkWorkSessionTimeline(id string, servers []string, from, to time.Time, fn func(storage.WorkSessionRowRef) bool) error
	GetActivitiesByKeys(keys [][]byte) ([]*storage.ActivityRecord, error)
	OpenUpstreamEventsBefore(servers []string, before, notBefore time.Time, visible func(owner string) bool) ([]storage.WorkSessionRowRef, error)
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
func (noWorkSessions) WalkWorkSessionTimeline(string, []string, time.Time, time.Time, func(storage.WorkSessionRowRef) bool) error {
	return nil
}
func (noWorkSessions) GetActivitiesByKeys(keys [][]byte) ([]*storage.ActivityRecord, error) {
	return make([]*storage.ActivityRecord, len(keys)), nil
}
func (noWorkSessions) OpenUpstreamEventsBefore([]string, time.Time, time.Time, func(string) bool) ([]storage.WorkSessionRowRef, error) {
	return nil, nil
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

// ownsRow reports whether a request row is the caller's own. An administrator
// reads every row. Upstream events are not request rows and are judged by
// their own rule.
func (v workSessionReach) ownsRow(rec *storage.ActivityRecord) bool {
	if !v.scoped || rec.Type == storage.ActivityTypeUpstreamEvent {
		return true
	}
	// Spec 125 FR-019a: an administrator-only row type is never a scoped
	// caller's row, whatever identity it carries.
	if v.ac == nil || storage.IsAdminOnlyActivityType(rec.Type) {
		return false
	}
	if v.ac.Type == auth.AuthTypeAgent {
		return callerOwnsActivity(v.ctx, rec)
	}
	return v.ac.UserID != "" && rec.UserID == v.ac.UserID
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
		ProjectName:    s.ProjectNameIn(allow),
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
		// The summary's own end is not consulted: a call on a hidden server
		// clears it, and that must not reopen what this caller sees.
		if now.Sub(t.LastActivityAt) > workSessionIdleWindow {
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
// @Success 200 {object} contracts.WorkSessionListEnvelope
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
// @Success 200 {object} contracts.WorkSessionEnvelope
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
// @Success 200 {object} contracts.WorkSessionTimelineEnvelope
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

	// A page never exceeds timelinePageMax rows, the positioned row included.
	if before+after+1 > timelinePageMax {
		after = timelinePageMax - 1 - before
	}

	// Upstream events of the servers the session called, inside the span the
	// caller sees (FR-023), and only those the caller may read (FR-025).
	// They are merged in by the storage walk below, from cursors, so a small
	// page of a session that spans weeks of reconnect trouble does not load
	// the event history.
	spanEnd := now
	if session.EndedAt != nil {
		spanEnd = *session.EndedAt
	}
	viewer := activityEventViewer(ctx)
	var eventServers []string
	for server := range sum.Servers {
		if server != "" && reach.allows(server) {
			eventServers = append(eventServers, server)
		}
	}

	at := q.Get("at")
	col, okAt := newTimelineCollector(at, before, after)
	if !okAt {
		s.writeError(w, r, http.StatusBadRequest, "Invalid at. Use first_failure, failure:<n>, row:<activity id> or end")
		return
	}

	// One pass over the session's index, in time order, merging the events
	// in. Nothing but the page is kept: the pass counts rows and failures and
	// finds the position, so a page of one row of a very long session costs
	// index keys, not memory.
	// Release QA F2 (SC-005): a server the session failed on that was already
	// down when the session started has its cause before the span. The latest
	// event of each such server before the start is placed ahead of the start
	// row while no recovery followed it, under the same FR-025 rule.
	var failedServers []string
	for _, server := range eventServers {
		// Only a failure that a down server explains: the call could not
		// reach it, or it waits for a login.
		if p := sum.Servers[server]; p != nil && p.UnavailableFailure {
			failedServers = append(failedServers, server)
		}
	}
	if len(failedServers) > 0 {
		// processStart: the recovery after a restart is not recorded, so an
		// event from before this process is no evidence the server was down.
		open, err := c.OpenUpstreamEventsBefore(failedServers, session.StartedAt, s.eventsSince, func(owner string) bool {
			return owner == "" || viewer == nil || viewer.UserID == owner
		})
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "Failed to read work session")
			return
		}
		sort.Slice(open, func(i, j int) bool {
			if !open[i].Timestamp.Equal(open[j].Timestamp) {
				return open[i].Timestamp.Before(open[j].Timestamp)
			}
			return bytes.Compare(open[i].ActivityKey, open[j].ActivityKey) < 0
		})
		for _, ref := range open {
			col.add(timelineEntry{ref: ref, precedes: true})
		}
	}
	col.add(timelineEntry{kind: contracts.TimelineKindSessionStart, at: session.StartedAt})
	scanned := 0
	walkErr := c.WalkWorkSessionTimeline(id, eventServers, session.StartedAt, spanEnd, func(ref storage.WorkSessionRowRef) bool {
		scanned++
		if scanned%4096 == 0 && ctx.Err() != nil {
			return false
		}
		if ref.Event {
			if ref.Owner != "" && viewer != nil && viewer.UserID != ref.Owner {
				return true // another user's credential (FR-025)
			}
		} else if !reach.allows(ref.Server) {
			return true // outside reach: never seen, counted or positioned on
		}
		col.add(timelineEntry{ref: ref})
		return true
	})
	if walkErr != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read work session")
		return
	}
	if ctx.Err() != nil {
		return
	}
	if session.EndedAt != nil {
		col.add(timelineEntry{kind: contracts.TimelineKindSessionEnd, at: *session.EndedAt})
	}
	page, from, position, okPos := col.finish()
	if !okPos {
		s.writeError(w, r, http.StatusBadRequest, "Invalid at. No such failure or row in this session")
		return
	}

	keys := make([][]byte, 0, len(page))
	for _, e := range page {
		if e.kind == "" {
			keys = append(keys, e.ref.ActivityKey)
		}
	}
	records, err := c.GetActivitiesByKeys(keys)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read activity rows")
		return
	}

	rows := make([]contracts.WorkSessionTimelineRow, 0, len(page))
	next := 0
	for i, e := range page {
		if e.kind != "" {
			rows = append(rows, contracts.WorkSessionTimelineRow{Index: from + i, Kind: e.kind, At: e.at})
			continue
		}
		rec := records[next]
		next++
		if rec == nil {
			continue // removed by retention since the index was read
		}
		// Defence in depth. The index said the row is in reach; the row must
		// agree. An owned event must be the caller's. And a scoped caller
		// reads only request rows it made: the session is its own, but a row
		// that carries no credential (written before credentials were
		// stamped on every row) is not provably its own.
		if !reach.allows(rec.ServerName) || !viewer.MaySee(rec) || !reach.ownsRow(rec) {
			continue
		}
		record := storageToContractActivity(rec)
		s.maskActivityPayloads(&record)
		redactForeignIdentity(ctx, rec, &record)
		if !includeBodies {
			record.Arguments, record.Response = nil, ""
		}
		rows = append(rows, contracts.WorkSessionTimelineRow{
			Index:           from + i,
			Kind:            timelineKind(rec.Type),
			At:              rec.Timestamp,
			Failed:          e.ref.Counted && e.ref.Failed,
			PolicyBlock:     e.ref.Block,
			BodiesRemoved:   rec.BodiesRemoved,
			PrecedesSession: e.precedes,
			Activity:        &record,
		})
	}

	s.writeSuccess(w, contracts.WorkSessionTimelineResponse{
		Session:                  session,
		Rows:                     rows,
		Position:                 position,
		TotalRows:                col.total,
		FailureIndex:             col.failuresAtPosition,
		FailureTotal:             col.failures,
		LifetimeFailures:         session.Failures,
		EarlierFailuresNotStored: session.Failures > col.failures,
		HasBefore:                from > 0,
		HasAfter:                 from+len(page) < col.total,
	})
}

// timelineEntry is one row of the merged timeline: a stored row or event
// (ref), or a synthetic session boundary (kind and at).
type timelineEntry struct {
	ref  storage.WorkSessionRowRef
	kind string
	at   time.Time
	// precedes marks an open event from before the session (release QA F2).
	precedes bool
}

func (e timelineEntry) failed() bool { return e.kind == "" && e.ref.Counted && e.ref.Failed }

// timelineCollector finds the positioned row of a timeline that is fed to it
// one entry at a time, and keeps only the page around it.
type timelineCollector struct {
	before, after int
	// What the position is.
	mode      string // "first_failure", "failure", "row", "end"
	failureNo int
	rowID     string

	total, failures    int
	failuresAtPosition int
	position           int // -1 until found

	// ring holds the last before+1 entries seen: the rows before a position
	// found later, and the page of "end".
	ring []timelineEntry
	// head holds the first after+1 entries: the page when the position falls
	// back to the first row (first_failure with no failure stored).
	head []timelineEntry
	// page is the page once the position is known; pageFrom is the index of
	// its first entry.
	page     []timelineEntry
	pageFrom int
	wantMore int
}

func newTimelineCollector(at string, before, after int) (*timelineCollector, bool) {
	c := &timelineCollector{before: before, after: after, position: -1}
	switch {
	case at == "" || at == "first_failure":
		c.mode = "first_failure"
	case at == "end":
		c.mode = "end"
	case strings.HasPrefix(at, "failure:"):
		n, err := strconv.Atoi(strings.TrimPrefix(at, "failure:"))
		if err != nil || n < 1 {
			return nil, false
		}
		c.mode, c.failureNo = "failure", n
	case strings.HasPrefix(at, "row:") && len(at) > len("row:"):
		c.mode, c.rowID = "row", strings.TrimPrefix(at, "row:")
	default:
		return nil, false
	}
	return c, true
}

func (c *timelineCollector) add(e timelineEntry) {
	index := c.total
	c.total++
	if e.failed() {
		c.failures++
	}
	// Entries handed to the collector may point into a database page; what is
	// kept is copied.
	keep := func() timelineEntry {
		if e.kind == "" {
			e.ref.ActivityKey = append([]byte(nil), e.ref.ActivityKey...)
		}
		return e
	}

	if c.position >= 0 {
		if c.wantMore > 0 {
			c.page = append(c.page, keep())
			c.wantMore--
		}
		return
	}

	hit := false
	switch c.mode {
	case "first_failure":
		hit = e.failed()
	case "failure":
		hit = e.failed() && c.failures == c.failureNo
	case "row":
		hit = e.kind == "" && activityKeyID(e.ref.ActivityKey) == c.rowID
	}
	if hit {
		c.position = index
		c.failuresAtPosition = c.failures
		c.pageFrom = index - len(c.ring)
		c.page = append(append([]timelineEntry(nil), c.ring...), keep())
		c.wantMore = c.after
		c.ring, c.head = nil, nil
		return
	}

	if len(c.head) < c.after+1 {
		c.head = append(c.head, keep())
	}
	// The ring keeps the rows before a position found later. For "end" the
	// position is the last row itself, so it keeps one more.
	ringCap := c.before
	if c.mode == "end" {
		ringCap = c.before + 1
	}
	if ringCap > 0 {
		if len(c.ring) == ringCap {
			copy(c.ring, c.ring[1:])
			c.ring = c.ring[:ringCap-1]
		}
		c.ring = append(c.ring, keep())
	}
}

// finish returns the page, the index of its first row and the position. ok is
// false when the requested failure or row is not in the timeline.
func (c *timelineCollector) finish() (page []timelineEntry, from, position int, ok bool) {
	if c.position >= 0 {
		return c.page, c.pageFrom, c.position, true
	}
	switch c.mode {
	case "end":
		if c.total == 0 {
			return nil, 0, 0, true
		}
		// The ring holds the last before+1 rows; the last of them is the end.
		c.failuresAtPosition = c.failures
		return c.ring, c.total - len(c.ring), c.total - 1, true
	case "first_failure":
		// No failure is stored: the view opens at the earliest stored row.
		return c.head, 0, 0, true
	default:
		return nil, 0, 0, false
	}
}
