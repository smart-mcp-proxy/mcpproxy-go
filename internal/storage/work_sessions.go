package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

	// maxSummaryConnections bounds the distinct connections a summary can
	// tell apart. Each is kept as a short hash, so the count is exact up to
	// this many connections and stops there.
	maxSummaryConnections = 4096

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

	// Last failure facts (FR-011). The reason is fixed wording for the error
	// class, never free text from the row or its bodies (FR-012).
	LastErrorClass string `json:"last_error_class,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	LastTool       string `json:"last_tool,omitempty"`
	LastReason     string `json:"last_reason,omitempty"`
	// NetworkFailure: some call to this server in the session failed because
	// the server could not be reached (class network), whatever the session's
	// last failure on it was. It lets the timeline show the lost connection
	// from before the session (release QA F2).
	NetworkFailure bool `json:"network_failure,omitempty"`
	// LastFailureBlock says whether the last failure was a policy block, so a
	// list that excludes blocks can tell.
	LastFailureBlock bool `json:"last_failure_block,omitempty"`

	// ProjectName is the project of the latest row of this server that
	// carried one. A caller whose reach is narrowed reads the project from
	// the servers in reach only, like every other fact.
	ProjectName string `json:"project_name,omitempty"`
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

	// ConnectionKeys are short hashes of the distinct MCP connections seen,
	// and ConnectionCount their number. The count is exact up to
	// maxSummaryConnections.
	ConnectionKeys  []string `json:"connection_keys,omitempty"`
	ConnectionCount int      `json:"connection_count,omitempty"`

	// TruncatedStartAt is set when rows of the session were removed by
	// retention: the time of the earliest row removed last.
	TruncatedStartAt time.Time `json:"truncated_start_at,omitzero"`
	// UnindexedBefore is set when the removal of an expired summary was
	// cancelled by a new row: the index entries already dropped are not put
	// back, so rows of this session older than this time are stored without
	// an index entry. Retention treats them as rows outside the window.
	UnindexedBefore time.Time `json:"unindexed_before,omitzero"`
	// Rebuilt is set when the summary was rebuilt from the remaining rows
	// (FR-013); its lifetime facts then cover only those rows.
	Rebuilt bool `json:"rebuilt,omitempty"`
	// Removal is set while retention is removing the session in batches
	// ("summary": index and summary only; "session": the rows too). A
	// summary that carries it is not shown, and the removal is finished at
	// the next start if a crash interrupted it.
	Removal string `json:"removal,omitempty"`

	Servers map[string]*WorkSessionPartition `json:"servers,omitempty"`

	// Unattributed records that a request row with no credential was folded
	// in while the session had no owner yet, so that a later attributed row
	// makes the session ownerless instead of claiming it.
	Unattributed bool `json:"unattributed,omitempty"`
	// NameOnly records that a request row with a token name but no token
	// prefix (and no user) was folded in: a row from before the prefix was
	// recorded. A name does not prove which generation of the token wrote
	// it, so such a row and a row with a prefix never share an owner.
	NameOnly bool `json:"name_only,omitempty"`
	// NoUser records that a request row with a credential but no user id
	// was folded in.
	NoUser bool `json:"no_user,omitempty"`
}

// ProjectNameIn returns the session's project as read from the servers that
// allow admits (nil admits all, and then it is the session's own): the
// project of the admitted server with the latest activity that has one.
func (s *WorkSessionSummary) ProjectNameIn(allow func(string) bool) string {
	if allow == nil {
		return s.ProjectName
	}
	name, at, from := "", time.Time{}, ""
	for server, p := range s.Servers {
		if p.ProjectName == "" || !allow(server) {
			continue
		}
		if name == "" || p.LastActivityAt.After(at) || (p.LastActivityAt.Equal(at) && server < from) {
			name, at, from = p.ProjectName, p.LastActivityAt, server
		}
	}
	return name
}

// UnattributedRows reports whether the session holds a request row that
// carries no credential.
func (s *WorkSessionSummary) UnattributedRows() bool { return s.Unattributed }

// sortedServers returns the admitted server names in a fixed order, so that
// every reader breaks a tie between two servers the same way.
func (s *WorkSessionSummary) sortedServers(allow func(server string) bool) []string {
	names := make([]string, 0, len(s.Servers))
	for name := range s.Servers {
		if allow == nil || allow(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Totals sums the partitions the allow function admits (nil admits all).
func (s *WorkSessionSummary) Totals(allow func(server string) bool) WorkSessionPartition {
	t, _ := s.totals(allow)
	return t
}

// LastFailureServer names the server of the most recent failure among the
// admitted partitions, and whether there is one. It is the server whose last
// failure facts Totals returns.
func (s *WorkSessionSummary) LastFailureServer(allow func(server string) bool) (string, bool) {
	t, server := s.totals(allow)
	return server, !t.LastFailureAt.IsZero()
}

// totals sums the admitted partitions and names the server of the last
// failure. Two servers that failed at the same instant are ordered by name,
// here and nowhere else, so the facts and the server always agree.
func (s *WorkSessionSummary) totals(allow func(server string) bool) (WorkSessionPartition, string) {
	var t WorkSessionPartition
	lastServer := ""
	for _, name := range s.sortedServers(allow) {
		p := s.Servers[name]
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
			lastServer = name
		}
	}
	return t, lastServer
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

// failureReasonByClass is the fixed wording of a failure of each error class.
var failureReasonByClass = map[string]string{
	"network":            "The upstream server could not be reached.",
	"timeout":            "The call timed out.",
	"http":               "The upstream server answered with an HTTP error.",
	"jsonrpc":            "The upstream server answered with a protocol error.",
	"tool_error":         "The tool reported an error.",
	"session_terminated": "The upstream session was terminated.",
	"auth":               "The upstream server refused the credentials.",
	"proxy_policy":       "MCPProxy refused the call.",
	"proxy_internal":     "MCPProxy failed to handle the call.",
	"cancelled":          "The call was cancelled.",
}

// failureReason is the short reason of a failed row (FR-012). It is built
// only from closed vocabularies: the error class, the upstream HTTP status
// and, for a refusal, the block reason key. The row's free-text error message
// is deliberately NOT used: an upstream error can echo a credential from the
// request, and the activity doors mask that text per row with the sensitive
// data detector, which a summary cannot do. The full, masked message stays
// one click away on the row itself.
func failureReason(r *ActivityRecord, role AttemptRole) string {
	class := failureClass(r, role)
	reason := failureReasonByClass[class]
	if reason == "" {
		reason = "The call failed."
	}
	if class == ErrorClassProxyPolicy {
		if key := r.EffectiveBlockReason(); key != "" {
			reason = "MCPProxy refused the call (" + key + ")."
		} else if r.Status == ActivityStatusRejected {
			reason = "MCPProxy refused the call (limit reached)."
		}
	}
	if class == "http" && r.UpstreamHTTPStatus > 0 {
		reason = fmt.Sprintf("The upstream server answered with HTTP %d.", r.UpstreamHTTPStatus)
	}
	// An auth failure with an upstream status is credentials the server
	// refused; without one the call was never sent, because the server waits
	// for a login (logged out, parked for sign-in; release QA F1).
	if class == "auth" {
		if r.UpstreamHTTPStatus > 0 {
			reason = fmt.Sprintf("The upstream server refused the credentials (HTTP %d).", r.UpstreamHTTPStatus)
		} else {
			reason = "The upstream server needs a login."
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
	// The identity is read the way ActivityIdentityOwner.Owns reads it,
	// including the internal arguments of rows written before the
	// first-class fields existed; otherwise such a row of another credential
	// would be folded into this owner's session unnoticed.
	rowName, rowPrefix := r.EffectiveTokenName(), r.TokenPrefix
	if rowPrefix == "" {
		rowPrefix = extractAuthMetadataField(r, authArgTokenPrefix)
	}
	rowHasIdentity := rowName != "" || rowPrefix != "" || r.UserID != ""
	summaryHasIdentity := s.TokenName != "" || s.TokenPrefix != "" || s.UserID != ""
	// requestRow: a row that records what a caller did. Since Spec 124 every
	// such row of a credential's session carries the credential. One that
	// carries none while other rows of the session carry one (in either
	// order) was written by an unknown caller: only rows from before Spec 124
	// can do that, and the session then belongs to no credential.
	requestRow := r.Type == ActivityTypeToolCall || r.Type == ActivityTypeInternalToolCall ||
		r.Type == ActivityTypePolicyDecision || r.Type == ActivityTypePromptGet
	nameOnlyRow := requestRow && rowName != "" && rowPrefix == "" && r.UserID == ""
	if nameOnlyRow {
		s.NameOnly = true
	}
	if s.NameOnly && (rowPrefix != "" || s.TokenPrefix != "") {
		// A name-only row and a proven token generation in one session, in
		// either order: no agent token may claim the name-only row's facts.
		s.Ownerless = true
	}
	if requestRow && rowHasIdentity && r.UserID == "" {
		s.NoUser = true
	}
	if s.NoUser && (r.UserID != "" || s.UserID != "") {
		// A request row with no user and a row of a known user in one
		// session, in either order (rows from before the user was recorded
		// on every row): a user principal may not claim the row it cannot
		// be shown, so the session belongs to no one.
		s.Ownerless = true
	}
	switch {
	case !rowHasIdentity:
		if requestRow {
			if summaryHasIdentity {
				s.Ownerless = true
			}
			s.Unattributed = true
		}
	case !summaryHasIdentity && !s.Ownerless:
		if s.UnattributedRows() {
			s.Ownerless = true
			break
		}
		s.TokenName, s.TokenPrefix, s.UserID = rowName, rowPrefix, r.UserID
	case rowName != s.TokenName || (rowPrefix != "" && s.TokenPrefix != "" && rowPrefix != s.TokenPrefix) ||
		(r.UserID != "" && s.UserID != "" && r.UserID != s.UserID):
		s.Ownerless = true
	default:
		if s.TokenPrefix == "" {
			s.TokenPrefix = rowPrefix
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
	if r.Surface != "" {
		s.Surface = r.Surface
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

	if r.SessionID != "" && len(s.ConnectionKeys) < maxSummaryConnections {
		key := connectionKey(r.SessionID)
		known := false
		for _, k := range s.ConnectionKeys {
			if k == key {
				known = true
				break
			}
		}
		if !known {
			s.ConnectionKeys = append(s.ConnectionKeys, key)
			s.ConnectionCount = len(s.ConnectionKeys)
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
	if r.ProjectName != "" && (p.ProjectName == "" || !ts.Before(p.LastActivityAt)) {
		p.ProjectName = r.ProjectName
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
	if failureClass(r, role) == "network" {
		p.NetworkFailure = true
	}
	if p.FirstFailureAt.IsZero() || ts.Before(p.FirstFailureAt) {
		p.FirstFailureAt = ts
	}
	if !ts.Before(p.LastFailureAt) {
		p.LastFailureAt = ts
		p.LastErrorClass = failureClass(r, role)
		p.LastErrorCode = failureCode(r)
		p.LastTool = r.ToolName
		p.LastReason = failureReason(r, role)
		p.LastFailureBlock = role.Block
	}
}

// connectionKey is a short hash of a connection id: enough to tell the
// connections of one session apart, without keeping the ids.
func connectionKey(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:6])
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
	// The truncated start is the time of the earliest row still stored; the
	// caller's flush sets it from the index. Until then, and when no row is
	// left, it is the time of the last row removed.
	if at.After(s.TruncatedStartAt) {
		s.TruncatedStartAt = at
	}
}

// indexActivityTx writes the index entry of a row that belongs to a work
// session and folds the row into the summary. It is a no-op for a row that is
// already indexed, so re-saving a row never counts it twice.
func indexActivityTx(tx *bbolt.Tx, r *ActivityRecord, key []byte, role AttemptRole) error {
	// An administrator-only row (Spec 125 FR-019a) never joins a work
	// session: the session doors answer scoped callers from the summary and
	// this index, so its times, row count or position would show it even with
	// the row withheld. The backfill indexes through here too.
	if r.WorkSessionID == "" || IsAdminOnlyActivityType(r.Type) {
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
		// A new summary. Rows of this id from before it (the session's
		// summary expired, with its index, and a long-lived connection came
		// back under the same id) are stored without an index entry;
		// retention treats them as rows outside the window. For a session
		// that really is new there are none.
		// The boundary is just before this first row, which is indexed.
		s = &WorkSessionSummary{ID: r.WorkSessionID, UnindexedBefore: activityKeyTime(key).Add(-time.Nanosecond)}
	}
	if s.Removal != "" {
		// Retention was removing this session and a new row has arrived for
		// it (a long-lived connection woke up). The session lives on: the
		// mark is cleared, which also stops the removal at its next batch,
		// and the session is truncated to what is still stored.
		mode := s.Removal
		s.Removal = ""
		// (The stored counters are already true in both modes: each removal
		// batch lowered them for the entries it dropped.)
		prefix := workSessionRowPrefix(s.ID)
		if k, _ := rows.Cursor().Seek(prefix); k != nil && bytes.HasPrefix(k, prefix) {
			s.TruncatedStartAt = activityKeyTime(k[len(prefix):])
		}
		if mode == removalSummary {
			// The rows whose entries were dropped are still stored. They
			// are all older than the first entry left (the one just put, if
			// no other is); retention may now take them one by one.
			if at := s.TruncatedStartAt; at.After(s.UnindexedBefore) {
				s.UnindexedBefore = at
			}
		}
	}
	s.applyRow(r, role)
	return storeSummary(sums, s)
}

// indexableServerName reports whether a server name can be the left part of
// an index key. The separator is 0x00, so a name that contains it would make
// one server's entries match another server's prefix; such a name is not
// indexed (its event rows are still stored and listed by the activity log).
func indexableServerName(name string) bool {
	return name != "" && strings.IndexByte(name, workSessionKeySep) < 0
}

// indexUpstreamEventTx indexes an upstream event row by its server.
func indexUpstreamEventTx(tx *bbolt.Tx, r *ActivityRecord, key []byte) error {
	if r.Type != ActivityTypeUpstreamEvent || !indexableServerName(r.ServerName) {
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
		if s != nil && s.Removal != "" {
			s = nil // being removed by retention
		}
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
			if s.Removal != "" {
				return nil // being removed by retention
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

// WalkWorkSessionRefs calls fn for each index entry of a session, in time
// order, until fn returns false. Only keys and their small values are read.
// The ref's ActivityKey is valid only during the call; copy it to keep it.
func (m *Manager) WalkWorkSessionRefs(workSessionID string, fn func(WorkSessionRowRef) bool) error {
	return m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionRowsBucket))
		if b == nil {
			return nil
		}
		prefix := workSessionRowPrefix(workSessionID)
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			ak := k[len(prefix):]
			flags, server := parseIndexValue(v)
			role := flagsRole(flags)
			if !fn(WorkSessionRowRef{
				ActivityKey: ak, Timestamp: activityKeyTime(ak), Server: server,
				Counted: role.Counted, Failed: role.Failed, Block: role.Block,
			}) {
				return nil
			}
		}
		return nil
	})
}

// WalkWorkSessionTimeline calls fn for each index entry of a session merged
// with the upstream events of the given servers inside [from, to], in time
// order, until fn returns false. An event is placed before a session row of
// the same time only if it is strictly earlier; events of the same time go by
// activity key. Everything is read with cursors inside one read transaction,
// so the cost in memory does not grow with the session or the event history,
// and fn must not open another transaction. A ref's ActivityKey is valid only
// during the call; copy it to keep it.
func (m *Manager) WalkWorkSessionTimeline(workSessionID string, servers []string, from, to time.Time, fn func(WorkSessionRowRef) bool) error {
	return m.db.db.View(func(tx *bbolt.Tx) error {
		type eventCursor struct {
			server string
			prefix []byte
			c      *bbolt.Cursor
			ref    WorkSessionRowRef
			ok     bool
		}
		// load moves an event cursor to its next valid entry in the span,
		// starting at (k, v).
		load := func(ec *eventCursor, k, v []byte) {
			ec.ok = false
			for ; k != nil && bytes.HasPrefix(k, ec.prefix); k, v = ec.c.Next() {
				ak := k[len(ec.prefix):]
				ts := activityKeyTime(ak)
				if len(ak) < 22 || ts.IsZero() || ts.Before(from) {
					continue // malformed, or before the span
				}
				if !to.IsZero() && ts.After(to) {
					return
				}
				ec.ref = WorkSessionRowRef{ActivityKey: ak, Timestamp: ts, Server: ec.server, Event: true, Owner: string(v)}
				ec.ok = true
				return
			}
		}
		var cursors []*eventCursor
		if eb := tx.Bucket([]byte(UpstreamEventsByServerBucket)); eb != nil {
			for _, server := range servers {
				if !indexableServerName(server) {
					continue
				}
				ec := &eventCursor{server: server, prefix: workSessionRowPrefix(server), c: eb.Cursor()}
				k, v := ec.c.Seek(workSessionRowKey(server, activityKey(from, "")))
				load(ec, k, v)
				if ec.ok {
					cursors = append(cursors, ec)
				}
			}
		}
		// emitEvents calls fn for the events before until (all when zero).
		emitEvents := func(until time.Time) bool {
			for {
				var best *eventCursor
				for _, ec := range cursors {
					if !ec.ok {
						continue
					}
					if best == nil || ec.ref.Timestamp.Before(best.ref.Timestamp) ||
						(ec.ref.Timestamp.Equal(best.ref.Timestamp) && bytes.Compare(ec.ref.ActivityKey, best.ref.ActivityKey) < 0) {
						best = ec
					}
				}
				if best == nil || (!until.IsZero() && !best.ref.Timestamp.Before(until)) {
					return true
				}
				if !fn(best.ref) {
					return false
				}
				k, v := best.c.Next()
				load(best, k, v)
			}
		}

		if b := tx.Bucket([]byte(WorkSessionRowsBucket)); b != nil {
			prefix := workSessionRowPrefix(workSessionID)
			c := b.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				ak := k[len(prefix):]
				ts := activityKeyTime(ak)
				if !emitEvents(ts) {
					return nil
				}
				flags, server := parseIndexValue(v)
				role := flagsRole(flags)
				if !fn(WorkSessionRowRef{
					ActivityKey: ak, Timestamp: ts, Server: server,
					Counted: role.Counted, Failed: role.Failed, Block: role.Block,
				}) {
					return nil
				}
			}
		}
		emitEvents(time.Time{})
		return nil
	})
}

// maxOpenEventLookback bounds how many events of one server
// OpenUpstreamEventsBefore steps back over that the caller may not read.
const maxOpenEventLookback = 256

// OpenUpstreamEventsBefore returns, for each server in kinds, the latest
// event of the kinds asked for that was recorded before the given time (the
// timeline asks for connection_lost and process_exited), that the caller may
// read (visible is asked with the event's owner, "" for a shared event), and
// that no connection_restored followed before that time (release QA F2,
// SC-005). A session that starts while a server is already down has its
// cause here rather than inside its span.
//
// A row stands for repeats merged into it (metadata last_at). A loss counts
// at its last repeat up to until (the end of the session's span), since a
// repeat during the session is not shown by the span walk; a recovery
// counts at its last repeat only before the session started. Nothing older
// than notBefore counts: the recovery after a restart of MCPProxy is not
// recorded, so a loss from before the current process is no evidence
// either. Only the index and a few rows per server are read.
func (m *Manager) OpenUpstreamEventsBefore(kinds map[string][]string, before, until, notBefore time.Time, visible func(owner string) bool) ([]WorkSessionRowRef, error) {
	var out []WorkSessionRowRef
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		eb := tx.Bucket([]byte(UpstreamEventsByServerBucket))
		acts := tx.Bucket([]byte(ActivityRecordsBucket))
		if eb == nil || acts == nil {
			return nil
		}
		names := make([]string, 0, len(kinds))
		for server := range kinds {
			names = append(names, server)
		}
		sort.Strings(names)
		for _, server := range names {
			if !indexableServerName(server) || len(kinds[server]) == 0 {
				continue
			}
			want := make(map[string]bool, len(kinds[server]))
			for _, k := range kinds[server] {
				want[k] = true
			}
			if ref, ok := openEventOf(eb.Cursor(), acts, server, before, until, notBefore, want, visible); ok {
				out = append(out, ref)
			}
		}
		return nil
	})
	return out, err
}

func openEventOf(c *bbolt.Cursor, acts *bbolt.Bucket, server string, before, until, notBefore time.Time, want map[string]bool, visible func(string) bool) (WorkSessionRowRef, bool) {
	prefix := workSessionRowPrefix(server)
	k, _ := c.Seek(workSessionRowKey(server, activityKey(before, "")))
	var v []byte
	if k == nil {
		k, v = c.Last()
	} else {
		k, v = c.Prev()
	}
	// The whole bounded range is read: rows are keyed by their first
	// occurrence and later repeats merge into them, so a recovery with an
	// older key can have happened after a newer loss. What is open is the
	// connection row that happened last, if it happened after every recovery.
	var (
		latest        WorkSessionRowRef
		latestAt      time.Time
		restoredUntil time.Time
	)
	for steps := 0; k != nil && bytes.HasPrefix(k, prefix) && steps < maxOpenEventLookback; k, v = c.Prev() {
		steps++
		ak := k[len(prefix):]
		ts := activityKeyTime(ak)
		if len(ak) < 22 || ts.IsZero() || !ts.Before(before) {
			continue
		}
		if !notBefore.IsZero() && ts.Before(notBefore) {
			break
		}
		owner := string(v)
		if visible != nil && !visible(owner) {
			continue
		}
		data := acts.Get(ak)
		if data == nil {
			break // removed by retention: nothing older is evidence either
		}
		rec := &ActivityRecord{}
		if err := rec.UnmarshalBinary(data); err != nil {
			break
		}
		lastAt := ts
		var repeatedAt time.Time
		if s, _ := rec.Metadata["last_at"].(string); s != "" {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil && t.After(lastAt) {
				repeatedAt = t
			}
		}
		switch kind, _ := rec.Metadata["kind"].(string); {
		case kind == "connection_restored":
			// A recovery repeated after the session started is not known to
			// have been before it: it closes nothing from before the start.
			if repeatedAt.Before(before) && repeatedAt.After(lastAt) {
				lastAt = repeatedAt
			}
			if lastAt.After(restoredUntil) {
				restoredUntil = lastAt
			}
		case want[kind]:
			// A loss repeated during the session merged into this row from
			// before it, and the span walk does not show the row: it is the
			// cause, so its last occurrence counts.
			// A repeat after the session ended says nothing about it.
			if repeatedAt.After(lastAt) && (until.IsZero() || !repeatedAt.After(until)) {
				lastAt = repeatedAt
			}
			if lastAt.After(latestAt) {
				latestAt = lastAt
				latest = WorkSessionRowRef{ActivityKey: append([]byte(nil), ak...), Timestamp: ts, Server: server, Event: true, Owner: owner}
			}
		}
		// Other kinds (a failed token refresh) say nothing about the
		// connection.
	}
	if latest.ActivityKey == nil || !latestAt.After(restoredUntil) {
		return WorkSessionRowRef{}, false
	}
	return latest, true
}

// UpstreamEventRefs returns the upstream event rows of one server whose time
// is within [from, to], in time order. A zero `to` means "until now".
func (m *Manager) UpstreamEventRefs(server string, from, to time.Time) ([]WorkSessionRowRef, error) {
	var refs []WorkSessionRowRef
	if !indexableServerName(server) {
		return nil, nil
	}
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
			// The suffix must be a whole activity key inside the span. A
			// malformed one is skipped rather than joined into a timeline.
			if len(ak) < 22 || ts.IsZero() || ts.Before(from) {
				continue
			}
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
	idle := func(s *WorkSessionSummary) bool {
		return s.EndedAt.IsZero() && !s.LastActivityAt.IsZero() && !s.LastActivityAt.After(idleBefore)
	}
	// The summaries are read without the writer, and closed in bounded write
	// transactions that check each one again: many sessions can go idle
	// together (a restart closes every open one), and SaveActivity must not
	// wait behind all of them.
	var ids []string
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var s WorkSessionSummary
			if json.Unmarshal(v, &s) == nil && idle(&s) {
				ids = append(ids, string(k))
			}
			return nil
		})
	}); err != nil {
		return 0, err
	}
	closed := 0
	for len(ids) > 0 {
		n := min(len(ids), markEndedBatch)
		batch := ids[:n]
		ids = ids[n:]
		if err := m.db.db.Update(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(WorkSessionsBucket))
			if b == nil {
				return nil
			}
			for _, id := range batch {
				s, err := loadSummary(b, id)
				if err != nil || s == nil || !idle(s) {
					continue // gone, unreadable, or it got a row meanwhile
				}
				s.EndedAt = s.LastActivityAt
				if err := storeSummary(b, s); err != nil {
					return err
				}
				closed++
			}
			return nil
		}); err != nil {
			return closed, err
		}
	}
	return closed, nil
}

// markEndedBatch is how many summaries MarkWorkSessionsEnded closes in one
// write transaction.
var markEndedBatch = 500

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
		ok, err := m.rebuildSummary(id)
		if err != nil {
			return rebuilt, err
		}
		if ok {
			rebuilt++
		}
	}
	return rebuilt, nil
}

// repairChunk bounds the rows one transaction of a rebuild reads.
const repairChunk = 1000

// rebuildSummary rebuilds one missing summary from its retained rows. The
// rows are read in bounded read transactions, so a session of any size never
// holds the database's single writer; the summary is published in one short
// write transaction, which also folds in the rows that were indexed after
// the reading passed them, and gives way to a summary a live row has created
// meanwhile by merging into the rebuilt one.
func (m *Manager) rebuildSummary(id string) (bool, error) {
	s := &WorkSessionSummary{ID: id, Rebuilt: true}
	prefix := workSessionRowPrefix(id)
	var last []byte // the last index key folded in

	// fold applies the index entries after `last`, at most `limit` of them
	// (0 = all), and reports whether it reached the end.
	fold := func(tx *bbolt.Tx, limit int) bool {
		rows := tx.Bucket([]byte(WorkSessionRowsBucket))
		acts := tx.Bucket([]byte(ActivityRecordsBucket))
		if rows == nil || acts == nil {
			return true
		}
		c := rows.Cursor()
		var k, v []byte
		if last == nil {
			k, v = c.Seek(prefix)
		} else {
			k, v = c.Seek(last)
			if k != nil && bytes.Equal(k, last) {
				k, v = c.Next()
			}
		}
		n := 0
		for ; k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			if limit > 0 && n == limit {
				return false
			}
			n++
			last = append(last[:0], k...)
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
		return true
	}

	published := false
	for {
		for {
			done := false
			if err := m.db.db.View(func(tx *bbolt.Tx) error {
				done = fold(tx, repairChunk)
				return nil
			}); err != nil {
				return false, err
			}
			if done {
				break
			}
		}

		caughtUp := true
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			sums, err := tx.CreateBucketIfNotExists([]byte(WorkSessionsBucket))
			if err != nil {
				return err
			}
			// Rows indexed since the reading passed them: a short tail. If
			// it is not short (a burst of writes got in between), the
			// writer is released and the reading goes on outside it.
			if !fold(tx, repairChunk) {
				caughtUp = false
				return nil
			}
			if s.StartedAt.IsZero() {
				return nil // every row is gone; nothing to summarise
			}
			// A live row may have created a summary meanwhile. It holds only
			// the rows written since, all of which were just folded in above,
			// so the rebuilt summary replaces it and nothing is lowered.
			if existing, err := loadSummary(sums, id); err != nil {
				return err
			} else if existing != nil && !existing.EndedAt.IsZero() {
				s.EndedAt = existing.EndedAt
			} else if existing == nil {
				s.EndedAt = s.LastActivityAt
			}
			published = true
			return storeSummary(sums, s)
		})
		if err != nil || caughtUp {
			return published, err
		}
	}
}

// CloseOpenWorkSessionsAtStart gives every summary with no end its last
// activity as its end. Work-session ids do not survive a restart, so no
// session that was open when the core stopped can continue.
//
// before is when this process started recording: a session whose last
// activity is later was opened by this process and is left open.
func (m *Manager) CloseOpenWorkSessionsAtStart(before time.Time) (int, error) {
	return m.MarkWorkSessionsEnded(before)
}

// CloseOpenWorkSessions gives every summary with no end its last activity as
// its end. Called when the core stops, after the last writer (FR-015).
func (m *Manager) CloseOpenWorkSessions() (int, error) {
	return m.MarkWorkSessionsEnded(time.Now().UTC().Add(24 * time.Hour))
}
