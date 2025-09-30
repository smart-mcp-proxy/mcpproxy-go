package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
)

// Status captures high-level state for API consumers.
type Status struct {
	Phase         string                 `json:"phase"`
	Message       string                 `json:"message"`
	UpstreamStats map[string]interface{} `json:"upstream_stats"`
	ToolsIndexed  int                    `json:"tools_indexed"`
	LastUpdated   time.Time              `json:"last_updated"`
}

// Runtime owns the non-HTTP lifecycle for the proxy process.
type Runtime struct {
	cfg     *config.Config
	cfgPath string
	logger  *zap.Logger

	mu      sync.RWMutex
	running bool

	statusMu sync.RWMutex
	status   Status
	statusCh chan Status

	eventMu   sync.RWMutex
	eventSubs map[chan Event]struct{}

	storageManager  *storage.Manager
	indexManager    *index.Manager
	upstreamManager *upstream.Manager
	cacheManager    *cache.Manager
	truncator       *truncate.Truncator
	secretResolver  *secret.Resolver

	appCtx    context.Context
	appCancel context.CancelFunc
}

// New creates a runtime helper for the given config and prepares core managers.
func New(cfg *config.Config, cfgPath string, logger *zap.Logger) (*Runtime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	storageManager, err := storage.NewManager(cfg.DataDir, logger.Sugar())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage manager: %w", err)
	}

	indexManager, err := index.NewManager(cfg.DataDir, logger)
	if err != nil {
		_ = storageManager.Close()
		return nil, fmt.Errorf("failed to initialize index manager: %w", err)
	}

	// Initialize secret resolver
	secretResolver := secret.NewResolver()

	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver)
	if cfg.Logging != nil {
		upstreamManager.SetLogConfig(cfg.Logging)
	}

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		_ = indexManager.Close()
		_ = storageManager.Close()
		return nil, fmt.Errorf("failed to initialize cache manager: %w", err)
	}

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	appCtx, appCancel := context.WithCancel(context.Background())

	rt := &Runtime{
		cfg:             cfg,
		cfgPath:         cfgPath,
		logger:          logger,
		storageManager:  storageManager,
		indexManager:    indexManager,
		upstreamManager: upstreamManager,
		cacheManager:    cacheManager,
		truncator:       truncator,
		secretResolver:  secretResolver,
		appCtx:          appCtx,
		appCancel:       appCancel,
		status: Status{
			Phase:       "Initializing",
			Message:     "Runtime is initializing...",
			LastUpdated: time.Now(),
		},
		statusCh:  make(chan Status, 10),
		eventSubs: make(map[chan Event]struct{}),
	}

	return rt, nil
}

// Config returns the underlying configuration pointer.
func (r *Runtime) Config() *config.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// ConfigPath returns the tracked config path.
func (r *Runtime) ConfigPath() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfgPath
}

// UpdateConfig replaces the runtime configuration in-place.
func (r *Runtime) UpdateConfig(cfg *config.Config, cfgPath string) {
	r.mu.Lock()
	r.cfg = cfg
	if cfgPath != "" {
		r.cfgPath = cfgPath
	}
	r.mu.Unlock()
}

// UpdateListenAddress mutates the in-memory listen address used by the runtime.
func (r *Runtime) UpdateListenAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("listen address cannot be empty")
	}

	if !strings.Contains(addr, ":") {
		return fmt.Errorf("listen address %q must include a port", addr)
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg == nil {
		return fmt.Errorf("runtime configuration is not available")
	}
	r.cfg.Listen = addr
	return nil
}

// SetRunning records whether the server HTTP layer is active.
func (r *Runtime) SetRunning(running bool) {
	r.mu.Lock()
	r.running = running
	r.mu.Unlock()
}

// IsRunning reports the last known running state.
func (r *Runtime) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// UpdateStatus mutates the status object and notifies subscribers.
func (r *Runtime) UpdateStatus(phase, message string, stats map[string]interface{}, toolsIndexed int) {
	r.statusMu.Lock()
	r.status.Phase = phase
	r.status.Message = message
	r.status.LastUpdated = time.Now()
	r.status.UpstreamStats = stats
	r.status.ToolsIndexed = toolsIndexed
	snapshot := r.status
	r.statusMu.Unlock()

	select {
	case r.statusCh <- snapshot:
	default:
	}

	if r.logger != nil {
		r.logger.Info("Status updated", zap.String("phase", phase), zap.String("message", message))
	}
}

// UpdatePhase gathers runtime metrics and broadcasts a status update.
func (r *Runtime) UpdatePhase(phase, message string) {
	var (
		stats map[string]interface{}
		tools int
	)

	if r.upstreamManager != nil {
		stats = r.upstreamManager.GetStats()
		tools = extractToolCount(stats)
	}

	r.UpdateStatus(phase, message, stats, tools)
}

// StatusSnapshot returns the latest status as a map for API responses.
// The serverRunning parameter should come from the authoritative server running state.
func (r *Runtime) StatusSnapshot(serverRunning bool) map[string]interface{} {
	r.statusMu.RLock()
	status := r.status
	r.statusMu.RUnlock()

	r.mu.RLock()
	listen := ""
	if r.cfg != nil {
		listen = r.cfg.Listen
	}
	r.mu.RUnlock()

	return map[string]interface{}{
		"running":        serverRunning,
		"listen_addr":    listen,
		"phase":          status.Phase,
		"message":        status.Message,
		"upstream_stats": status.UpstreamStats,
		"tools_indexed":  status.ToolsIndexed,
		"last_updated":   status.LastUpdated,
	}
}

// StatusChannel exposes the status updates stream.
func (r *Runtime) StatusChannel() <-chan Status {
	return r.statusCh
}

// CurrentStatus returns a copy of the underlying status struct.
func (r *Runtime) CurrentStatus() Status {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}

// Logger returns the runtime logger.
func (r *Runtime) Logger() *zap.Logger {
	return r.logger
}

// StorageManager exposes the storage manager.
func (r *Runtime) StorageManager() *storage.Manager {
	return r.storageManager
}

// IndexManager exposes the index manager.
func (r *Runtime) IndexManager() *index.Manager {
	return r.indexManager
}

// UpstreamManager exposes the upstream manager.
func (r *Runtime) UpstreamManager() *upstream.Manager {
	return r.upstreamManager
}

// CacheManager exposes the cache manager.
func (r *Runtime) CacheManager() *cache.Manager {
	return r.cacheManager
}

// Truncator exposes the truncator utility.
func (r *Runtime) Truncator() *truncate.Truncator {
	return r.truncator
}

// AppContext returns the long-lived runtime context.
func (r *Runtime) AppContext() context.Context {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.appCtx
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.appCancel != nil {
		r.appCancel()
		r.appCancel = nil
		r.appCtx = context.Background()
	}
	r.mu.Unlock()

	var errs []error

	if r.upstreamManager != nil {
		if err := r.upstreamManager.DisconnectAll(); err != nil {
			errs = append(errs, fmt.Errorf("disconnect upstream servers: %w", err))
			if r.logger != nil {
				r.logger.Error("Failed to disconnect upstream servers", zap.Error(err))
			}
		}
	}

	if r.cacheManager != nil {
		r.cacheManager.Close()
	}

	if r.indexManager != nil {
		if err := r.indexManager.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close index manager: %w", err))
		}
	}

	if r.storageManager != nil {
		if err := r.storageManager.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close storage manager: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func extractToolCount(stats map[string]interface{}) int {
	if stats == nil {
		return 0
	}

	if totalTools, ok := stats["total_tools"].(int); ok {
		return totalTools
	}

	servers, ok := stats["servers"].(map[string]interface{})
	if !ok {
		return 0
	}

	result := 0
	for _, value := range servers {
		serverStats, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		if count, ok := serverStats["tool_count"].(int); ok {
			result += count
		}
	}
	return result
}

// GetSecretResolver returns the secret resolver instance
func (r *Runtime) GetSecretResolver() *secret.Resolver {
	return r.secretResolver
}

// GetCurrentConfig returns the current configuration
func (r *Runtime) GetCurrentConfig() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// GetToolCalls retrieves tool call history with pagination
func (r *Runtime) GetToolCalls(limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get all server identities to aggregate tool calls
	identities, err := r.storageManager.ListServerIdentities()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list server identities: %w", err)
	}

	// Collect tool calls from all servers
	var allCalls []*storage.ToolCallRecord
	for _, identity := range identities {
		calls, err := r.storageManager.GetServerToolCalls(identity.ID, 1000) // Get up to 1000 per server
		if err != nil {
			r.logger.Sugar().Warnw("Failed to get tool calls for server",
				"server_id", identity.ID,
				"error", err)
			continue
		}
		allCalls = append(allCalls, calls...)
	}

	// Sort by timestamp (most recent first)
	sort.Slice(allCalls, func(i, j int) bool {
		return allCalls[i].Timestamp.After(allCalls[j].Timestamp)
	})

	total := len(allCalls)

	// Apply pagination
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	pagedCalls := allCalls[start:end]

	// Convert to contract types
	contractCalls := make([]*contracts.ToolCallRecord, len(pagedCalls))
	for i, call := range pagedCalls {
		contractCalls[i] = &contracts.ToolCallRecord{
			ID:         call.ID,
			ServerID:   call.ServerID,
			ServerName: call.ServerName,
			ToolName:   call.ToolName,
			Arguments:  call.Arguments,
			Response:   call.Response,
			Error:      call.Error,
			Duration:   call.Duration,
			Timestamp:  call.Timestamp,
			ConfigPath: call.ConfigPath,
			RequestID:  call.RequestID,
		}
	}

	return contractCalls, total, nil
}

// GetToolCallByID retrieves a single tool call by ID
func (r *Runtime) GetToolCallByID(id string) (*contracts.ToolCallRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Search through all server tool calls
	identities, err := r.storageManager.ListServerIdentities()
	if err != nil {
		return nil, fmt.Errorf("failed to list server identities: %w", err)
	}

	for _, identity := range identities {
		calls, err := r.storageManager.GetServerToolCalls(identity.ID, 1000)
		if err != nil {
			continue
		}

		for _, call := range calls {
			if call.ID == id {
				return &contracts.ToolCallRecord{
					ID:         call.ID,
					ServerID:   call.ServerID,
					ServerName: call.ServerName,
					ToolName:   call.ToolName,
					Arguments:  call.Arguments,
					Response:   call.Response,
					Error:      call.Error,
					Duration:   call.Duration,
					Timestamp:  call.Timestamp,
					ConfigPath: call.ConfigPath,
					RequestID:  call.RequestID,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("tool call not found: %s", id)
}

// GetServerToolCalls retrieves tool call history for a specific server
func (r *Runtime) GetServerToolCalls(serverName string, limit int) ([]*contracts.ToolCallRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get server config to find its identity
	serverConfig, err := r.storageManager.GetUpstreamServer(serverName)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	serverID := storage.GenerateServerID(serverConfig)

	// Get tool calls for this server
	calls, err := r.storageManager.GetServerToolCalls(serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get server tool calls: %w", err)
	}

	// Convert to contract types
	contractCalls := make([]*contracts.ToolCallRecord, len(calls))
	for i, call := range calls {
		contractCalls[i] = &contracts.ToolCallRecord{
			ID:         call.ID,
			ServerID:   call.ServerID,
			ServerName: call.ServerName,
			ToolName:   call.ToolName,
			Arguments:  call.Arguments,
			Response:   call.Response,
			Error:      call.Error,
			Duration:   call.Duration,
			Timestamp:  call.Timestamp,
			ConfigPath: call.ConfigPath,
			RequestID:  call.RequestID,
		}
	}

	return contractCalls, nil
}
