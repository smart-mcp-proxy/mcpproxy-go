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
// A decision is held back until the pass has read companionWindow past it.
// Only decision-to-call pairs are matched: two tool_call rows that share a
// request id (old limiter sheds stamped the transport id) each stay counted.
// The decided role is stored in the index, so a later rebuild never has to
// decide again.
//
// stop aborts the pass between batches; the marker is then not written and the
// next start resumes (already indexed rows are skipped).
func (m *Manager) BackfillWorkSessions(stop <-chan struct{}) (int, error) {
	if m.workSessionBackfillDone() {
		return 0, nil
	}

	var (
		next    []byte // first key of the next batch; nil = from the start
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

			index := func(key []byte, rec *ActivityRecord, role AttemptRole) error {
				if rows.Get(workSessionRowKey(rec.WorkSessionID, key)) != nil {
					return nil
				}
				indexed++
				return indexActivityTx(tx, rec, key, role)
			}
			// writes counts the rows this transaction has indexed. Both the
			// rows read and the held decisions settled count against
			// backfillBatch, so no transaction writes more than that (FR-038).
			writes := 0
			// settle writes held decisions the pass has read far enough past
			// (or any of them at the end) as counted, sole rows, up to the
			// transaction's budget. It reports whether some are still due.
			settle := func(now time.Time, all bool) (bool, error) {
				kept := pending[:0]
				remaining := false
				for _, p := range pending {
					due := all || now.Sub(p.rec.Timestamp) > companionWindow
					if due && writes < backfillBatch {
						writes++
						if err := index(p.key, p.rec, AttemptRoleOf(p.rec)); err != nil {
							return false, err
						}
						continue
					}
					remaining = remaining || due
					kept = append(kept, p)
				}
				pending = kept
				return remaining, nil
			}

			c := acts.Cursor()
			var k, v []byte
			if next == nil {
				k, v = c.First()
			} else {
				k, v = c.Seek(next)
			}
			read := 0
			for ; k != nil && read < backfillBatch && writes < backfillBatch; k, v = c.Next() {
				rec := &ActivityRecord{}
				if err := rec.UnmarshalBinary(v); err != nil || rec.WorkSessionID == "" {
					read++
					continue
				}
				key := append([]byte(nil), k...)
				ts := rec.Timestamp

				// Decisions the pass has moved past are settled before this
				// row is taken. If the budget ran out first, the row is left
				// for the next transaction.
				stillDue, err := settle(ts, false)
				if err != nil {
					return err
				}
				if stillDue {
					break
				}
				read++
				writes++
				// Forget calls the pass has moved past.
				live := calls[:0]
				for _, cl := range calls {
					if ts.Sub(cl.at) <= companionWindow {
						live = append(live, cl)
					}
				}
				calls = live

				switch {
				case rec.Type == ActivityTypeToolCall && rec.RequestID != "":
					// A held decision of this attempt is its companion.
					kept := pending[:0]
					for _, p := range pending {
						if (backfillCall{rec.WorkSessionID, rec.RequestID, rec.ServerName, rec.ToolName, ts}).same(p.rec) {
							if err := index(p.key, p.rec, AttemptRole{}); err != nil {
								return err
							}
							continue
						}
						kept = append(kept, p)
					}
					pending = kept
					calls = append(calls, backfillCall{rec.WorkSessionID, rec.RequestID, rec.ServerName, rec.ToolName, ts})
					if err := index(key, rec, AttemptRoleOf(rec)); err != nil {
						return err
					}

				case rec.Type == ActivityTypePolicyDecision && isBlockingDecision(rec.Status) && !rec.Companion && rec.RequestID != "":
					if rows.Get(workSessionRowKey(rec.WorkSessionID, key)) != nil {
						continue // already indexed by a live write
					}
					paired := false
					for _, cl := range calls {
						if cl.same(rec) {
							paired = true
							break
						}
					}
					if paired {
						if err := index(key, rec, AttemptRole{}); err != nil {
							return err
						}
					} else {
						pending = append(pending, backfillPending{key: key, rec: rec})
					}

				default:
					if err := index(key, rec, AttemptRoleOf(rec)); err != nil {
						return err
					}
				}
			}

			if k == nil {
				stillDue, err := settle(time.Time{}, true)
				if err != nil {
					return err
				}
				if stillDue {
					// More held decisions than one transaction may write: the
					// next one starts at the end of the log and settles on.
					next = append([]byte(nil), 0xff)
					return nil
				}
				meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
				if err != nil {
					return err
				}
				done = true
				return meta.Put([]byte(workSessionBackfillDoneKey), []byte(time.Now().UTC().Format(time.RFC3339)))
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
