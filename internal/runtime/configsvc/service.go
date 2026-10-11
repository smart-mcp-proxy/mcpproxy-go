package configsvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UpdateType describes the nature of a configuration update.
type UpdateType string

const (
	UpdateTypeReload UpdateType = "reload" // Config reloaded from disk
	UpdateTypeModify UpdateType = "modify" // In-memory modification
	UpdateTypeInit   UpdateType = "init"   // Initial configuration load
)

// Update represents a configuration change notification.
type Update struct {
	Snapshot  *Snapshot
	Type      UpdateType
	ChangedAt time.Time
	Source    string // e.g., "file_watcher", "api_request", "initial_load"
}

// Service manages configuration state with lock-free reads and atomic updates.
//
// Key design principles:
// 1. Reads are lock-free via atomic.Value (no mutex contention)
// 2. Updates are serialized through a single mutex
// 3. Subscribers receive updates via buffered channels
// 4. File I/O is decoupled from snapshot reads
type Service struct {
	logger *zap.Logger

	// Atomic snapshot for lock-free reads
	snapshot atomic.Value // *Snapshot

	// Update coordination
	updateMu    sync.Mutex
	version     int64
	subscribers []chan Update
	subMu       sync.RWMutex

	// prePublishObservers run on the exact *Config that is about to be
	// stored — while nothing else can observe it. Read-only: they build state
	// keyed on that pointer (Spec 105's profile index) so it is ready before
	// the first reader can capture the snapshot.
	//
	// Spec 116 removed the single pre-publish hook the #937 admission gate
	// used to own: admission now runs in step 2 of Runtime.Commit, on the
	// config before it is prepared, so every published config is already
	// gated and nothing under updateMu reads storage (review R1.7).
	observersMu         sync.RWMutex
	prePublishObservers []func(*config.Config)
}

// NewService creates a new configuration service with the given initial config.
func NewService(initialConfig *config.Config, configPath string, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}

	svc := &Service{
		logger:      logger,
		version:     0,
		subscribers: make([]chan Update, 0),
	}

	// Create initial snapshot
	snapshot := &Snapshot{
		Config:    initialConfig,
		Path:      configPath,
		Version:   0,
		Timestamp: time.Now(),
	}

	svc.snapshot.Store(snapshot)

	logger.Info("ConfigService initialized",
		zap.String("config_path", configPath),
		zap.Int64("version", snapshot.Version))

	return svc
}

// Current returns the current configuration snapshot.
// This is a lock-free operation that returns an immutable snapshot.
func (s *Service) Current() *Snapshot {
	return s.snapshot.Load().(*Snapshot)
}

// AddPrePublishObserver registers a read-only observer of every configuration
// on its way into the snapshot. Observers run inside the update, after the
// pre-publish hook has produced the final config and before the snapshot is
// stored or any subscriber is notified — so they see the exact *Config that
// will be published, at a moment nothing else can. They must not mutate it,
// and because they run under the update mutex they must be cheap (an index
// build of one insertion per entry, not I/O).
//
// Observers run with the update mutex held and MUST NOT publish: a call to
// Update, UpdateIfCurrent or Publish from inside an observer is a re-entrant
// publish and deadlocks by design. Registering a further observer from inside one is allowed — the
// list is not locked while observers run — and that observer first runs on
// the next publication.
//
// Nil observers and a nil service are ignored. Safe to call at any time,
// including from an observer.
func (s *Service) AddPrePublishObserver(observe func(*config.Config)) {
	if s == nil || observe == nil {
		return
	}
	s.observersMu.Lock()
	defer s.observersMu.Unlock()
	s.prePublishObservers = append(s.prePublishObservers, observe)
}

// runPrePublishObservers runs every observer registered when it is called on
// cfg (called with updateMu held, before the snapshot is stored). The list is
// copied under the read lock and released before any observer runs, so an
// observer may register another without deadlocking.
func (s *Service) runPrePublishObservers(cfg *config.Config) {
	s.observersMu.RLock()
	observers := slices.Clone(s.prePublishObservers)
	s.observersMu.RUnlock()
	for _, observe := range observers {
		observe(cfg)
	}
}

// Update atomically updates the configuration and notifies all subscribers.
// It is Prepare+Publish without replacement metadata; after Spec 116 only the
// runtime's commit (and tests) call it.
func (s *Service) Update(newConfig *config.Config, updateType UpdateType, source string) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	p, err := s.prepareLocked(PrepareInput{Config: newConfig, Source: source, Type: updateType})
	if err != nil {
		return err
	}
	s.publishLocked(p)
	return nil
}

// UpdateIfCurrent atomically publishes newConfig only when expected is still
// the active configuration pointer. It closes the check-then-update race for
// derived snapshots: a newer config must never be overwritten by a stale copy
// prepared before it was committed.
func (s *Service) UpdateIfCurrent(expected, newConfig *config.Config, updateType UpdateType, source string) (bool, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.Current().Config != expected {
		return false, nil
	}
	p, err := s.prepareLocked(PrepareInput{Config: newConfig, Source: source, Type: updateType})
	if err != nil {
		return false, err
	}
	s.publishLocked(p)
	return true, nil
}

// ErrConcurrentPublish reports that a prepared snapshot was published after
// another publication moved the version it was prepared against. Under the
// runtime's commit lock this is an invariant violation.
var ErrConcurrentPublish = errors.New("configsvc: concurrent publish")

// Prepare computes the next snapshot for in.Config against the current one
// without making it visible (Spec 116 two-phase publish). It assigns the
// commit generation G = version+1, the per-server generations and the change
// set. Publish makes it current. The runtime's commit lock serializes
// Prepare/Publish pairs; Publish refuses a snapshot whose base version moved.
func (s *Service) Prepare(in PrepareInput) (*PreparedSnapshot, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	return s.prepareLocked(in)
}

// Publish makes a prepared snapshot current: it runs the pre-publish
// observers, stores the snapshot and notifies subscribers.
func (s *Service) Publish(p *PreparedSnapshot) (*Snapshot, error) {
	if p == nil || p.snap == nil {
		return nil, fmt.Errorf("configsvc: nil prepared snapshot")
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.version != p.base {
		return nil, fmt.Errorf("%w: prepared against version %d, current %d", ErrConcurrentPublish, p.base, s.version)
	}
	s.publishLocked(p)
	return p.snap, nil
}

// SeedVersion raises the version counter to v so the next commit generation
// is greater than every generation persisted by an earlier process (Spec 116
// D6). It never lowers the counter.
func (s *Service) SeedVersion(v int64) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if v > s.version {
		s.version = v
	}
}

func (s *Service) prepareLocked(in PrepareInput) (*PreparedSnapshot, error) {
	if in.Config == nil {
		return nil, fmt.Errorf("configsvc: nil config")
	}
	current := s.Current()
	g := s.version + 1
	gens, changes, err := computeGenerations(current, in, g)
	if err != nil {
		return nil, err
	}
	updateType := in.Type
	if updateType == "" {
		updateType = UpdateTypeModify
	}
	return &PreparedSnapshot{
		base:       s.version,
		generation: g,
		updateType: updateType,
		source:     in.Source,
		Changes:    changes,
		snap: &Snapshot{
			Config:            in.Config,
			Path:              current.Path,
			Version:           g,
			Timestamp:         time.Now(),
			ServerGenerations: gens,
		},
	}, nil
}

func (s *Service) publishLocked(p *PreparedSnapshot) {
	s.runPrePublishObservers(p.snap.Config)

	current := s.Current()
	s.version = p.generation
	snap := p.snap
	snap.Path = current.Path
	snap.Timestamp = time.Now()
	s.snapshot.Store(snap)

	s.logger.Info("Configuration updated",
		zap.String("type", string(p.updateType)),
		zap.String("source", p.source),
		zap.Int64("old_version", current.Version),
		zap.Int64("new_version", snap.Version),
		zap.Int("server_count", snap.ServerCount()))

	s.notifySubscribers(Update{
		Snapshot:  snap,
		Type:      p.updateType,
		ChangedAt: snap.Timestamp,
		Source:    p.source,
	})
}

// UpdatePath updates the configuration file path.
func (s *Service) UpdatePath(newPath string) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	current := s.Current()
	newSnapshot := &Snapshot{
		Config:            current.Config,
		Path:              newPath,
		Version:           current.Version,
		Timestamp:         current.Timestamp,
		ServerGenerations: current.ServerGenerations,
	}

	s.snapshot.Store(newSnapshot)

	s.logger.Debug("Configuration path updated",
		zap.String("old_path", current.Path),
		zap.String("new_path", newPath))
}

// SaveToFile persists the current configuration to disk.
// This operation blocks on file I/O but doesn't hold any locks.
func (s *Service) SaveToFile() error {
	snapshot := s.Current()
	if snapshot.Path == "" {
		return fmt.Errorf("no configuration file path set")
	}

	s.logger.Debug("Saving configuration to disk",
		zap.String("path", snapshot.Path),
		zap.Int64("version", snapshot.Version))

	if err := config.SaveConfig(snapshot.Config, snapshot.Path); err != nil {
		return fmt.Errorf("failed to save config to %s: %w", snapshot.Path, err)
	}

	return nil
}

// Subscribe returns a channel that receives configuration updates.
// The channel has a buffer size of 10 to prevent blocking publishers.
// The caller should stop reading from the channel and call Unsubscribe when done.
func (s *Service) Subscribe(ctx context.Context) <-chan Update {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	ch := make(chan Update, 10)
	s.subscribers = append(s.subscribers, ch)

	s.logger.Debug("New configuration subscriber",
		zap.Int("total_subscribers", len(s.subscribers)))

	// Send initial snapshot to new subscriber.
	go func() {
		// Best-effort early-out if the subscriber canceled before we ran.
		select {
		case <-ctx.Done():
			s.Unsubscribe(ch)
			return
		default:
		}

		// Deliver the initial snapshot under subMu(R) so Close()/Unsubscribe()
		// (which close ch under the subMu write lock) cannot race or panic this
		// send — a send on a closed channel both data-races and panics. The
		// membership check covers both teardown paths: Close() nils the slice and
		// Unsubscribe() removes this ch. The buffer (cap 10) is empty at subscribe
		// time, so a non-blocking send always reaches a still-live subscriber;
		// staying non-blocking avoids holding the lock across a blocking send.
		s.subMu.RLock()
		defer s.subMu.RUnlock()
		if !s.isSubscribedLocked(ch) {
			return
		}
		select {
		case ch <- Update{
			Snapshot:  s.Current(),
			Type:      UpdateTypeInit,
			ChangedAt: time.Now(),
			Source:    "subscription",
		}:
		default:
		}
	}()

	return ch
}

// isSubscribedLocked reports whether ch is still a live subscriber. Callers must
// hold subMu (read or write).
func (s *Service) isSubscribedLocked(ch chan Update) bool {
	for _, sub := range s.subscribers {
		if sub == ch {
			return true
		}
	}
	return false
}

// Unsubscribe removes a subscriber channel and closes it.
func (s *Service) Unsubscribe(ch <-chan Update) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	for i, sub := range s.subscribers {
		if sub == ch {
			// Remove from slice
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			close(sub)

			s.logger.Debug("Configuration subscriber removed",
				zap.Int("remaining_subscribers", len(s.subscribers)))
			break
		}
	}
}

// notifySubscribers sends an update to all subscribers (called with updateMu held).
func (s *Service) notifySubscribers(update Update) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()

	for _, ch := range s.subscribers {
		select {
		case ch <- update:
			// Successfully sent
		default:
			// Channel is full, log and skip
			s.logger.Warn("Subscriber channel full, dropping update",
				zap.Int64("version", update.Snapshot.Version))
		}
	}
}

// Close cleans up the service and closes all subscriber channels.
func (s *Service) Close() {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	for _, ch := range s.subscribers {
		close(ch)
	}

	s.subscribers = nil

	s.logger.Info("ConfigService closed")
}

// Version returns the current configuration version number.
func (s *Service) Version() int64 {
	return s.Current().Version
}
