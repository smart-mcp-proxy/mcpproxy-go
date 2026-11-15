package storage

import (
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

// Spec 124 FR-014, FR-018: rows written before this feature carry a
// work-session id (Spec 082) but have no index entry and no summary. One pass
// at first start builds both.

const (
	// backfillBatch bounds the keys one transaction reads, so the pass never
	// holds the database's single writer for long (FR-038).
	backfillBatch = 1000

	// companionWindow is how far apart a tool_call row and the blocking
	// policy_decision of the same attempt can be. Both are written by one
	// handler, milliseconds apart.
	companionWindow = 5 * time.Second
)

type backfillCall struct {
	workSession, requestID, server, tool string
	at                                   time.Time
}

func (c backfillCall) same(r *ActivityRecord) bool {
	return c.workSession == r.WorkSessionID && c.requestID == r.RequestID &&
		c.server == r.ServerName && c.tool == r.ToolName
}

type backfillPending struct {
	key []byte
	rec *ActivityRecord
}

// companionOf reports whether a held blocking decision is the companion of
// one of the tool_call rows the pass has seen near it.
func companionOf(calls []backfillCall, rec *ActivityRecord) bool {
	for _, cl := range calls {
		if !cl.same(rec) {
			continue
		}
		d := cl.at.Sub(rec.Timestamp)
		if d < 0 {
			d = -d
		}
		if d <= companionWindow {
			return true
		}
	}
	return false
}

// workSessionBackfillDone reports whether the one-time pass has completed.
func (m *Manager) workSessionBackfillDone() bool {
	done := false
	_ = m.db.db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket([]byte(MetaBucket)); b != nil {
			done = b.Get([]byte(workSessionBackfillDoneKey)) != nil
		}
		return nil
	})
	return done
}

// BackfillWorkSessions indexes every row that has a work-session id and no
// index entry, and folds it into its session's summary. It returns the number
// of rows indexed.
//
// The pass is safe while calls are being recorded: a live row indexes itself
// in SaveActivity, and the index-entry check here skips it, so every row is
// counted exactly once whichever side gets to it first (FR-018). It is
// idempotent for the same reason, and a completion marker stops later starts
// from walking the log again.
//
// Rows of this age have no Companion mark (Spec 124 D2). A blocking
// policy_decision is therefore paired by shape: it is the companion of a
// tool_call row of the same work session, request id, server and tool that
// lies within companionWindow of it, before or after, and is then not counted.
// Every such decision is held back until the pass has read companionWindow
// past it, and its role is decided when it is written. Only decision-to-call
// pairs are matched: two tool_call rows that share a request id (old limiter
// sheds stamped the transport id) each stay counted. The decided role is
// stored in the index, so a later rebuild never has to decide again.
//
// No transaction indexes more than backfillBatch rows, held decisions
// included (FR-038).
//
// stop aborts the pass between batches; the marker is then not written and the
// next start resumes (already indexed rows are skipped).
func (m *Manager) BackfillWorkSessions(stop <-chan struct{}) (int, error) {
	if m.workSessionBackfillDone() {
		return 0, nil
	}

	var (
		next    []byte // first key of the next batch; nil = from the start
		atEnd   bool   // the whole log has been read; only held decisions remain
		calls   []backfillCall
		pending []backfillPending
		indexed int
		done    bool
	)

	for !done {
		select {
		case <-stop:
			return indexed, nil
		default:
		}

		err := m.db.db.Update(func(tx *bbolt.Tx) error {
			acts := tx.Bucket([]byte(ActivityRecordsBucket))
			if acts == nil {
				done = true
				return nil
			}
			rows, err := tx.CreateBucketIfNotExists([]byte(WorkSessionRowsBucket))
			if err != nil {
				return err
			}

			// writes counts the rows this transaction has indexed, whatever
			// path indexed them.
			writes := 0
			index := func(key []byte, rec *ActivityRecord, role AttemptRole) error {
				if rows.Get(workSessionRowKey(rec.WorkSessionID, key)) != nil {
					return nil
				}
				writes++
				indexed++
				return indexActivityTx(tx, rec, key, role)
			}
			// settle writes held decisions the pass has read far enough past
			// (or any of them once the log has been read), up to the
			// transaction's budget. A decision that has a tool_call near it
			// is its companion and is not counted. It reports whether
			// decisions that are due could not be written in this
			// transaction.
			settle := func(now time.Time, all bool) (bool, error) {
				kept := pending[:0]
				stillDue := false
				for _, p := range pending {
					due := all || now.Sub(p.rec.Timestamp) > companionWindow
					if !due {
						kept = append(kept, p)
						continue
					}
					if writes >= backfillBatch {
						stillDue = true
						kept = append(kept, p)
						continue
					}
					role := AttemptRoleOf(p.rec)
					if companionOf(calls, p.rec) {
						role = AttemptRole{}
					}
					if err := index(p.key, p.rec, role); err != nil {
						return false, err
					}
				}
				pending = kept
				return stillDue, nil
			}
			finish := func() error {
				stillDue, err := settle(time.Time{}, true)
				if err != nil || stillDue {
					return err // more than one transaction's worth: the next one goes on
				}
				meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
				if err != nil {
					return err
				}
				done = true
				return meta.Put([]byte(workSessionBackfillDoneKey), []byte(time.Now().UTC().Format(time.RFC3339)))
			}
			if atEnd {
				return finish()
			}

			c := acts.Cursor()
			var k, v []byte
			if next == nil {
				k, v = c.First()
			} else {
				k, v = c.Seek(next)
			}
			read := 0
			for ; k != nil && read < backfillBatch; k, v = c.Next() {
				// The cursor's key and value are valid only until the next
				// write in this transaction: everything needed later is
				// decoded or copied now, before any write.
				key := append([]byte(nil), k...)
				rec := &ActivityRecord{}
				if err := rec.UnmarshalBinary(v); err != nil || rec.WorkSessionID == "" {
					read++
					continue
				}
				ts := rec.Timestamp

				// Decisions the pass has moved past are written before this
				// row is taken. When the budget is used up, this row is left
				// for the next transaction.
				stillDue, err := settle(ts, false)
				if err != nil {
					return err
				}
				if stillDue || writes >= backfillBatch {
					next = key
					return nil
				}
				read++

				// Calls are remembered for twice the window: a decision is
				// written one window after it, and its call may lie one
				// window before it.
				live := calls[:0]
				for _, cl := range calls {
					if ts.Sub(cl.at) <= 2*companionWindow {
						live = append(live, cl)
					}
				}
				calls = live

				switch {
				case rec.Type == ActivityTypePolicyDecision && isBlockingDecision(rec.Status) && !rec.Companion && rec.RequestID != "":
					if rows.Get(workSessionRowKey(rec.WorkSessionID, key)) == nil {
						pending = append(pending, backfillPending{key: key, rec: rec})
					}
				default:
					if rec.Type == ActivityTypeToolCall && rec.RequestID != "" {
						calls = append(calls, backfillCall{rec.WorkSessionID, rec.RequestID, rec.ServerName, rec.ToolName, ts})
					}
					if err := index(key, rec, AttemptRoleOf(rec)); err != nil {
						return err
					}
				}
				// The cursor is re-positioned after the write above, so the
				// next step does not depend on a key read before it.
				if k, _ = c.Seek(key); k == nil {
					break
				}
			}

			if k == nil {
				atEnd = true
				return finish()
			}
			next = append([]byte(nil), k...)
			return nil
		})
		if err != nil {
			return indexed, fmt.Errorf("work session backfill: %w", err)
		}
	}

	if indexed > 0 && m.logger != nil {
		m.logger.Infow("Built work-session summaries for existing activity rows", "rows", indexed)
	}
	return indexed, nil
}
