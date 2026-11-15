package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
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

	// bodiesStrippedThroughKey remembers how far body stripping has gone, so a
	// later pass does not re-read rows it already stripped (MetaBucket).
	bodiesStrippedThroughKey = "activity_bodies_stripped_through"
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

// measureLog sums the bytes that count against the size cap: the activity rows
// and the three buckets this feature adds (FR-034).
func (m *Manager) measureLog() (retentionState, error) {
	var st retentionState
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		n, size := bucketBytes(tx.Bucket([]byte(ActivityRecordsBucket)))
		st.rows, st.bytes = n, size
		for _, name := range []string{WorkSessionsBucket, WorkSessionRowsBucket, UpstreamEventsByServerBucket} {
			_, s := bucketBytes(tx.Bucket([]byte(name)))
			st.bytes += s
		}
		return nil
	})
	return st, err
}

// txSummaries caches the summaries one transaction touches and writes them
// back once.
type txSummaries struct {
	bucket  *bbolt.Bucket
	cache   map[string]*WorkSessionSummary
	missing map[string]bool
}

func newTxSummaries(tx *bbolt.Tx) *txSummaries {
	return &txSummaries{bucket: tx.Bucket([]byte(WorkSessionsBucket)), cache: map[string]*WorkSessionSummary{}, missing: map[string]bool{}}
}

func (t *txSummaries) get(id string) *WorkSessionSummary {
	if t.bucket == nil || id == "" || t.missing[id] {
		return nil
	}
	if s, ok := t.cache[id]; ok {
		return s
	}
	s, err := loadSummary(t.bucket, id)
	if err != nil || s == nil {
		t.missing[id] = true
		return nil
	}
	t.cache[id] = s
	return s
}

func (t *txSummaries) flush() error {
	for _, s := range t.cache {
		if err := storeSummary(t.bucket, s); err != nil {
			return err
		}
	}
	return nil
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
		st, err := m.measureLog()
		if err != nil {
			return res, err
		}
		targetRows := 0
		if p.MaxRecords > 0 && st.rows > p.MaxRecords {
			targetRows = int(float64(p.MaxRecords) * 0.9)
		}
		over := func() bool {
			return (p.MaxBytes > 0 && st.bytes > p.MaxBytes) || (targetRows > 0 && st.rows > targetRows)
		}
		cause := func() string {
			if p.MaxBytes > 0 && st.bytes > p.MaxBytes {
				return ShortfallSizeCap
			}
			return ShortfallRecordCap
		}

		// 3a. Bodies. Only the size cap is helped by this.
		if p.MaxBytes > 0 && st.bytes > p.MaxBytes {
			n, err := m.stripBodies(&st, p.MaxBytes)
			if err != nil {
				return res, err
			}
			res.BodiesStripped = n
		}
		// 3b. Rows outside the window.
		if over() {
			n, err := m.pruneOutsideWindow(&st, over)
			if err != nil {
				return res, err
			}
			res.RowsOutsideWindow = n
		}
		// 3c. Whole sessions, and 3d. the last one.
		if over() {
			c := cause()
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
	for _, id := range expired {
		if err := m.dropSessionIndex(id, false, nil); err != nil {
			return 0, err
		}
	}
	return len(expired), nil
}

// dropSessionIndex removes a session's index entries and its summary, in
// batches. With rows it also removes the activity rows themselves.
func (m *Manager) dropSessionIndex(id string, rows bool, st *retentionState) error {
	prefix := workSessionRowPrefix(id)
	for {
		more := false
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			idx := tx.Bucket([]byte(WorkSessionRowsBucket))
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
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
				if rows && acts != nil {
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
			if !more {
				if sums := tx.Bucket([]byte(WorkSessionsBucket)); sums != nil {
					if v := sums.Get([]byte(id)); v != nil && st != nil {
						st.bytes -= int64(len(id) + len(v))
					}
					return sums.Delete([]byte(id))
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
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
			return sums.flush()
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

// stripBodies removes Arguments and Response from the oldest rows that still
// have them until the log is within maxBytes or every row has been visited.
// Every other field of a row is kept (FR-032).
func (m *Manager) stripBodies(st *retentionState, maxBytes int64) (int, error) {
	stripped := 0
	for st.bytes > maxBytes {
		visited, end := 0, false
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
			if err != nil {
				return err
			}
			if acts == nil {
				end = true
				return nil
			}
			rows := tx.Bucket([]byte(WorkSessionRowsBucket))
			c := acts.Cursor()
			var k, v []byte
			if from := meta.Get([]byte(bodiesStrippedThroughKey)); from != nil {
				k, v = c.Seek(from)
				if k != nil && bytes.Equal(k, from) {
					k, v = c.Next()
				}
			} else {
				k, v = c.First()
			}
			type rewrite struct {
				k, v []byte
				ws   string
			}
			var writes []rewrite
			var last []byte
			freed := int64(0)
			for ; k != nil && visited < retentionBatch && st.bytes-freed > maxBytes; k, v = c.Next() {
				visited++
				last = append(last[:0], k...)
				var rec ActivityRecord
				if rec.UnmarshalBinary(v) != nil || (len(rec.Arguments) == 0 && rec.Response == "") {
					continue
				}
				rec.Arguments, rec.Response, rec.BodiesRemoved = nil, "", true
				data, err := rec.MarshalBinary()
				if err != nil || len(data) >= len(v) {
					continue
				}
				freed += int64(len(v) - len(data))
				writes = append(writes, rewrite{append([]byte(nil), k...), data, rec.WorkSessionID})
			}
			if k == nil {
				end = true
			}
			for _, w := range writes {
				if err := acts.Put(w.k, w.v); err != nil {
					return err
				}
				if w.ws != "" && rows != nil {
					ik := workSessionRowKey(w.ws, w.k)
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
			if last != nil {
				return meta.Put([]byte(bodiesStrippedThroughKey), last)
			}
			return nil
		})
		if err != nil {
			return stripped, err
		}
		if end || visited == 0 {
			break
		}
	}
	return stripped, nil
}

// pruneOutsideWindow deletes, oldest first, rows that belong to no session
// with a summary: rows with no work session, and rows of sessions older than
// the window. Rows of a session inside the window are skipped.
func (m *Manager) pruneOutsideWindow(st *retentionState, over func() bool) (int, error) {
	deleted := 0
	var resume []byte
	for over() {
		visited, end := 0, false
		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if acts == nil {
				end = true
				return nil
			}
			sums := newTxSummaries(tx)
			type kv struct{ k, v []byte }
			var doomed []kv
			pending := retentionState{}
			c := acts.Cursor()
			var k, v []byte
			if resume == nil {
				k, v = c.First()
			} else {
				k, v = c.Seek(resume)
			}
			// The newest row is never removed: the log is not emptied.
			lastKey, _ := acts.Cursor().Last()
			for ; k != nil && visited < retentionBatch; k, v = c.Next() {
				if !overAfter(st, pending, over) {
					break
				}
				visited++
				if bytes.Equal(k, lastKey) {
					continue
				}
				var rec ActivityRecord
				if rec.UnmarshalBinary(v) == nil && rec.WorkSessionID != "" && sums.get(rec.WorkSessionID) != nil {
					continue // inside the window: whole or not at all
				}
				doomed = append(doomed, kv{append([]byte(nil), k...), append([]byte(nil), v...)})
				pending.rows++
				pending.bytes += int64(len(k) + len(v))
			}
			if k == nil {
				end = true
			} else {
				resume = append([]byte(nil), k...)
			}
			for _, d := range doomed {
				freed, _, err := deleteRowTx(tx, sums, d.k, d.v)
				if err != nil {
					return err
				}
				st.rows--
				st.bytes -= freed
				deleted++
			}
			return sums.flush()
		})
		if err != nil {
			return deleted, err
		}
		if end || visited == 0 {
			break
		}
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
		if err := m.dropSessionIndex(sums[0].ID, true, st); err != nil {
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
			return sums.flush()
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
