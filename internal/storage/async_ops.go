package storage

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// Operation represents a queued storage operation
type Operation struct {
	Type     string
	Data     interface{}
	ResultCh chan Result
}

// Result contains the result of a storage operation
type Result struct {
	Error error
	Data  interface{}
}

// AsyncManager handles asynchronous storage operations to prevent deadlocks
type AsyncManager struct {
	logger  *zap.SugaredLogger
	db      *BoltDB
	opQueue chan Operation
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	wg      sync.WaitGroup
}

// NewAsyncManager creates a new async storage manager
func NewAsyncManager(db *BoltDB, logger *zap.SugaredLogger) *AsyncManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &AsyncManager{
		logger:  logger,
		db:      db,
		opQueue: make(chan Operation, 100), // Buffer for 100 operations
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start begins processing storage operations in a dedicated goroutine
func (am *AsyncManager) Start() {
	if am.started {
		return
	}
	am.started = true
	am.wg.Add(1)
	go am.processOperations()
}

// Stop gracefully shuts down the async manager
func (am *AsyncManager) Stop() {
	if !am.started {
		return
	}
	am.cancel()
	am.wg.Wait() // Wait for the goroutine to finish
	am.started = false
}

// processOperations is the main worker loop that processes storage operations
func (am *AsyncManager) processOperations() {
	defer am.wg.Done()
	am.logger.Debug("Storage async manager started")
	defer am.logger.Debug("Storage async manager stopped")

	for {
		select {
		case <-am.ctx.Done():
			// Drain remaining operations before shutting down
			am.drainQueue()
			return
		case op := <-am.opQueue:
			am.executeOperation(op)
		}
	}
}

// drainQueue processes any remaining operations in the queue
func (am *AsyncManager) drainQueue() {
	for {
		select {
		case op := <-am.opQueue:
			am.executeOperation(op)
		default:
			return
		}
	}
}

// executeOperation performs the actual storage operation
func (am *AsyncManager) executeOperation(op Operation) {
	var result Result

	switch op.Type {
	case "enable_server":
		data := op.Data.(EnableServerData)
		result.Error = am.enableServerSync(data.Name, data.Enabled)
	case "quarantine_server":
		data := op.Data.(QuarantineServerData)
		result.Error = am.quarantineServerSync(data.Name, data.Quarantined)
	case "save_server":
		data := op.Data.(*config.ServerConfig)
		result.Error = am.saveServerSync(data)
	case "delete_server":
		data := op.Data.(string)
		result.Error = am.deleteServerSync(data)
	default:
		result.Error = &UnsupportedOperationError{Operation: op.Type}
	}

	// Send result back if a result channel was provided
	if op.ResultCh != nil {
		select {
		case op.ResultCh <- result:
		case <-time.After(5 * time.Second):
			am.logger.Warn("Timeout sending storage operation result", "type", op.Type)
		}
	}
}

// Data structures for different operation types
type EnableServerData struct {
	Name    string
	Enabled bool
}

type QuarantineServerData struct {
	Name        string
	Quarantined bool
}

// UnsupportedOperationError is returned for unknown operation types
type UnsupportedOperationError struct {
	Operation string
}

func (e *UnsupportedOperationError) Error() string {
	return "unsupported storage operation: " + e.Operation
}

// Synchronous implementations that are called by the worker goroutine
func (am *AsyncManager) enableServerSync(name string, enabled bool) error {
	record, err := am.db.GetUpstream(name)
	if err != nil {
		return err
	}
	record.Enabled = enabled
	return am.db.SaveUpstream(record)
}

func (am *AsyncManager) quarantineServerSync(name string, quarantined bool) error {
	record, err := am.db.GetUpstream(name)
	if err != nil {
		return err
	}
	record.Quarantined = quarantined
	record.Updated = time.Now()
	return am.db.SaveUpstream(record)
}

func (am *AsyncManager) saveServerSync(serverConfig *config.ServerConfig) error {
	record := &UpstreamRecord{
		ID:          serverConfig.Name,
		Name:        serverConfig.Name,
		URL:         serverConfig.URL,
		Protocol:    serverConfig.Protocol,
		Command:     serverConfig.Command,
		Args:        serverConfig.Args,
		Env:         serverConfig.Env,
		WorkingDir:  serverConfig.WorkingDir,
		Enabled:     serverConfig.Enabled,
		Quarantined: serverConfig.Quarantined,
		Headers:     serverConfig.Headers,
		Created:     serverConfig.Created,
		Updated:     time.Now(),
	}
	return am.db.SaveUpstream(record)
}

func (am *AsyncManager) deleteServerSync(name string) error {
	return am.db.DeleteUpstream(name)
}

// Queue operation methods that return immediately

// EnableServerAsync queues an enable/disable operation
func (am *AsyncManager) EnableServerAsync(name string, enabled bool) {
	op := Operation{
		Type: "enable_server",
		Data: EnableServerData{Name: name, Enabled: enabled},
	}

	select {
	case am.opQueue <- op:
		am.logger.Debug("Queued enable server operation", "server", name, "enabled", enabled)
	default:
		am.logger.Warn("Storage operation queue full, dropping enable server operation", "server", name)
	}
}

// QuarantineServerAsync queues a quarantine operation
func (am *AsyncManager) QuarantineServerAsync(name string, quarantined bool) {
	op := Operation{
		Type: "quarantine_server",
		Data: QuarantineServerData{Name: name, Quarantined: quarantined},
	}

	select {
	case am.opQueue <- op:
		am.logger.Debug("Queued quarantine server operation", "server", name, "quarantined", quarantined)
	default:
		am.logger.Warn("Storage operation queue full, dropping quarantine server operation", "server", name)
	}
}

// SaveServerAsync queues a save server operation
func (am *AsyncManager) SaveServerAsync(serverConfig *config.ServerConfig) {
	op := Operation{
		Type: "save_server",
		Data: serverConfig,
	}

	select {
	case am.opQueue <- op:
		am.logger.Debug("Queued save server operation", "server", serverConfig.Name)
	default:
		am.logger.Warn("Storage operation queue full, dropping save server operation", "server", serverConfig.Name)
	}
}

// DeleteServerAsync queues a delete server operation
func (am *AsyncManager) DeleteServerAsync(name string) {
	op := Operation{
		Type: "delete_server",
		Data: name,
	}

	select {
	case am.opQueue <- op:
		am.logger.Debug("Queued delete server operation", "server", name)
	default:
		am.logger.Warn("Storage operation queue full, dropping delete server operation", "server", name)
	}
}

// Synchronous operations with result channels for when confirmation is needed

// EnableServerSync queues an enable/disable operation and waits for confirmation
func (am *AsyncManager) EnableServerSync(name string, enabled bool) error {
	resultCh := make(chan Result, 1)
	op := Operation{
		Type:     "enable_server",
		Data:     EnableServerData{Name: name, Enabled: enabled},
		ResultCh: resultCh,
	}

	select {
	case am.opQueue <- op:
		// Wait for result
		select {
		case result := <-resultCh:
			return result.Error
		case <-time.After(30 * time.Second):
			return &TimeoutError{Operation: "enable_server"}
		}
	default:
		return &QueueFullError{Operation: "enable_server"}
	}
}

// QuarantineServerSync queues a quarantine operation and waits for confirmation
func (am *AsyncManager) QuarantineServerSync(name string, quarantined bool) error {
	resultCh := make(chan Result, 1)
	op := Operation{
		Type:     "quarantine_server",
		Data:     QuarantineServerData{Name: name, Quarantined: quarantined},
		ResultCh: resultCh,
	}

	select {
	case am.opQueue <- op:
		// Wait for result
		select {
		case result := <-resultCh:
			return result.Error
		case <-time.After(30 * time.Second):
			return &TimeoutError{Operation: "quarantine_server"}
		}
	default:
		return &QueueFullError{Operation: "quarantine_server"}
	}
}

// Error types for async operations
type TimeoutError struct {
	Operation string
}

func (e *TimeoutError) Error() string {
	return "storage operation timeout: " + e.Operation
}

type QueueFullError struct {
	Operation string
}

func (e *QueueFullError) Error() string {
	return "storage operation queue full: " + e.Operation
}
