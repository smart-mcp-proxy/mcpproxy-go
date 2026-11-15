package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"go.etcd.io/bbolt"
)

// Spec 124: durable work-session summaries.
//
// A work session is one agent's run of work (Spec 082). Its activity rows live
// in the activity bucket, ordered by time across ALL sessions, so answering
// "which sessions failed last night, and what led to the failure" from the
// rows alone means scanning the whole log. Three buckets make it an indexed
// read instead, all written in the transaction that writes the row, so a
// summary can never disagree with its rows after a crash:
//
//   - work_sessions:             <work-session id> -> WorkSessionSummary (JSON)
//   - work_session_rows:         <work-session id> 0x00 <activity key> -> flags + server
//   - upstream_events_by_server: <server> 0x00 <activity key> -> owner user id

const (
	// WorkSessionsBucket holds one summary per work session.
	WorkSessionsBucket = "work_sessions"
	// WorkSessionRowsBucket indexes a session's activity rows in time order.
	WorkSessionRowsBucket = "work_session_rows"
	// UpstreamEventsByServerBucket indexes upstream event rows by server, for
	// the timeline join.
	UpstreamEventsByServerBucket = "upstream_events_by_server"

	// workSessionBackfillDoneKey marks the one-time backfill of rows written
	// before this feature (stored in MetaBucket).
	workSessionBackfillDoneKey = "work_session_backfill_done"

	// MaxWorkSessionReasonLen bounds the short reason of the last failure
	// (FR-012), in characters.
	MaxWorkSessionReasonLen = 200

	// maxSummaryConnections bounds the connection ids a summary lists; the
	// count keeps growing past it.
	maxSummaryConnections = 32

	workSessionKeySep = byte(0)
)

// Index flag bits of one work_session_rows entry. The role of a row is fixed
// when the row is indexed and never re-derived, so a rebuild after pruning or
// a backfill pairing decision always agrees with what was counted.
const (
	rowFlagCounted byte = 1 << iota
	rowFlagFailed
	rowFlagBlock
	rowFlagBodiesRemoved
)

// WorkSessionPartition is the part of a session that touched one server. The
// empty server name holds MCPProxy's own tools. Keeping the facts per server
// lets a scoped caller be shown exactly the part in its reach without reading
// rows (FR-048, FR-049).
type WorkSessionPartition struct {
	// Lifetime facts: never lowered by pruning (FR-013).
	Attempts int `json:"attempts,omitempty"`
	Failures int `json:"failures,omitempty"`
	Blocks   int `json:"blocks,omitempty"`

	// Stored facts: failures and blocks whose rows are still in the log. They
	// drive failure navigation and are lowered when rows are removed.
	StoredFailures int `json:"stored_failures,omitempty"`
	StoredBlocks   int `json:"stored_blocks,omitempty"`

	FirstActivityAt time.Time `json:"first_activity_at,omitzero"`
	LastActivityAt  time.Time `json:"last_activity_at,omitzero"`
	FirstFailureAt  time.Time `json:"first_failure_at,omitzero"`
	LastFailureAt   time.Time `json:"last_failure_at,omitzero"`

	// Last failure facts (FR-011). The reason comes from the row's own error
	// message, never from argument or response bodies (FR-012).
	LastErrorClass string `json:"last_error_class,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	LastTool       string `json:"last_tool,omitempty"`
	LastReason     string `json:"last_reason,omitempty"`
	// LastFailureBlock says whether the last failure was a policy block, so a
	// list that excludes blocks can tell.
	LastFailureBlock bool `json:"last_failure_block,omitempty"`
}

// WorkSessionSummary is the durable record of one work session.
type WorkSessionSummary struct {
	ID string `json:"id"`

	// Ownership (FR-017): decided the way activity rows decide it, by token
	// name and prefix, or by the owning user in the server edition. The prefix
	// and the user id are internal and never leave through an API shape.
	TokenName   string `json:"token_name,omitempty"`
	TokenPrefix string `json:"token_prefix,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	ClientID    string `json:"client_id,omitempty"`
	// Ownerless is set when rows of more than one credential share the id,
	// which only rows written before Spec 124 can do. Such a session belongs
	// to no credential and is visible to administrators only (FR-014).
	Ownerless bool `json:"ownerless,omitempty"`

	ClientName    string `json:"client_name,omitempty"`
	ProjectName   string `json:"project_name,omitempty"`
	Surface       string `json:"surface,omitempty"`
	RunName       string `json:"run_name,omitempty"`
	Profile       string `json:"profile,omitempty"`
	ProfileSource string `json:"profile_source,omitempty"`

	StartedAt      time.Time `json:"started_at,omitzero"`
	LastActivityAt time.Time `json:"last_activity_at,omitzero"`
	// EndedAt is set once the idle window has closed (FR-015) and cleared if a
	// later row arrives.
	EndedAt time.Time `json:"ended_at,omitzero"`

	// ConnectionIDs are the distinct MCP connections seen, up to a bound;
	// ConnectionCount is the true number.
	ConnectionIDs   []string `json:"connection_ids,omitempty"`
	ConnectionCount int      `json:"connection_count,omitempty"`

	// TruncatedStartAt is set when rows of the session were removed by
	// retention: the time of the earliest row removed last.
	TruncatedStartAt time.Time `json:"truncated_start_at,omitzero"`
	// Rebuilt is set when the summary was rebuilt from the remaining rows
	// (FR-013); its lifetime facts then cover only those rows.
	Rebuilt bool `json:"rebuilt,omitempty"`

	Servers map[string]*WorkSessionPartition `json:"servers,omitempty"`
}

// Totals sums the partitions the allow function admits (nil admits all).
func (s *WorkSessionSummary) Totals(allow func(server string) bool) WorkSessionPartition {
	var t WorkSessionPartition
	for name, p := range s.Servers {
		if allow != nil && !allow(name) {
			continue
		}
		t.Attempts += p.Attempts
		t.Failures += p.Failures
		t.Blocks += p.Blocks
		t.StoredFailures += p.StoredFailures
		t.StoredBlocks += p.StoredBlocks
		if t.FirstActivityAt.IsZero() || (!p.FirstActivityAt.IsZero() && p.FirstActivityAt.Before(t.FirstActivityAt)) {
			t.FirstActivityAt = p.FirstActivityAt
		}
		if p.LastActivityAt.After(t.LastActivityAt) {
			t.LastActivityAt = p.LastActivityAt
		}
		if !p.FirstFailureAt.IsZero() && (t.FirstFailureAt.IsZero() || p.FirstFailureAt.Before(t.FirstFailureAt)) {
			t.FirstFailureAt = p.FirstFailureAt
		}
		if p.LastFailureAt.After(t.LastFailureAt) {
			t.LastFailureAt = p.LastFailureAt
			t.LastErrorClass, t.LastErrorCode = p.LastErrorClass, p.LastErrorCode
			t.LastTool, t.LastReason = p.LastTool, p.LastReason
			t.LastFailureBlock = p.LastFailureBlock
		}
	}
	return t
}

// LastFailureServer names the server of the most recent failure among the
// admitted partitions, and whether there is one.
func (s *WorkSessionSummary) LastFailureServer(allow func(server string) bool) (string, bool) {
	var best time.Time
	name, ok := "", false
	for n, p := range s.Servers {
		if allow != nil && !allow(n) {
			continue
		}
		if p.LastFailureAt.After(best) {
			best, name, ok = p.LastFailureAt, n, true
		}
	}
	return name, ok
}

func roleFlags(role AttemptRole, bodiesRemoved bool) byte {
	var f byte
	if role.Counted {
		f |= rowFlagCounted
	}
	if role.Failed {
		f |= rowFlagFailed
	}
	if role.Block {
		f |= rowFlagBlock
	}
	if bodiesRemoved {
		f |= rowFlagBodiesRemoved
	}
	return f
}

func flagsRole(f byte) AttemptRole {
	return AttemptRole{Counted: f&rowFlagCounted != 0, Failed: f&rowFlagFailed != 0, Block: f&rowFlagBlock != 0}
}

func workSessionRowKey(workSessionID string, activityKey []byte) []byte {
	k := make([]byte, 0, len(workSessionID)+1+len(activityKey))
	k = append(k, workSessionID...)
	k = append(k, workSessionKeySep)
	return append(k, activityKey...)
}

func workSessionRowPrefix(workSessionID string) []byte {
	return append([]byte(workSessionID), workSessionKeySep)
}

// splitIndexKey splits "<left> 0x00 <activity key>" at its first separator.
func splitIndexKey(k []byte) (left string, activityKey []byte, ok bool) {
	i := bytes.IndexByte(k, workSessionKeySep)
	if i < 0 {
		return "", nil, false
	}
	return string(k[:i]), k[i+1:], true
}

func indexValue(flags byte, server string) []byte {
	v := make([]byte, 0, 1+len(server))
	v = append(v, flags)
	return append(v, server...)
}

func parseIndexValue(v []byte) (flags byte, server string) {
	if len(v) == 0 {
		return 0, ""
	}
	return v[0], string(v[1:])
}

// activityKeyTime reads the timestamp out of an activity key.
func activityKeyTime(key []byte) time.Time {
	if len(key) < 20 {
		return time.Time{}
	}
	var n int64
	for _, c := range key[:20] {
		if c < '0' || c > '9' {
			return time.Time{}
		}
		n = n*10 + int64(c-'0')
	}
	return time.Unix(0, n).UTC()
}

func loadSummary(b *bbolt.Bucket, id string) (*WorkSessionSummary, error) {
	data := b.Get([]byte(id))
	if data == nil {
		return nil, nil
	}
	var s WorkSessionSummary
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("failed to unmarshal work session summary %s: %w", id, err)
	}
	return &s, nil
}

func storeSummary(b *bbolt.Bucket, s *WorkSessionSummary) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal work session summary: %w", err)
	}
	return b.Put([]byte(s.ID), data)
}

// truncateRunes cuts s to at most n characters.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	out := make([]rune, 0, n)
	for _, r := range s {
		if len(out) == n {
			break
		}
		out = append(out, r)
	}
	return string(out)
}

// failureReason is the short reason of a failed row (FR-012): the row's own
// error message, or for a policy decision its stated reason. Never a body.
func failureReason(r *ActivityRecord) string {
	reason := r.ErrorMessage
	if reason == "" {
		if v, ok := r.Metadata["reason"].(string); ok {
			reason = v
		}
	}
	return truncateRunes(reason, MaxWorkSessionReasonLen)
}

func failureClass(r *ActivityRecord, role AttemptRole) string {
	if r.ErrorClass != "" {
		return r.ErrorClass
	}
	if role.Block {
		return ErrorClassProxyPolicy
	}
	return ""
}

func failureCode(r *ActivityRecord) string {
	if v, ok := r.Metadata["error_code"].(string); ok {
		return v
	}
	return ""
}

// applyRow folds one row into the summary. role is the row's counting role as
// stored in (or about to be stored in) the index.
func (s *WorkSessionSummary) applyRow(r *ActivityRecord, role AttemptRole) {
	ts := r.Timestamp

	// Ownership. The first attributed row names the owner. A later row of
	// another credential makes the session ownerless; only pre-124 rows can do
	// that, since the credential is part of what derives the id now.
	rowHasIdentity := r.TokenName != "" || r.TokenPrefix != "" || r.UserID != ""
	summaryHasIdentity := s.TokenName != "" || s.TokenPrefix != "" || s.UserID != ""
	switch {
	case !rowHasIdentity:
	case !summaryHasIdentity && !s.Ownerless:
		s.TokenName, s.TokenPrefix, s.UserID = r.TokenName, r.TokenPrefix, r.UserID
	case r.TokenName != s.TokenName || (r.TokenPrefix != "" && s.TokenPrefix != "" && r.TokenPrefix != s.TokenPrefix) ||
		(r.UserID != "" && s.UserID != "" && r.UserID != s.UserID):
		s.Ownerless = true
	default:
		if s.TokenPrefix == "" {
			s.TokenPrefix = r.TokenPrefix
		}
		if s.UserID == "" {
			s.UserID = r.UserID
		}
	}

	if r.ClientID != "" {
		s.ClientID = r.ClientID
	}
	if r.ClientName != "" {
		s.ClientName = r.ClientName
	}
	if s.Surface == "" && r.Source != "" && r.Source != ActivitySourceInternal {
		s.Surface = string(r.Source)
	}
	if r.RunName != "" {
		s.RunName = r.RunName
	}
	if r.ProjectName != "" {
		s.ProjectName = r.ProjectName
	}
	if r.ProfileSource != "" && !ts.Before(s.LastActivityAt) {
		s.Profile, s.ProfileSource = r.Profile, r.ProfileSource
	}

	if s.StartedAt.IsZero() || ts.Before(s.StartedAt) {
		s.StartedAt = ts
	}
	if ts.After(s.LastActivityAt) {
		s.LastActivityAt = ts
	}
	if !s.EndedAt.IsZero() && ts.After(s.EndedAt) {
		s.EndedAt = time.Time{} // the session is active again (FR-015)
	}

	if r.SessionID != "" {
		known := false
		for _, id := range s.ConnectionIDs {
			if id == r.SessionID {
				known = true
				break
			}
		}
		if !known {
			if len(s.ConnectionIDs) < maxSummaryConnections {
				s.ConnectionIDs = append(s.ConnectionIDs, r.SessionID)
				s.ConnectionCount = len(s.ConnectionIDs)
			} else {
				// Past the bound the ids are not kept; the count is a floor.
				s.ConnectionCount = maxSummaryConnections + 1
			}
		}
	}

	if s.Servers == nil {
		s.Servers = make(map[string]*WorkSessionPartition)
	}
	p := s.Servers[r.ServerName]
	if p == nil {
		p = &WorkSessionPartition{}
		s.Servers[r.ServerName] = p
	}
	if p.FirstActivityAt.IsZero() || ts.Before(p.FirstActivityAt) {
		p.FirstActivityAt = ts
	}
	if ts.After(p.LastActivityAt) {
		p.LastActivityAt = ts
	}
	if !role.Counted {
		return
	}
	p.Attempts++
	if !role.Failed {
		return
	}
	p.Failures++
	p.StoredFailures++
	if role.Block {
		p.Blocks++
		p.StoredBlocks++
	}
	if p.FirstFailureAt.IsZero() || ts.Before(p.FirstFailureAt) {
		p.FirstFailureAt = ts
	}
	if !ts.Before(p.LastFailureAt) {
		p.LastFailureAt = ts
		p.LastErrorClass = failureClass(r, role)
		p.LastErrorCode = failureCode(r)
		p.LastTool = r.ToolName
		p.LastReason = failureReason(r)
		p.LastFailureBlock = role.Block
	}
}

// forgetStoredRow lowers the STORED counters for a row that retention removed.
// Lifetime facts are untouched (FR-013).
func (s *WorkSessionSummary) forgetStoredRow(flags byte, server string, at time.Time) {
	role := flagsRole(flags)
	if p := s.Servers[server]; p != nil && role.Counted && role.Failed {
		if p.StoredFailures > 0 {
			p.StoredFailures--
		}
		if role.Block && p.StoredBlocks > 0 {
			p.StoredBlocks--
		}
	}
	if at.After(s.TruncatedStartAt) {
		s.TruncatedStartAt = at
	}
}

// indexActivityTx writes the index entry of a row that belongs to a work
// session and folds the row into the summary. It is a no-op for a row that is
// already indexed, so re-saving a row never counts it twice.
func indexActivityTx(tx *bbolt.Tx, r *ActivityRecord, key []byte, role AttemptRole) error {
	if r.WorkSessionID == "" {
		return nil
	}
	rows, err := tx.CreateBucketIfNotExists([]byte(WorkSessionRowsBucket))
	if err != nil {
		return fmt.Errorf("failed to create work session rows bucket: %w", err)
	}
	ik := workSessionRowKey(r.WorkSessionID, key)
	if rows.Get(ik) != nil {
		return nil
	}
	if err := rows.Put(ik, indexValue(roleFlags(role, r.BodiesRemoved), r.ServerName)); err != nil {
		return fmt.Errorf("failed to index activity row: %w", err)
	}
	sums, err := tx.CreateBucketIfNotExists([]byte(WorkSessionsBucket))
	if err != nil {
		return fmt.Errorf("failed to create work sessions bucket: %w", err)
	}
	s, err := loadSummary(sums, r.WorkSessionID)
	if err != nil {
		return err
	}
	if s == nil {
		s = &WorkSessionSummary{ID: r.WorkSessionID}
	}
	s.applyRow(r, role)
	return storeSummary(sums, s)
}

// indexUpstreamEventTx indexes an upstream event row by its server.
func indexUpstreamEventTx(tx *bbolt.Tx, r *ActivityRecord, key []byte) error {
	if r.Type != ActivityTypeUpstreamEvent || r.ServerName == "" {
		return nil
	}
	b, err := tx.CreateBucketIfNotExists([]byte(UpstreamEventsByServerBucket))
	if err != nil {
		return fmt.Errorf("failed to create upstream events bucket: %w", err)
	}
	return b.Put(workSessionRowKey(r.ServerName, key), []byte(r.UserID))
}

// GetWorkSessionSummary returns one summary, or nil when there is none.
func (m *Manager) GetWorkSessionSummary(id string) (*WorkSessionSummary, error) {
	var out *WorkSessionSummary
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionsBucket))
		if b == nil {
			return nil
		}
		s, err := loadSummary(b, id)
		out = s
		return err
	})
	return out, err
}

// ListWorkSessionSummaries returns every summary. The caller projects, filters,
// sorts and pages: at the scale the retention window allows this is one small
// bucket read, and it never touches the activity log (SC-006).
func (m *Manager) ListWorkSessionSummaries() ([]*WorkSessionSummary, error) {
	var out []*WorkSessionSummary
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var s WorkSessionSummary
			if err := json.Unmarshal(v, &s); err != nil {
				return nil // a corrupt summary is skipped, not fatal to the list
			}
			out = append(out, &s)
			return nil
		})
	})
	return out, err
}

// WorkSessionRowRef points at one row of a session timeline without loading it.
type WorkSessionRowRef struct {
	ActivityKey []byte
	Timestamp   time.Time
	Server      string
	Counted     bool
	Failed      bool
	Block       bool
	// Event is true for an upstream event row joined into the timeline.
	Event bool
	// Owner is the owning user of an upstream credential event ("" = shared).
	Owner string
}

// WorkSessionRowRefs returns the index entries of a session in time order.
// Only keys and their small values are read; no activity row is loaded.
func (m *Manager) WorkSessionRowRefs(workSessionID string) ([]WorkSessionRowRef, error) {
	var refs []WorkSessionRowRef
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionRowsBucket))
		if b == nil {
			return nil
		}
		prefix := workSessionRowPrefix(workSessionID)
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			ak := append([]byte(nil), k[len(prefix):]...)
			flags, server := parseIndexValue(v)
			role := flagsRole(flags)
			refs = append(refs, WorkSessionRowRef{
				ActivityKey: ak, Timestamp: activityKeyTime(ak), Server: server,
				Counted: role.Counted, Failed: role.Failed, Block: role.Block,
			})
		}
		return nil
	})
	return refs, err
}

// UpstreamEventRefs returns the upstream event rows of one server whose time
// is within [from, to], in time order. A zero `to` means "until now".
func (m *Manager) UpstreamEventRefs(server string, from, to time.Time) ([]WorkSessionRowRef, error) {
	var refs []WorkSessionRowRef
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(UpstreamEventsByServerBucket))
		if b == nil {
			return nil
		}
		prefix := workSessionRowPrefix(server)
		start := workSessionRowKey(server, activityKey(from, ""))
		c := b.Cursor()
		for k, v := c.Seek(start); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			ak := append([]byte(nil), k[len(prefix):]...)
			ts := activityKeyTime(ak)
			if !to.IsZero() && ts.After(to) {
				break
			}
			refs = append(refs, WorkSessionRowRef{ActivityKey: ak, Timestamp: ts, Server: server, Event: true, Owner: string(v)})
		}
		return nil
	})
	return refs, err
}

// GetActivitiesByKeys loads the rows at the given activity keys, in the order
// given. A key whose row is gone yields nil at that position.
func (m *Manager) GetActivitiesByKeys(keys [][]byte) ([]*ActivityRecord, error) {
	out := make([]*ActivityRecord, len(keys))
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(ActivityRecordsBucket))
		if b == nil {
			return nil
		}
		for i, k := range keys {
			v := b.Get(k)
			if v == nil {
				continue
			}
			rec := &ActivityRecord{}
			if err := rec.UnmarshalBinary(v); err != nil {
				return fmt.Errorf("failed to unmarshal activity record: %w", err)
			}
			out[i] = rec
		}
		return nil
	})
	return out, err
}

// MarkWorkSessionsEnded records the end of every summary whose last activity
// is at or before idleBefore and that has no end yet (FR-015). The end of a
// session is its last activity. Returns how many were closed.
func (m *Manager) MarkWorkSessionsEnded(idleBefore time.Time) (int, error) {
	closed := 0
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionsBucket))
		if b == nil {
			return nil
		}
		var toStore []*WorkSessionSummary
		if err := b.ForEach(func(_, v []byte) error {
			var s WorkSessionSummary
			if err := json.Unmarshal(v, &s); err != nil {
				return nil
			}
			if s.EndedAt.IsZero() && !s.LastActivityAt.IsZero() && !s.LastActivityAt.After(idleBefore) {
				s.EndedAt = s.LastActivityAt
				toStore = append(toStore, &s)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, s := range toStore {
			if err := storeSummary(b, s); err != nil {
				return err
			}
			closed++
		}
		return nil
	})
	return closed, err
}

// workSessionIDsInIndex returns the distinct work-session ids that have index
// entries, with one seek per session.
func workSessionIDsInIndex(tx *bbolt.Tx) []string {
	b := tx.Bucket([]byte(WorkSessionRowsBucket))
	if b == nil {
		return nil
	}
	var ids []string
	c := b.Cursor()
	for k, _ := c.First(); k != nil; {
		id, _, ok := splitIndexKey(k)
		if !ok {
			k, _ = c.Next()
			continue
		}
		ids = append(ids, id)
		// Jump past this session: 0x01 sorts right after the separator.
		k, _ = c.Seek(append([]byte(id), workSessionKeySep+1))
	}
	return ids
}

// RepairWorkSessionSummaries rebuilds every summary that is missing while its
// index entries remain (FR-013). Ownership, attribution and the last failure
// come from the retained rows; the arithmetic uses the role stored in each
// index entry, never a new classification. An existing summary is left as it
// is. Returns how many were rebuilt.
func (m *Manager) RepairWorkSessionSummaries() (int, error) {
	var missing []string
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		sums := tx.Bucket([]byte(WorkSessionsBucket))
		for _, id := range workSessionIDsInIndex(tx) {
			if sums == nil || sums.Get([]byte(id)) == nil {
				missing = append(missing, id)
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}

	rebuilt := 0
	for _, id := range missing {
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			sums, err := tx.CreateBucketIfNotExists([]byte(WorkSessionsBucket))
			if err != nil {
				return err
			}
			if sums.Get([]byte(id)) != nil {
				return nil // written meanwhile by a live row
			}
			rows := tx.Bucket([]byte(WorkSessionRowsBucket))
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if rows == nil || acts == nil {
				return nil
			}
			s := &WorkSessionSummary{ID: id, Rebuilt: true}
			prefix := workSessionRowPrefix(id)
			c := rows.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				data := acts.Get(k[len(prefix):])
				if data == nil {
					continue
				}
				rec := &ActivityRecord{}
				if err := rec.UnmarshalBinary(data); err != nil {
					continue
				}
				flags, _ := parseIndexValue(v)
				s.applyRow(rec, flagsRole(flags))
			}
			if s.StartedAt.IsZero() {
				return nil // every row is gone; nothing to summarise
			}
			s.EndedAt = s.LastActivityAt
			rebuilt++
			return storeSummary(sums, s)
		})
		if err != nil {
			return rebuilt, err
		}
	}
	return rebuilt, nil
}

// CloseOpenWorkSessionsAtStart gives every summary with no end its last
// activity as its end. Work-session ids do not survive a restart, so no
// session that was open when the core stopped can continue.
func (m *Manager) CloseOpenWorkSessionsAtStart() (int, error) {
	return m.MarkWorkSessionsEnded(time.Now().UTC().Add(24 * time.Hour))
}

// sortRefs orders timeline refs by time, then by key for a stable order.
func sortRefs(refs []WorkSessionRowRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if !refs[i].Timestamp.Equal(refs[j].Timestamp) {
			return refs[i].Timestamp.Before(refs[j].Timestamp)
		}
		return bytes.Compare(refs[i].ActivityKey, refs[j].ActivityKey) < 0
	})
}

// MergeTimelineRefs merges a session's row refs with upstream event refs into
// one time-ordered list.
func MergeTimelineRefs(rows, events []WorkSessionRowRef) []WorkSessionRowRef {
	out := make([]WorkSessionRowRef, 0, len(rows)+len(events))
	out = append(out, rows...)
	out = append(out, events...)
	sortRefs(out)
	return out
}
