package replaycorpus

import (
	"sort"
	"time"
)

// ReplaySession is one unit of real recorded work: everything that shares a
// work_session_id (spec 082 — one client, one project, across reconnects).
// That, not the transport session, is the unit US1 scores, because a transport
// session is regenerated on every reconnect and would split one piece of work
// into several.
type ReplaySession struct {
	WorkSessionID string `json:"work_session_id"`

	// Calls holds the TOP-LEVEL calls only. Code-execution sub-calls hang off
	// their parent's SubCalls, so summing over Calls cannot double-count them
	// and AllCalls is the single way to see every record exactly once.
	Calls []*ReplayCall `json:"calls"`

	// Usability is the union of the calls' flags, computed once at load. It is
	// a union because a report quotes the SESSION total: one truncated call
	// makes that total suspect, so the session has to say so.
	Usability Flags `json:"usability,omitempty"`

	CallCount    int `json:"call_count"`
	SubCallCount int `json:"sub_call_count,omitempty"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Span is the wall-clock extent of the session, first record to last.
func (s *ReplaySession) Span() time.Duration { return s.LastSeen.Sub(s.FirstSeen) }

// AllCalls returns every call in the session — parents followed by their
// sub-calls — each exactly once. It is the accessor anything that sums or
// counts should use: walking Calls alone misses sandbox sub-calls, and walking
// a flattened list built by hand is how they come to be counted twice.
func (s *ReplaySession) AllCalls() []*ReplayCall {
	out := make([]*ReplayCall, 0, s.CallCount+s.SubCallCount)
	for _, call := range s.Calls {
		out = append(out, call)
		out = append(out, call.SubCalls...)
	}
	return out
}

// group assembles decoded calls into sessions.
//
// The join is the delicate part. A code-execution sub-call is recorded with the
// issuing call's request_id in its parent_id, and — being issued inside a
// sandbox rather than by the client — it may carry no work_session_id of its
// own. So the pass has to run in two stages: index every call by request_id
// first, then attach children, because the export is not guaranteed to present
// a parent before its children. A child that finds its parent is attributed to
// the PARENT's session and appears only under it; a child that does not is kept
// at top level and counted as orphaned, since dropping it would understate the
// workload and silently promoting it would misattribute it.
func group(calls []*ReplayCall, sessions map[string]string, rep *ExclusionReport) []*ReplaySession {
	byRequestID := make(map[string]*ReplayCall, len(calls))
	for _, call := range calls {
		if call.RequestID == "" {
			continue
		}
		// First wins: a duplicate request_id is not expected, and preferring
		// the first keeps the result a function of input order alone.
		if _, exists := byRequestID[call.RequestID]; !exists {
			byRequestID[call.RequestID] = call
		}
	}

	topLevel := make([]*ReplayCall, 0, len(calls))
	owner := make(map[*ReplayCall]string, len(calls))
	orphaned := make(map[*ReplayCall]bool)
	for _, call := range calls {
		if call.ParentID != "" {
			if parent, ok := byRequestID[call.ParentID]; ok && parent != call {
				parent.SubCalls = append(parent.SubCalls, call)
				continue
			}
			// The parent fell outside the exported window. Keep the call at
			// top level if it can stand alone; remember that it is an orphan
			// either way, so a drop below is attributed to the export window
			// rather than to the record simply having no session.
			rep.OrphanedSubCalls++
			orphaned[call] = true
		}
		topLevel = append(topLevel, call)
		owner[call] = sessions[call.ID]
	}

	grouped := make(map[string]*ReplaySession)
	var order []string
	for _, call := range topLevel {
		workSessionID := owner[call]
		if workSessionID == "" {
			// No work session, and no parent to inherit one from: the record
			// belongs to no unit of work. Reported, not folded in.
			//
			// An orphan lands here whenever the export window cut its parent,
			// which is the common case for sandbox sub-calls: they inherit
			// their session from the parent, so losing one loses the other.
			// It gets its own reason because it is the actionable one — a
			// wider re-export brings the record back.
			if orphaned[call] {
				rep.drop(ReasonOrphanedSubCall)
			} else {
				rep.drop(ReasonUnattributed)
			}
			continue
		}
		session, ok := grouped[workSessionID]
		if !ok {
			session = &ReplaySession{WorkSessionID: workSessionID}
			grouped[workSessionID] = session
			order = append(order, workSessionID)
		}
		session.Calls = append(session.Calls, call)
	}

	out := make([]*ReplaySession, 0, len(order))
	for _, workSessionID := range order {
		session := grouped[workSessionID]
		finalize(session)
		out = append(out, session)
	}
	// Deterministic ordering: a replay report must be byte-reproducible
	// (SC-002), so the session list cannot depend on map iteration. Earliest
	// first, id as the tie-break.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FirstSeen.Equal(out[j].FirstSeen) {
			return out[i].FirstSeen.Before(out[j].FirstSeen)
		}
		return out[i].WorkSessionID < out[j].WorkSessionID
	})
	return out
}

// finalize computes the derived counts, the span and the union of flags for one
// assembled session.
func finalize(s *ReplaySession) {
	s.CallCount = len(s.Calls)
	for _, call := range s.Calls {
		s.SubCallCount += len(call.SubCalls)
	}
	for _, call := range s.AllCalls() {
		s.Usability.merge(call.Flags)
		if s.FirstSeen.IsZero() || call.Timestamp.Before(s.FirstSeen) {
			s.FirstSeen = call.Timestamp
		}
		if call.Timestamp.After(s.LastSeen) {
			s.LastSeen = call.Timestamp
		}
	}
}
