package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// Spec 124 D4: session-aware retention.
//
// The activity log keeps its three caps (age, record count, size). What
// changes is WHAT is removed when a cap bites, so that a work session inside
// the retention window stays whole for as long as the caps allow:
//
//  1. Summaries older than the session window are dropped (their rows stay
//     readable through the ordinary activity filters, FR-036).
//  2. The age cap removes rows as before; a session that loses rows is marked
//     truncated and keeps its lifetime facts.
//  3. Over the size or count cap, in this order:
//     a. argument and response bodies of the oldest rows are removed, the rows
//     kept (FR-032);
//     b. rows that belong to no session inside the window are removed;
//     c. whole sessions inside the window are removed, oldest last activity
//     first (FR-033);
//     d. when a single session is all that is left and it alone is over, it is
//     trimmed from its oldest end.
//
// Every step writes in batches of at most retentionBatch rows per transaction
// (FR-038).

const (
	retentionBatch = 1000

	// scanChunk bounds the keys one read transaction of the measuring scan
	// visits, so the scan never pins one snapshot for the whole log.
	scanChunk = 10000
	// retentionShortfallKey holds the RetentionShortfall record (MetaBucket).
	retentionShortfallKey = "activity_retention_shortfall"
)

// maxRetentionCandidates bounds each candidate list one scan collects. When a
// list is cut and the log is still over its caps after the list is used up,
// the pass reads the log again and goes on with the same stage. A variable so
// that tests can make the bound small.
var maxRetentionCandidates = 50000

// Shortfall causes (FR-035).
const (
	ShortfallAgeCap    = "age_cap"
	ShortfallRecordCap = "record_cap"
	ShortfallSizeCap   = "size_cap"
)

// RetentionPolicy is one cleanup pass's limits. A zero limit disables that cap.
type RetentionPolicy struct {
	Now           time.Time
	MaxAge        time.Duration
	MaxRecords    int
	MaxBytes      int64
	SessionWindow time.Duration
}

// RetentionResult reports what one pass did.
type RetentionResult struct {
	SummariesExpired  int
	RowsAged          int
	BodiesStripped    int
	RowsOutsideWindow int
	SessionsRemoved   int
	RowsTrimmed       int
	// ShortfallCause is set when this pass removed or trimmed a session inside
	// the window.
	ShortfallCause string

	countCapStarted bool
}

// RetentionShortfall records that the caps could not hold the whole retention
// window (FR-035). It is cleared only when no in-window removal has happened
// for a full window, which is when complete history covers the window again; a
// single quiet pass does not clear it.
type RetentionShortfall struct {
	ObservedAt              time.Time `json:"observed_at"`
	Cause                   string    `json:"cause"`
	LastInWindowRemovalAt   time.Time `json:"last_in_window_removal_at"`
	OldestCompleteSessionAt time.Time `json:"oldest_complete_session_at,omitzero"`
}

// retentionState is the running byte and row count of the log during a pass.
type retentionState struct {
	rows  int
	bytes int64
}

func bucketBytes(b *bbolt.Bucket) (n int, size int64) {
	if b == nil {
		return 0, 0
	}
	_ = b.ForEach(func(k, v []byte) error {
		n++
		size += int64(len(k) + len(v))
		return nil
	})
	return n, size
}

// logScan is the result of the one read of the activity log a pass makes
// (FR-038): its size, and the rows a later step may act on, oldest first.
// The candidates are nominations from a cheap look at the stored bytes; the
// step that acts re-reads and re-checks each row.
type logScan struct {
	st retentionState
	// strip: rows that still have argument or response bodies.
	strip [][]byte
	// outside: rows that belong to no session with a summary.
	outside [][]byte
	// newest is the key of the newest row, which is never removed.
	newest []byte
	// stripTruncated / outsideTruncated say a candidate list was cut at
	// maxRetentionCandidates: more such rows exist than were nominated.
	stripTruncated   bool
	outsideTruncated bool
}

// maxRetentionRescans bounds how often one pass reads the log again because
// a candidate list was cut. Each rescan handles maxRetentionCandidates more
// rows, so the bound covers a log far larger than the record cap allows.
const maxRetentionRescans = 32

const sniffAuthArgument = "_auth_"

// sniffRow reads three facts out of a stored row without decoding it: its
// work-session id, and whether it has arguments or a response. It walks the
// TOP LEVEL of the JSON object only, so a nested value that merely looks like
// one of these keys (an argument named "work_session_id", a response body
// that quotes a row) cannot be mistaken for the row's own field.
//
// It is a nomination, not a decision: the acting step decodes the row and
// re-checks. On anything it does not understand it answers conservatively
// (no session, has bodies), so the row is nominated and the re-check decides.
func sniffRow(v []byte) (workSession string, hasArguments, hasResponse bool) {
	conservative := func() (string, bool, bool) { return "", true, true }
	i, n := 0, len(v)
	skipSpace := func() {
		for i < n && (v[i] == ' ' || v[i] == '\n' || v[i] == '\t' || v[i] == '\r') {
			i++
		}
	}
	// readString consumes a JSON string starting at v[i] == '"' and returns
	// its raw bytes (escapes are left as they are).
	readString := func() ([]byte, bool) {
		if i >= n || v[i] != '"' {
			return nil, false
		}
		i++
		start := i
		for i < n {
			switch v[i] {
			case '\\':
				i += 2
			case '"':
				out := v[start:i]
				i++
				return out, true
			default:
				i++
			}
		}
		return nil, false
	}
	// skipValue consumes any JSON value starting at v[i].
	skipValue := func() bool {
		if i >= n {
			return false
		}
		switch v[i] {
		case '"':
			_, ok := readString()
			return ok
		case '{', '[':
			depth := 0
			for i < n {
				switch v[i] {
				case '"':
					if _, ok := readString(); !ok {
						return false
					}
					continue
				case '{', '[':
					depth++
				case '}', ']':
					depth--
					if depth == 0 {
						i++
						return true
					}
				}
				i++
			}
			return false
		default:
			for i < n && v[i] != ',' && v[i] != '}' {
				i++
			}
			return i < n
		}
	}

	skipSpace()
	if i >= n || v[i] != '{' {
		return conservative()
	}
	i++
	for {
		skipSpace()
		if i < n && v[i] == '}' {
			return workSession, hasArguments, hasResponse
		}
		key, ok := readString()
		if !ok {
			return conservative()
		}
		skipSpace()
		if i >= n || v[i] != ':' {
			return conservative()
		}
		i++
		skipSpace()
		switch string(key) {
		case "work_session_id":
			val, ok := readString()
			if !ok {
				return conservative()
			}
			workSession = string(val)
		case "response":
			val, ok := readString()
			if !ok {
				return conservative()
			}
			hasResponse = len(val) > 0
		case "arguments":
			if i >= n || v[i] != '{' {
				// null, or something stripping does not touch.
				if !skipValue() {
					return conservative()
				}
				break
			}
			// An argument is a body unless it is one of the internal
			// `_auth_*` ones, which stripping keeps: a row that has only
			// those has nothing to strip and must not be nominated again
			// on every pass.
			i++
			for {
				skipSpace()
				if i < n && v[i] == '}' {
					i++
					break
				}
				name, ok := readString()
				if !ok {
					return conservative()
				}
				skipSpace()
				if i >= n || v[i] != ':' {
					return conservative()
				}
				i++
				skipSpace()
				if !skipValue() {
					return conservative()
				}
				if !bytes.HasPrefix(name, []byte(sniffAuthArgument)) {
					hasArguments = true
				}
				skipSpace()
				if i < n && v[i] == ',' {
					i++
				}
			}
		default:
			if !skipValue() {
				return conservative()
			}
		}
		skipSpace()
		if i < n && v[i] == ',' {
			i++
			continue
		}
		if i < n && v[i] == '}' {
			return workSession, hasArguments, hasResponse
		}
		return conservative()
	}
}

// summaryUnindexedBefore reads UnindexedBefore out of a stored summary. The
// field is absent from nearly every summary, so the common case is one
// substring search and no decoding.
func summaryUnindexedBefore(v []byte) time.Time {
	if !bytes.Contains(v, []byte(`"unindexed_before"`)) {
		return time.Time{}
	}
	var s struct {
		UnindexedBefore time.Time `json:"unindexed_before"`
	}
	if json.Unmarshal(v, &s) != nil {
		return time.Time{}
	}
	return s.UnindexedBefore
}

// scanLog reads the activity log once, in bounded read transactions, and
// measures the bytes that count against the size cap: the activity rows and
// the three buckets this feature adds (FR-034).
func (m *Manager) scanLog() (logScan, error) {
	var scan logScan
	// live maps each session with a summary to its UnindexedBefore time
	// (zero for nearly all of them).
	live := map[string]time.Time{}
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket([]byte(WorkSessionsBucket)); b != nil {
			_ = b.ForEach(func(k, v []byte) error {
				live[string(k)] = summaryUnindexedBefore(v)
				return nil
			})
		}
		for _, name := range []string{WorkSessionsBucket, WorkSessionRowsBucket, UpstreamEventsByServerBucket} {
			_, size := bucketBytes(tx.Bucket([]byte(name)))
			scan.st.bytes += size
		}
		return nil
	}); err != nil {
		return scan, err
	}

	var resume []byte
	for {
		done := false
		err := m.db.db.View(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if acts == nil {
				done = true
				return nil
			}
			c := acts.Cursor()
			var k, v []byte
			if resume == nil {
				k, v = c.First()
			} else {
				k, v = c.Seek(resume)
				if k != nil && bytes.Equal(k, resume) {
					k, v = c.Next()
				}
			}
			n := 0
			for ; k != nil && n < scanChunk; k, v = c.Next() {
				n++
				scan.st.rows++
				scan.st.bytes += int64(len(k) + len(v))
				scan.newest = append(scan.newest[:0], k...)
				ws, hasArguments, hasResponse := sniffRow(v)
				if hasArguments || hasResponse {
					if len(scan.strip) < maxRetentionCandidates {
						scan.strip = append(scan.strip, append([]byte(nil), k...))
					} else {
						scan.stripTruncated = true
					}
				}
				unindexedBefore, isLive := live[ws]
				if ws == "" || !isLive || (!unindexedBefore.IsZero() && activityKeyTime(k).Before(unindexedBefore)) {
					if len(scan.outside) < maxRetentionCandidates {
						scan.outside = append(scan.outside, append([]byte(nil), k...))
					} else {
						scan.outsideTruncated = true
					}
				}
			}
			if k == nil {
				done = true
			} else {
				resume = append(resume[:0], scan.newest...)
			}
			return nil
		})
		if err != nil || done {
			return scan, err
		}
	}
}

// txSummaries caches the summaries one transaction touches and writes them
// back once. It tracks how the stored size of each changed, so the running
// byte count of the pass stays true to what is stored.
type txSummaries struct {
	tx      *bbolt.Tx
	bucket  *bbolt.Bucket
	cache   map[string]*WorkSessionSummary
	stored  map[string]int // serialized size when loaded
	trimmed map[string]bool
	missing map[string]bool
}

func newTxSummaries(tx *bbolt.Tx) *txSummaries {
	return &txSummaries{
		tx: tx, bucket: tx.Bucket([]byte(WorkSessionsBucket)),
		cache: map[string]*WorkSessionSummary{}, stored: map[string]int{},
		trimmed: map[string]bool{}, missing: map[string]bool{},
	}
}

func (t *txSummaries) get(id string) *WorkSessionSummary {
	if t.bucket == nil || id == "" || t.missing[id] {
		return nil
	}
	if s, ok := t.cache[id]; ok {
		return s
	}
	data := t.bucket.Get([]byte(id))
	var s WorkSessionSummary
	if data == nil || json.Unmarshal(data, &s) != nil {
		t.missing[id] = true
		return nil
	}
	t.cache[id] = &s
	t.stored[id] = len(data)
	return &s
}

// flush writes the touched summaries and returns the change in stored bytes.
// A summary that lost rows gets its truncated-start time: the time of its
// earliest row that is still stored (the time of the last row removed when
// none is left).
func (t *txSummaries) flush() (int64, error) {
	var delta int64
	rows := t.tx.Bucket([]byte(WorkSessionRowsBucket))
	for id, s := range t.cache {
		if t.trimmed[id] && rows != nil {
			prefix := workSessionRowPrefix(id)
			if k, _ := rows.Cursor().Seek(prefix); k != nil && bytes.HasPrefix(k, prefix) {
				s.TruncatedStartAt = activityKeyTime(k[len(prefix):])
			}
		}
		data, err := json.Marshal(s)
		if err != nil {
			return delta, err
		}
		if err := t.bucket.Put([]byte(id), data); err != nil {
			return delta, err
		}
		delta += int64(len(data) - t.stored[id])
	}
	return delta, nil
}

// deleteRowTx removes one activity row with its index entries and lowers the
// stored counters of its session. It returns the bytes freed and whether the
// row belonged to a session that still has a summary.
func deleteRowTx(tx *bbolt.Tx, sums *txSummaries, key, value []byte) (freed int64, inSession bool, err error) {
	acts := tx.Bucket([]byte(ActivityRecordsBucket))
	freed = int64(len(key) + len(value))

	var rec ActivityRecord
	if uerr := rec.UnmarshalBinary(value); uerr == nil {
		if rec.WorkSessionID != "" {
			if rows := tx.Bucket([]byte(WorkSessionRowsBucket)); rows != nil {
				ik := workSessionRowKey(rec.WorkSessionID, key)
				if iv := rows.Get(ik); iv != nil {
					freed += int64(len(ik) + len(iv))
					flags, server := parseIndexValue(iv)
					if s := sums.get(rec.WorkSessionID); s != nil {
						s.forgetStoredRow(flags, server, activityKeyTime(key))
						sums.trimmed[rec.WorkSessionID] = true
						inSession = true
					}
					if err := rows.Delete(ik); err != nil {
						return 0, false, err
					}
				}
			}
		}
		if rec.Type == ActivityTypeUpstreamEvent && rec.ServerName != "" {
			if ev := tx.Bucket([]byte(UpstreamEventsByServerBucket)); ev != nil {
				ek := workSessionRowKey(rec.ServerName, key)
				if evv := ev.Get(ek); evv != nil {
					freed += int64(len(ek) + len(evv))
					if err := ev.Delete(ek); err != nil {
						return 0, false, err
					}
				}
			}
		}
	}
	return freed, inSession, acts.Delete(key)
}

// RunActivityRetention runs one cleanup pass.
func (m *Manager) RunActivityRetention(p RetentionPolicy) (RetentionResult, error) {
	var res RetentionResult
	if p.Now.IsZero() {
		p.Now = time.Now().UTC()
	}
	windowStart := time.Time{}
	if p.SessionWindow > 0 {
		windowStart = p.Now.Add(-p.SessionWindow)
	}

	// 1. Session window.
	if p.SessionWindow > 0 {
		n, err := m.expireSummaries(windowStart)
		if err != nil {
			return res, err
		}
		res.SummariesExpired = n
	}

	// 2. Age cap.
	if p.MaxAge > 0 {
		n, inWindow, err := m.pruneAged(p.Now.Add(-p.MaxAge), p)
		if err != nil {
			return res, err
		}
		res.RowsAged = n
		if inWindow {
			res.ShortfallCause = ShortfallAgeCap
		}
	}

	// 3. Size and count caps.
	//
	// A stage that is less destructive must be used up before the next one
	// starts. The scan nominates a bounded number of rows per stage, so when a
	// stage ran out of nominations that were cut short and the log is still
	// over, the log is read again and the same stage goes on; the pass never
	// moves on to deleting rows, or whole sessions, while bodies or outside
	// rows that could have satisfied the cap remain.
	if p.MaxBytes > 0 || p.MaxRecords > 0 {
		for rescan := 0; ; rescan++ {
			scan, err := m.scanLog()
			if err != nil {
				return res, err
			}
			st := scan.st
			// The count cap prunes down to 90% of itself, and never below
			// one row.
			countCap, targetRows := p.MaxRecords > 0 && st.rows > p.MaxRecords, 0
			if rescan > 0 && p.MaxRecords > 0 {
				// A pass that already started pruning for the count cap
				// keeps pruning to its target, not to the cap.
				countCap = st.rows > int(float64(p.MaxRecords)*0.9) && res.countCapStarted
			}
			if countCap {
				res.countCapStarted = true
				targetRows = int(float64(p.MaxRecords) * 0.9)
				if targetRows < 1 {
					targetRows = 1
				}
			}
			over := func() bool {
				return (p.MaxBytes > 0 && st.bytes > p.MaxBytes) || (countCap && st.rows > targetRows)
			}
			cause := func() string {
				if p.MaxBytes > 0 && st.bytes > p.MaxBytes {
					return ShortfallSizeCap
				}
				return ShortfallRecordCap
			}
			again := func(truncated bool) bool {
				return truncated && over() && rescan < maxRetentionRescans
			}

			// 3a. Bodies. Only the size cap is helped by this.
			if p.MaxBytes > 0 && st.bytes > p.MaxBytes {
				n, err := m.stripBodies(&st, p.MaxBytes, scan.strip)
				if err != nil {
					return res, err
				}
				res.BodiesStripped += n
				if p.MaxBytes > 0 && st.bytes > p.MaxBytes && scan.stripTruncated && rescan < maxRetentionRescans {
					continue // more bodies remain than were nominated
				}
			}
			// 3b. Rows outside the window.
			if over() {
				n, err := m.pruneOutsideWindow(&st, over, scan.outside, scan.newest)
				if err != nil {
					return res, err
				}
				res.RowsOutsideWindow += n
				if again(scan.outsideTruncated) {
					continue // more outside rows remain than were nominated
				}
			}
			// 3c. Whole sessions, and 3d. the last one.
			if over() {
				c := cause()
				// Recorded before the first row goes: a pass that is
				// interrupted midway has still told the operator.
				if err := m.updateShortfall(p, c); err != nil {
					return res, err
				}
				removed, trimmed, err := m.pruneSessions(&st, over)
				if err != nil {
					return res, err
				}
				res.SessionsRemoved, res.RowsTrimmed = removed, trimmed
				if removed > 0 || trimmed > 0 {
					res.ShortfallCause = c
				}
			}
			break
		}
	}

	if err := m.updateShortfall(p, res.ShortfallCause); err != nil {
		return res, err
	}
	return res, nil
}

// expireSummaries deletes summaries whose last activity is before cutoff, with
// their index entries. The rows stay (FR-036).
func (m *Manager) expireSummaries(cutoff time.Time) (int, error) {
	var expired []string
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var s WorkSessionSummary
			if json.Unmarshal(v, &s) == nil && s.LastActivityAt.Before(cutoff) {
				expired = append(expired, string(k))
			}
			return nil
		})
	}); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range expired {
		dropped, err := m.dropSession(id, removalSummary, cutoff, nil)
		if err != nil {
			return n, err
		}
		if dropped {
			n++
		}
	}
	return n, nil
}

// Summary removal modes, stored on the summary while a removal is under way.
const (
	// removalSummary drops the summary and its index; the rows stay.
	removalSummary = "summary"
	// removalSession drops the rows too.
	removalSession = "session"
)

// dropSession removes a session's index entries and its summary, in batches.
// In removalSession mode it removes the activity rows too.
//
// The first batch marks the summary as being removed, in the same transaction
// that deletes its first rows. A removal interrupted by a crash is therefore
// visible on the summary and is finished at the next start
// (ResumeSessionRemovals); until then the readers do not show the session.
//
// expireBefore, when set, is re-checked in that first transaction: a summary
// whose last activity is no longer before it (a row arrived since it was
// selected) is left alone, and dropSession reports false.
func (m *Manager) dropSession(id, mode string, expireBefore time.Time, st *retentionState) (bool, error) {
	prefix := workSessionRowPrefix(id)
	first := true
	for {
		more, skipped := false, false
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			idx := tx.Bucket([]byte(WorkSessionRowsBucket))
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			sums := tx.Bucket([]byte(WorkSessionsBucket))

			if !first && sums != nil {
				// A live row cleared the mark: the session is active again
				// and keeps what is left of its index.
				if sum, err := loadSummary(sums, id); err != nil {
					return err
				} else if sum == nil || sum.Removal == "" {
					skipped = true
					return nil
				}
			}
			if first && sums != nil {
				sum, err := loadSummary(sums, id)
				if err != nil {
					return err
				}
				if sum != nil {
					if !expireBefore.IsZero() && !sum.LastActivityAt.Before(expireBefore) {
						skipped = true
						return nil
					}
					oldSize := len(sums.Get([]byte(id)))
					sum.Removal = mode
					if err := storeSummary(sums, sum); err != nil {
						return err
					}
					if st != nil {
						st.bytes += int64(len(sums.Get([]byte(id))) - oldSize)
					}
				}
			}

			var keys [][]byte
			var sizes []int64
			if idx != nil {
				c := idx.Cursor()
				for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
					if len(keys) == retentionBatch {
						more = true
						break
					}
					keys = append(keys, append([]byte(nil), k...))
					sizes = append(sizes, int64(len(k)+len(v)))
				}
			}
			for i, k := range keys {
				if mode == removalSession && acts != nil {
					ak := k[len(prefix):]
					if v := acts.Get(ak); v != nil {
						if st != nil {
							st.rows--
							st.bytes -= int64(len(ak) + len(v))
						}
						if err := acts.Delete(ak); err != nil {
							return err
						}
					}
				}
				if st != nil {
					st.bytes -= sizes[i]
				}
				if err := idx.Delete(k); err != nil {
					return err
				}
			}
			if !more && sums != nil {
				if v := sums.Get([]byte(id)); v != nil && st != nil {
					st.bytes -= int64(len(id) + len(v))
				}
				return sums.Delete([]byte(id))
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		if skipped {
			return false, nil
		}
		first = false
		if !more {
			return true, nil
		}
	}
}

// ResumeSessionRemovals finishes every session removal that a crash
// interrupted. Called once at start, before sessions are read.
func (m *Manager) ResumeSessionRemovals() (int, error) {
	pending := map[string]string{}
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var s WorkSessionSummary
			if json.Unmarshal(v, &s) == nil && s.Removal != "" {
				pending[string(k)] = s.Removal
			}
			return nil
		})
	}); err != nil {
		return 0, err
	}
	for id, mode := range pending {
		var err error
		if mode == removalSession {
			err = m.resumeSessionRemoval(id)
		} else {
			_, err = m.dropSession(id, mode, time.Time{}, nil)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}

// resumeSessionRemoval finishes an interrupted whole-session removal. The
// mark is already on the summary, so the selection is not re-checked.
func (m *Manager) resumeSessionRemoval(id string) error {
	// removeSession's first transaction sets the mark and re-checks the
	// selection; on resume the mark is set and the checks do not apply, so
	// the loop is entered as a continuation.
	prefix := workSessionRowPrefix(id)
	for {
		more := false
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			idx := tx.Bucket([]byte(WorkSessionRowsBucket))
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			sums := newTxSummaries(tx)
			sum := sums.get(id)
			if sum == nil || sum.Removal == "" {
				return nil
			}
			var keys [][]byte
			if idx != nil {
				c := idx.Cursor()
				for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
					if len(keys) == retentionBatch {
						more = true
						break
					}
					keys = append(keys, append([]byte(nil), k[len(prefix):]...))
				}
			}
			for _, ak := range keys {
				var v []byte
				if acts != nil {
					v = acts.Get(ak)
				}
				if v == nil {
					if err := idx.Delete(workSessionRowKey(id, ak)); err != nil {
						return err
					}
					continue
				}
				if _, _, err := deleteRowTx(tx, sums, ak, append([]byte(nil), v...)); err != nil {
					return err
				}
			}
			if _, err := sums.flush(); err != nil {
				return err
			}
			if !more {
				return sums.bucket.Delete([]byte(id))
			}
			return nil
		})
		if err != nil || !more {
			return err
		}
	}
}

// pruneAged deletes rows older than cutoff, oldest first. It reports whether a
// session that still has a summary lost rows.
func (m *Manager) pruneAged(cutoff time.Time, p RetentionPolicy) (deleted int, inWindow bool, err error) {
	cutoffKey := activityKey(cutoff, "")
	for {
		batch := 0
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if acts == nil {
				return nil
			}
			sums := newTxSummaries(tx)
			type kv struct{ k, v []byte }
			var doomed []kv
			c := acts.Cursor()
			for k, v := c.First(); k != nil && bytes.Compare(k, cutoffKey) < 0 && len(doomed) < retentionBatch; k, v = c.Next() {
				doomed = append(doomed, kv{append([]byte(nil), k...), append([]byte(nil), v...)})
			}
			for _, d := range doomed {
				_, in, err := deleteRowTx(tx, sums, d.k, d.v)
				if err != nil {
					return fmt.Errorf("failed to delete old activity: %w", err)
				}
				if in && !inWindow {
					// Recorded in the transaction that removes the first row
					// of a session inside the window: a crash after it must
					// not leave the loss unreported.
					if err := putShortfallTx(tx, p, ShortfallAgeCap); err != nil {
						return err
					}
				}
				inWindow = inWindow || in
				batch++
			}
			_, err := sums.flush()
			return err
		})
		if err != nil {
			return deleted, inWindow, err
		}
		deleted += batch
		if batch < retentionBatch {
			return deleted, inWindow, nil
		}
	}
}

// stripRecordBodies removes the argument and response bodies of a row and
// reports whether anything was removed. Every other field is kept (FR-032).
// The internal `_auth_*` arguments are kept too: rows written before the
// attribution fields existed carry their owner there, and a row must stay
// readable by its owner after stripping. The owner is also copied to the
// first-class fields.
func stripRecordBodies(rec *ActivityRecord) bool {
	removed := rec.Response != ""
	var kept map[string]interface{}
	for k, v := range rec.Arguments {
		if strings.HasPrefix(k, sniffAuthArgument) {
			if kept == nil {
				kept = make(map[string]interface{})
			}
			kept[k] = v
			continue
		}
		removed = true
	}
	if !removed {
		return false
	}
	if rec.TokenName == "" {
		rec.TokenName = rec.EffectiveTokenName()
	}
	if rec.TokenPrefix == "" {
		rec.TokenPrefix = extractAuthMetadataField(rec, authArgTokenPrefix)
	}
	rec.Arguments, rec.Response, rec.BodiesRemoved = kept, "", true
	return true
}

// stripBodies removes the bodies of the candidate rows, oldest first, until
// the log is within maxBytes or the candidates are used up.
func (m *Manager) stripBodies(st *retentionState, maxBytes int64, candidates [][]byte) (int, error) {
	stripped := 0
	for len(candidates) > 0 && st.bytes > maxBytes {
		n := len(candidates)
		if n > retentionBatch {
			n = retentionBatch
		}
		batch := candidates[:n]
		used := 0
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if acts == nil {
				used = len(batch)
				return nil
			}
			rows := tx.Bucket([]byte(WorkSessionRowsBucket))
			freed := int64(0)
			for _, k := range batch {
				if st.bytes-freed <= maxBytes {
					break
				}
				used++
				v := acts.Get(k)
				if v == nil {
					continue
				}
				var rec ActivityRecord
				if rec.UnmarshalBinary(v) != nil || !stripRecordBodies(&rec) {
					continue
				}
				data, err := rec.MarshalBinary()
				if err != nil || len(data) >= len(v) {
					continue
				}
				freed += int64(len(v) - len(data))
				if err := acts.Put(k, data); err != nil {
					return err
				}
				if rec.WorkSessionID != "" && rows != nil {
					ik := workSessionRowKey(rec.WorkSessionID, k)
					if iv := rows.Get(ik); iv != nil {
						nv := append([]byte(nil), iv...)
						nv[0] |= rowFlagBodiesRemoved
						if err := rows.Put(ik, nv); err != nil {
							return err
						}
					}
				}
				stripped++
			}
			st.bytes -= freed
			return nil
		})
		if err != nil {
			return stripped, err
		}
		if used == 0 {
			break
		}
		candidates = candidates[used:]
	}
	return stripped, nil
}

// pruneOutsideWindow deletes, oldest first, candidate rows that belong to no
// session with a summary: rows with no work session, and rows of sessions
// older than the window. Each candidate is re-read and re-checked; a row of a
// session inside the window is never removed here, and neither is the newest
// row of the log.
func (m *Manager) pruneOutsideWindow(st *retentionState, over func() bool, candidates [][]byte, newest []byte) (int, error) {
	deleted := 0
	for len(candidates) > 0 && over() {
		n := len(candidates)
		if n > retentionBatch {
			n = retentionBatch
		}
		batch := candidates[:n]
		used := 0
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if acts == nil {
				used = len(batch)
				return nil
			}
			sums := newTxSummaries(tx)
			pending := retentionState{}
			for _, k := range batch {
				if !overAfter(st, pending, over) {
					break
				}
				used++
				if bytes.Equal(k, newest) {
					continue
				}
				v := acts.Get(k)
				if v == nil {
					continue
				}
				var rec ActivityRecord
				if rec.UnmarshalBinary(v) == nil && rec.WorkSessionID != "" {
					if sum := sums.get(rec.WorkSessionID); sum != nil && !unindexedRow(tx, sum, rec.WorkSessionID, k) {
						continue // inside the window: whole or not at all
					}
				}
				freed, _, err := deleteRowTx(tx, sums, k, append([]byte(nil), v...))
				if err != nil {
					return err
				}
				pending.rows++
				pending.bytes += freed
				deleted++
			}
			st.rows -= pending.rows
			st.bytes -= pending.bytes
			delta, err := sums.flush()
			st.bytes += delta
			return err
		})
		if err != nil {
			return deleted, err
		}
		if used == 0 {
			break
		}
		candidates = candidates[used:]
	}
	return deleted, nil
}

// unindexedRow reports whether the row at key is one a cancelled summary
// expiry left behind: older than the session's UnindexedBefore and without an
// index entry. Such a row is in no timeline and no count, so it is removed
// like a row outside the window.
func unindexedRow(tx *bbolt.Tx, sum *WorkSessionSummary, id string, key []byte) bool {
	if sum.UnindexedBefore.IsZero() || !activityKeyTime(key).Before(sum.UnindexedBefore) {
		return false
	}
	rows := tx.Bucket([]byte(WorkSessionRowsBucket))
	return rows == nil || rows.Get(workSessionRowKey(id, key)) == nil
}

// overAfter reports whether the log would still be over its caps once the
// pending deletions are applied.
func overAfter(st *retentionState, pending retentionState, over func() bool) bool {
	saved := *st
	st.rows -= pending.rows
	st.bytes -= pending.bytes
	still := over()
	*st = saved
	return still
}

// pruneSessions removes whole sessions inside the window, oldest last activity
// first, and trims the last remaining one from its oldest end when it alone is
// still over the caps.
func (m *Manager) pruneSessions(st *retentionState, over func() bool) (removed, trimmed int, err error) {
	// refused counts how often each session's removal was refused in this
	// stage. A refusal is either a stale selection (the session got a row
	// between the listing and the removal), which a second try settles, or
	// the session holding the newest row of the log, which is never removed.
	// Either way the stage goes on with the next session.
	refused := map[string]int{}
	for over() {
		// The order is read again for every removal: a session that got a
		// row since is no longer the one with the oldest last activity.
		sums, err := m.ListWorkSessionSummaries()
		if err != nil {
			return removed, trimmed, err
		}
		if len(sums) <= 1 {
			if len(sums) == 1 {
				trimmed, err = m.trimSession(sums[0].ID, st, over)
			}
			return removed, trimmed, err
		}
		sort.Slice(sums, func(i, j int) bool {
			if !sums[i].LastActivityAt.Equal(sums[j].LastActivityAt) {
				return sums[i].LastActivityAt.Before(sums[j].LastActivityAt)
			}
			return sums[i].ID < sums[j].ID
		})
		var victim *WorkSessionSummary
		for _, s := range sums {
			if refused[s.ID] < 2 {
				victim = s
				break
			}
		}
		if victim == nil {
			return removed, trimmed, nil
		}
		ok, err := m.removeSession(victim.ID, victim.LastActivityAt, st)
		if err != nil {
			return removed, trimmed, err
		}
		if ok {
			removed++
			continue
		}
		refused[victim.ID]++
	}
	return removed, trimmed, nil
}

// removeSession removes a whole session inside the window: its rows, its
// index entries and its summary, in batches.
//
// Rows go through deleteRowTx, so the summary's stored counters and truncated
// start stay true after every batch, and the first batch marks the summary as
// being removed. That makes every interruption safe:
//
//   - a crash leaves a truncated, consistent, marked session, hidden from
//     readers, and the removal is finished at the next start;
//   - a live row for the session (a long-lived connection woke up) clears the
//     mark in indexActivityTx; the next batch sees that and stops, and the
//     session stays, truncated, with its new row.
//
// The first transaction re-checks the selection: the session must still have
// the last activity it was selected for (expectLast, when set), and must not
// hold the newest row of the log, which is never removed. It reports false
// when the session was left alone.
func (m *Manager) removeSession(id string, expectLast time.Time, st *retentionState) (bool, error) {
	prefix := workSessionRowPrefix(id)
	first := true
	for {
		more, stopped, skipped := false, false, false
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			idx := tx.Bucket([]byte(WorkSessionRowsBucket))
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			sums := newTxSummaries(tx)
			sum := sums.get(id)
			if sum == nil {
				stopped = true
				return nil
			}
			if first {
				if !expectLast.IsZero() && !sum.LastActivityAt.Equal(expectLast) {
					skipped = true
					return nil
				}
				if acts != nil && idx != nil {
					if newest, _ := acts.Cursor().Last(); newest != nil && idx.Get(workSessionRowKey(id, newest)) != nil {
						skipped = true
						return nil
					}
				}
				sum.Removal = removalSession
			} else if sum.Removal == "" {
				stopped = true // a live row cancelled the removal
				return nil
			}

			var keys [][]byte
			if idx != nil {
				c := idx.Cursor()
				for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
					if len(keys) == retentionBatch {
						more = true
						break
					}
					keys = append(keys, append([]byte(nil), k[len(prefix):]...))
				}
			}
			var freed int64
			rows := 0
			for _, ak := range keys {
				var v []byte
				if acts != nil {
					v = acts.Get(ak)
				}
				if v == nil {
					ik := workSessionRowKey(id, ak)
					if iv := idx.Get(ik); iv != nil {
						freed += int64(len(ik) + len(iv))
						if err := idx.Delete(ik); err != nil {
							return err
						}
					}
					continue
				}
				f, _, err := deleteRowTx(tx, sums, ak, append([]byte(nil), v...))
				if err != nil {
					return err
				}
				freed += f
				rows++
			}
			delta, err := sums.flush()
			if err != nil {
				return err
			}
			if !more {
				// Nothing of the session is left: the summary goes too.
				if v := sums.bucket.Get([]byte(id)); v != nil {
					freed += int64(len(id) + len(v))
				}
				if err := sums.bucket.Delete([]byte(id)); err != nil {
					return err
				}
			}
			if st != nil {
				st.rows -= rows
				st.bytes += delta - freed
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		if skipped {
			return false, nil
		}
		first = false
		if stopped || !more {
			return true, nil
		}
	}
}

// trimSession deletes the oldest rows of one session until the caps hold. The
// newest row always stays.
func (m *Manager) trimSession(id string, st *retentionState, over func() bool) (int, error) {
	trimmed := 0
	prefix := workSessionRowPrefix(id)
	for over() {
		batch := 0
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			idx := tx.Bucket([]byte(WorkSessionRowsBucket))
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if idx == nil || acts == nil {
				return nil
			}
			sums := newTxSummaries(tx)
			var keys [][]byte
			c := idx.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix) && len(keys) <= retentionBatch; k, _ = c.Next() {
				keys = append(keys, append([]byte(nil), k[len(prefix):]...))
			}
			if len(keys) == 0 {
				return nil
			}
			// The session's newest row is kept when it is the newest row of
			// the log, which is never removed. When a newer row exists
			// outside the session (an upstream event, say), the session's
			// last row may go too, or a one-record cap would keep two rows.
			// A full batch always leaves its last key for the next one.
			keepLast := len(keys) > retentionBatch
			if !keepLast {
				newest, _ := acts.Cursor().Last()
				keepLast = newest == nil || bytes.Equal(newest, keys[len(keys)-1])
			}
			if keepLast {
				if len(keys) == 1 {
					return nil // only the newest row is left
				}
				keys = keys[:len(keys)-1]
			}
			pending := retentionState{}
			for _, ak := range keys {
				if !overAfter(st, pending, over) {
					break
				}
				v := acts.Get(ak)
				if v == nil {
					// The row is already gone; drop the dangling index entry.
					ik := workSessionRowKey(id, ak)
					if iv := idx.Get(ik); iv != nil {
						pending.bytes += int64(len(ik) + len(iv))
						if err := idx.Delete(ik); err != nil {
							return err
						}
					}
					continue
				}
				freed, _, err := deleteRowTx(tx, sums, ak, append([]byte(nil), v...))
				if err != nil {
					return err
				}
				pending.rows++
				pending.bytes += freed
				batch++
			}
			st.rows -= pending.rows
			st.bytes -= pending.bytes
			delta, err := sums.flush()
			st.bytes += delta
			return err
		})
		if err != nil {
			return trimmed, err
		}
		trimmed += batch
		if batch == 0 {
			break
		}
	}
	return trimmed, nil
}

// updateShortfall records a shortfall observed by this pass, and clears the
// record once no in-window removal has happened for a full window.
func (m *Manager) updateShortfall(p RetentionPolicy, cause string) error {
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		return putShortfallTx(tx, p, cause)
	})
}

// putShortfallTx is updateShortfall inside a caller's transaction.
func putShortfallTx(tx *bbolt.Tx, p RetentionPolicy, cause string) error {
	{
		meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
		if err != nil {
			return err
		}
		var cur *RetentionShortfall
		if data := meta.Get([]byte(retentionShortfallKey)); data != nil {
			var s RetentionShortfall
			if json.Unmarshal(data, &s) == nil {
				cur = &s
			}
		}
		switch {
		case cause != "":
			cur = &RetentionShortfall{ObservedAt: p.Now, Cause: cause, LastInWindowRemovalAt: p.Now}
		case cur == nil:
			return nil
		case p.SessionWindow > 0 && p.Now.Sub(cur.LastInWindowRemovalAt) > p.SessionWindow:
			return meta.Delete([]byte(retentionShortfallKey))
		}
		cur.OldestCompleteSessionAt = oldestCompleteSession(tx)
		data, err := json.Marshal(cur)
		if err != nil {
			return err
		}
		return meta.Put([]byte(retentionShortfallKey), data)
	}
}

// oldestCompleteSession is the start of the oldest session whose rows have not
// been trimmed, or zero when there is none.
func oldestCompleteSession(tx *bbolt.Tx) time.Time {
	var oldest time.Time
	b := tx.Bucket([]byte(WorkSessionsBucket))
	if b == nil {
		return oldest
	}
	_ = b.ForEach(func(_, v []byte) error {
		var s WorkSessionSummary
		if json.Unmarshal(v, &s) != nil || !s.TruncatedStartAt.IsZero() || s.StartedAt.IsZero() {
			return nil
		}
		if oldest.IsZero() || s.StartedAt.Before(oldest) {
			oldest = s.StartedAt
		}
		return nil
	})
	return oldest
}

// GetRetentionShortfall returns the current shortfall record, or nil.
func (m *Manager) GetRetentionShortfall() (*RetentionShortfall, error) {
	var out *RetentionShortfall
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		meta := tx.Bucket([]byte(MetaBucket))
		if meta == nil {
			return nil
		}
		data := meta.Get([]byte(retentionShortfallKey))
		if data == nil {
			return nil
		}
		var s RetentionShortfall
		if err := json.Unmarshal(data, &s); err != nil {
			return nil
		}
		out = &s
		return nil
	})
	return out, err
}
