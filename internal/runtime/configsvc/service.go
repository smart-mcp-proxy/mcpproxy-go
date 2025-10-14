package configsvc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
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

// Update atomically updates the configuration and notifies all subscribers.
// This operation is serialized to ensure consistency.
func (s *Service) Update(newConfig *config.Config, updateType UpdateType, source string) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	current := s.Current()
	s.version++

	newSnapshot := &Snapshot{
		Config:    newConfig,
		Path:      current.Path,
		Version:   s.version,
		Timestamp: time.Now(),
	}

	// Store the new snapshot atomically
	s.snapshot.Store(newSnapshot)

	s.logger.Info("Configuration updated",
		zap.String("type", string(updateType)),
		zap.String("source", source),
		zap.Int64("old_version", current.Version),
		zap.Int64("new_version", newSnapshot.Version),
		zap.Int("server_count", newSnapshot.ServerCount()))

	// Notify subscribers
	update := Update{
		Snapshot:  newSnapshot,
		Type:      updateType,
		ChangedAt: newSnapshot.Timestamp,
		Source:    source,
	}

	s.notifySubscribers(update)

	return nil
}

// UpdatePath updates the configuration file path.
func (s *Service) UpdatePath(newPath string) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	current := s.Current()
	newSnapshot := &Snapshot{
		Config:    current.Config,
		Path:      newPath,
		Version:   current.Version,
		Timestamp: current.Timestamp,
	}

	s.snapshot.Store(newSnapshot)

	s.logger.Debug("Configuration path updated",
		zap.String("old_path", current.Path),
		zap.String("new_path", newPath))
}

// ReloadFromFile loads configuration from disk and updates the snapshot.
// Returns the new snapshot and any error encountered.
func (s *Service) ReloadFromFile() (*Snapshot, error) {
	current := s.Current()
	if current.Path == "" {
		return nil, fmt.Errorf("no configuration file path set")
	}

	s.logger.Info("Reloading configuration from disk",
		zap.String("path", current.Path))

	// Load config from file (this may block on disk I/O)
	newConfig, err := config.LoadFromFile(current.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", current.Path, err)
	}

	// Update atomically
	if err := s.Update(newConfig, UpdateTypeReload, "file_reload"); err != nil {
		return nil, err
	}

	return s.Current(), nil
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

	// Send initial snapshot to new subscriber
	go func() {
		select {
		case ch <- Update{
			Snapshot:  s.Current(),
			Type:      UpdateTypeInit,
			ChangedAt: time.Now(),
			Source:    "subscription",
		}:
		case <-ctx.Done():
			s.Unsubscribe(ch)
		}
	}()

	return ch
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
