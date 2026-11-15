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
	// maxRetentionCandidates bounds each candidate list the scan collects.
	// A pass that needs more continues in the next pass.
	maxRetentionCandidates = 50000
	// retentionShortfallKey holds the RetentionShortfall record (MetaBucket).
	retentionShortfallKey = "activity_retention_shortfall"
)

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
}

var (
	sniffWorkSession  = []byte(`"work_session_id":"`)
	sniffArguments    = []byte(`"arguments":{`)
	sniffResponse     = []byte(`"response":"`)
	sniffBodiesGone   = []byte(`"bodies_removed":true`)
	sniffAuthArgument = "_auth_"
)

// sniffedWorkSessionID reads the work-session id out of a stored row without
// decoding it. JSON escapes a quote inside a string, so the unescaped pattern
// is a real key; a nested map with the same key name can only make a row look
// like it belongs to a session, and the acting step re-checks.
func sniffedWorkSessionID(v []byte) string {
	i := bytes.Index(v, sniffWorkSession)
	if i < 0 {
		return ""
	}
	rest := v[i+len(sniffWorkSession):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return string(rest[:j])
}

func sniffedHasBodies(v []byte) bool {
	if bytes.Contains(v, sniffBodiesGone) {
		return false
	}
	return bytes.Contains(v, sniffArguments) || bytes.Contains(v, sniffResponse)
}

// scanLog reads the activity log once, in bounded read transactions, and
// measures the bytes that count against the size cap: the activity rows and
// the three buckets this feature adds (FR-034).
func (m *Manager) scanLog() (logScan, error) {
	var scan logScan
	live := map[string]bool{}
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket([]byte(WorkSessionsBucket)); b != nil {
			_ = b.ForEach(func(k, _ []byte) error {
				live[string(k)] = true
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
				if len(scan.strip) < maxRetentionCandidates && sniffedHasBodies(v) {
					scan.strip = append(scan.strip, append([]byte(nil), k...))
				}
				if len(scan.outside) < maxRetentionCandidates {
					if ws := sniffedWorkSessionID(v); ws == "" || !live[ws] {
						scan.outside = append(scan.outside, append([]byte(nil), k...))
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
		n, inWindow, err := m.pruneAged(p.Now.Add(-p.MaxAge))
		if err != nil {
			return res, err
		}
		res.RowsAged = n
		if inWindow {
			res.ShortfallCause = ShortfallAgeCap
		}
	}

	// 3. Size and count caps.
	if p.MaxBytes > 0 || p.MaxRecords > 0 {
		scan, err := m.scanLog()
		if err != nil {
			return res, err
		}
		st := scan.st
		// The count cap prunes down to 90% of itself, and never below one row.
		countCap, targetRows := p.MaxRecords > 0 && st.rows > p.MaxRecords, 0
		if countCap {
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

		// 3a. Bodies. Only the size cap is helped by this.
		if p.MaxBytes > 0 && st.bytes > p.MaxBytes {
			n, err := m.stripBodies(&st, p.MaxBytes, scan.strip)
			if err != nil {
				return res, err
			}
			res.BodiesStripped = n
		}
		// 3b. Rows outside the window.
		if over() {
			n, err := m.pruneOutsideWindow(&st, over, scan.outside, scan.newest)
			if err != nil {
				return res, err
			}
			res.RowsOutsideWindow = n
		}
		// 3c. Whole sessions, and 3d. the last one.
		if over() {
			c := cause()
			// Recorded before the first row goes: a pass that is interrupted
			// midway has still told the operator.
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
		if _, err := m.dropSession(id, mode, time.Time{}, nil); err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}

// pruneAged deletes rows older than cutoff, oldest first. It reports whether a
// session that still has a summary lost rows.
func (m *Manager) pruneAged(cutoff time.Time) (deleted int, inWindow bool, err error) {
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
				if rec.UnmarshalBinary(v) == nil && rec.WorkSessionID != "" && sums.get(rec.WorkSessionID) != nil {
					continue // inside the window: whole or not at all
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
	sums, err := m.ListWorkSessionSummaries()
	if err != nil {
		return 0, 0, err
	}
	sort.Slice(sums, func(i, j int) bool { return sums[i].LastActivityAt.Before(sums[j].LastActivityAt) })

	for len(sums) > 1 && over() {
		if _, err := m.dropSession(sums[0].ID, removalSession, time.Time{}, st); err != nil {
			return removed, trimmed, err
		}
		removed++
		sums = sums[1:]
	}
	if len(sums) == 1 && over() {
		trimmed, err = m.trimSession(sums[0].ID, st, over)
	}
	return removed, trimmed, err
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
			if len(keys) <= 1 {
				return nil // only the newest row is left
			}
			keys = keys[:len(keys)-1]
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
	})
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
