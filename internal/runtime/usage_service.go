package runtime

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 069 A2: ActivityService orchestration for the usage aggregate.
//
// The ActivityService goroutine owns the aggregate: it folds each persisted
// tool_call into it (Apply, in handleEvent), serves immutable snapshots to
// readers, and persists the snapshot periodically + on shutdown. On a cold
// start it loads the last persisted snapshot, or rebuilds via a single
// full-scan when none exists.

// DefaultUsagePersistInterval is the default snapshot flush cadence.
const DefaultUsagePersistInterval = 30 * time.Second

// encodeUsageAggregate serializes the aggregate snapshot for persistence.
func encodeUsageAggregate(a *UsageAggregate) ([]byte, error) {
	return json.Marshal(a)
}

// decodeUsageAggregate restores a persisted snapshot, preserving constructed
// defaults for any maps absent from the blob.
func decodeUsageAggregate(data []byte) (*UsageAggregate, error) {
	agg := newUsageAggregate()
	if err := json.Unmarshal(data, agg); err != nil {
		return nil, err
	}
	if agg.Tools == nil {
		agg.Tools = map[string]*ToolUsage{}
	}
	if agg.Buckets == nil {
		agg.Buckets = map[int64]*TimeBucket{}
	}
	return agg, nil
}

// UsageSnapshot returns the latest immutable usage aggregate snapshot. Safe for
// concurrent readers (the A3 endpoint); never blocks. Returns nil if usage
// tracking was never initialized.
func (s *ActivityService) UsageSnapshot() *UsageAggregate {
	if s.usage == nil {
		return nil
	}
	return s.usage.Snapshot()
}

// SetUsagePersistInterval updates the snapshot flush cadence. Hot-reloadable:
// the flush loop re-reads this value each cycle.
func (s *ActivityService) SetUsagePersistInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	s.usagePersistIntervalNs.Store(int64(d))
}

func (s *ActivityService) usagePersistInterval() time.Duration {
	ns := s.usagePersistIntervalNs.Load()
	if ns <= 0 {
		return DefaultUsagePersistInterval
	}
	return time.Duration(ns)
}

// initUsageFromStorage loads the persisted snapshot, or rebuilds the aggregate
// via exactly one full-scan when no snapshot exists. Called once before the
// event loop starts.
func (s *ActivityService) initUsageFromStorage() {
	if s.usage == nil {
		s.usage = newUsageStore()
	}

	data, err := s.storage.LoadUsageSnapshot()
	if err != nil {
		s.logger.Warn("Failed to load usage snapshot; rebuilding from scan", zap.Error(err))
	} else if len(data) > 0 {
		if agg, derr := decodeUsageAggregate(data); derr != nil {
			s.logger.Warn("Failed to decode usage snapshot; rebuilding from scan", zap.Error(derr))
		} else {
			s.usage.Replace(agg)
			replayed := s.replayActivitiesSince(agg.UpdatedAt)
			s.logger.Info("Loaded usage aggregate from persisted snapshot",
				zap.Int("tools", len(agg.Tools)),
				zap.Time("snapshot_updated_at", agg.UpdatedAt),
				zap.Int("records_replayed", replayed))
			return
		}
	}

	// Cold start (or unreadable snapshot): rebuild with a single full-scan.
	agg := newUsageAggregate()
	scanned := 0
	if serr := s.storage.ScanAllActivities(func(rec *storage.ActivityRecord) {
		scanned++
		agg.Apply(rec)
	}); serr != nil {
		s.logger.Error("Failed to rebuild usage aggregate from scan", zap.Error(serr))
	}
	s.usage.Replace(agg)
	s.logger.Info("Rebuilt usage aggregate from activity scan",
		zap.Int("records_scanned", scanned),
		zap.Int("tools", len(agg.Tools)))
}

// replayActivitiesSince folds every activity record persisted AFTER the loaded
// snapshot was stamped into the aggregate, and reports how many it applied.
//
// Without it, restoring a snapshot silently dropped every record written since
// the last 30s flush. On a clean shutdown that tail is small (the flush-on-stop
// covers it); on a crash or a kill it is up to 30 seconds of calls, and the
// loss is PERMANENT — the aggregate is never rebuilt again, so the usage
// histogram drifts further from the activity list with every unclean stop.
//
// Safe to run on every start because the write path persists a record BEFORE
// folding it in (ActivityService.handleToolCallCompleted), so "saved but not
// counted" is the only inconsistency possible, never "counted but not saved".
// The bound is strictly greater than the stamp, so nothing already in the
// snapshot can be applied a second time.
func (s *ActivityService) replayActivitiesSince(since time.Time) int {
	if since.IsZero() {
		// Snapshot from a build that did not stamp its aggregate: there is no
		// safe lower bound, and replaying from the beginning would double every
		// record it already contains. Keep the snapshot as loaded.
		s.logger.Warn("Persisted usage snapshot has no timestamp; skipping catch-up replay")
		return 0
	}
	replayed := 0
	if err := s.storage.ScanActivitiesAfter(since, func(rec *storage.ActivityRecord) {
		replayed++
		s.usage.Apply(rec)
	}); err != nil {
		s.logger.Warn("Failed to replay activity records into the usage aggregate",
			zap.Error(err), zap.Time("since", since))
	}
	return replayed
}

// persistUsage flushes the current snapshot to storage. No-op if usage tracking
// is uninitialized or no snapshot exists yet.
func (s *ActivityService) persistUsage() {
	if s.usage == nil {
		return
	}
	snap := s.usage.Snapshot()
	if snap == nil {
		return
	}
	data, err := encodeUsageAggregate(snap)
	if err != nil {
		s.logger.Warn("Failed to encode usage snapshot for persistence", zap.Error(err))
		return
	}
	if err := s.storage.SaveUsageSnapshot(data); err != nil {
		s.logger.Warn("Failed to persist usage snapshot", zap.Error(err))
	}
}

// runUsageFlushLoop periodically persists the usage snapshot. It re-reads the
// configured interval each cycle so config hot-reload takes effect without a
// restart. It stops on ctx cancellation; the final flush-on-shutdown is done by
// Start.
func (s *ActivityService) runUsageFlushLoop(ctx context.Context) {
	for {
		timer := time.NewTimer(s.usagePersistInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.persistUsage()
		}
	}
}
