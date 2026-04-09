package scanner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	DefaultWorkerPoolSize = 3  // Max concurrent scans
	MaxQueueSize          = 50 // Max pending scans in queue
)

// QueueItemStatus represents the status of a queued scan
const (
	QueueStatusPending   = "pending"
	QueueStatusRunning   = "running"
	QueueStatusCompleted = "completed"
	QueueStatusFailed    = "failed"
	QueueStatusSkipped   = "skipped"
	QueueStatusCancelled = "cancelled"
)

// QueueItem represents a single scan request in the queue
type QueueItem struct {
	ServerName string    `json:"server_name"`
	Status     string    `json:"status"`
	JobID      string    `json:"job_id,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	DoneAt     time.Time `json:"done_at,omitempty"`
	SkipReason string    `json:"skip_reason,omitempty"` // Why the scan was skipped
}

// QueueProgress tracks overall scan-all progress
type QueueProgress struct {
	BatchID   string      `json:"batch_id"`
	Status    string      `json:"status"` // running, completed, cancelled
	Total     int         `json:"total"`
	Pending   int         `json:"pending"`
	Running   int         `json:"running"`
	Completed int         `json:"completed"`
	Failed    int         `json:"failed"`
	Skipped   int         `json:"skipped"`
	Cancelled int         `json:"cancelled"`
	StartedAt time.Time   `json:"started_at"`
	DoneAt    time.Time   `json:"done_at,omitempty"`
	Items     []QueueItem `json:"items"`
}

// ScanQueue manages a queue of scan requests with a worker pool
type ScanQueue struct {
	mu          sync.Mutex
	progress    *QueueProgress
	cancel      context.CancelFunc
	logger      *zap.Logger
	workerCount int
}

// NewScanQueue creates a new scan queue
func NewScanQueue(logger *zap.Logger) *ScanQueue {
	return &ScanQueue{
		logger:      logger,
		workerCount: DefaultWorkerPoolSize,
	}
}

// IsRunning returns true if a batch scan is in progress
func (q *ScanQueue) IsRunning() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.progress != nil && q.progress.Status == "running"
}

// GetProgress returns the current batch scan progress
func (q *ScanQueue) GetProgress() *QueueProgress {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.progress == nil {
		return nil
	}
	// Return a copy
	p := *q.progress
	items := make([]QueueItem, len(q.progress.Items))
	copy(items, q.progress.Items)
	p.Items = items
	return &p
}

// ScanAllRequest defines what to scan
type ScanAllRequest struct {
	ScannerIDs  []string // Specific scanners (empty = all installed)
	SkipEnabled bool     // If true, only scan quarantined servers
}

// ServerStatus provides info about a server for queue filtering
type ServerStatus struct {
	Name      string
	Enabled   bool
	Connected bool
	Protocol  string
}

// StartScanAll begins scanning all eligible servers using a worker pool.
// scanFunc is called for each server to perform the actual scan.
// serverList provides the list of servers to scan.
func (q *ScanQueue) StartScanAll(
	serverList []ServerStatus,
	scanFunc func(ctx context.Context, serverName string) (*ScanJob, error),
) (*QueueProgress, error) {
	q.mu.Lock()
	if q.progress != nil && q.progress.Status == "running" {
		q.mu.Unlock()
		return q.progress, fmt.Errorf("batch scan already in progress")
	}

	ctx, cancel := context.WithCancel(context.Background())
	q.cancel = cancel

	// Build queue items, skipping disabled servers
	var items []QueueItem
	for _, srv := range serverList {
		item := QueueItem{
			ServerName: srv.Name,
			Status:     QueueStatusPending,
		}
		if !srv.Enabled {
			item.Status = QueueStatusSkipped
			item.SkipReason = "server is disabled; enable it first"
		}
		items = append(items, item)
	}

	q.progress = &QueueProgress{
		BatchID:   fmt.Sprintf("batch-%d", time.Now().UnixNano()),
		Status:    "running",
		Total:     len(items),
		Skipped:   countByStatus(items, QueueStatusSkipped),
		Pending:   countByStatus(items, QueueStatusPending),
		StartedAt: time.Now(),
		Items:     items,
	}

	progress := *q.progress
	q.mu.Unlock()

	// Run worker pool in background
	go q.runWorkerPool(ctx, scanFunc)

	return &progress, nil
}

// CancelAll cancels the current batch scan
func (q *ScanQueue) CancelAll() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.progress == nil || q.progress.Status != "running" {
		return fmt.Errorf("no batch scan in progress")
	}

	if q.cancel != nil {
		q.cancel()
	}

	// Mark remaining pending items as cancelled
	for i := range q.progress.Items {
		if q.progress.Items[i].Status == QueueStatusPending {
			q.progress.Items[i].Status = QueueStatusCancelled
			q.progress.Cancelled++
			q.progress.Pending--
		}
	}
	q.progress.Status = "cancelled"
	q.progress.DoneAt = time.Now()

	return nil
}

// runWorkerPool processes the queue with concurrent workers
func (q *ScanQueue) runWorkerPool(ctx context.Context, scanFunc func(ctx context.Context, serverName string) (*ScanJob, error)) {
	// Collect pending items
	q.mu.Lock()
	var pendingIndices []int
	for i, item := range q.progress.Items {
		if item.Status == QueueStatusPending {
			pendingIndices = append(pendingIndices, i)
		}
	}
	q.mu.Unlock()

	// Create work channel
	work := make(chan int, len(pendingIndices))
	for _, idx := range pendingIndices {
		work <- idx
	}
	close(work)

	// Start workers
	var wg sync.WaitGroup
	workerCount := q.workerCount
	if workerCount > len(pendingIndices) {
		workerCount = len(pendingIndices)
	}
	if workerCount == 0 {
		workerCount = 1
	}

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for idx := range work {
				// Check for cancellation
				if ctx.Err() != nil {
					q.mu.Lock()
					if q.progress.Items[idx].Status == QueueStatusPending {
						q.progress.Items[idx].Status = QueueStatusCancelled
						q.progress.Cancelled++
						q.progress.Pending--
					}
					q.mu.Unlock()
					continue
				}

				q.processItem(ctx, idx, scanFunc)
			}
		}(w)
	}

	wg.Wait()

	// Mark batch as complete
	q.mu.Lock()
	if q.progress.Status == "running" {
		q.progress.Status = "completed"
	}
	q.progress.DoneAt = time.Now()
	q.mu.Unlock()

	q.logger.Info("Batch scan completed",
		zap.String("batch_id", q.progress.BatchID),
		zap.Int("total", q.progress.Total),
		zap.Int("completed", q.progress.Completed),
		zap.Int("failed", q.progress.Failed),
		zap.Int("skipped", q.progress.Skipped),
	)
}

// processItem scans a single server
func (q *ScanQueue) processItem(ctx context.Context, idx int, scanFunc func(ctx context.Context, serverName string) (*ScanJob, error)) {
	q.mu.Lock()
	item := &q.progress.Items[idx]
	item.Status = QueueStatusRunning
	item.StartedAt = time.Now()
	q.progress.Running++
	q.progress.Pending--
	serverName := item.ServerName
	q.mu.Unlock()

	q.logger.Info("Scanning server", zap.String("server", serverName))

	job, err := scanFunc(ctx, serverName)

	q.mu.Lock()
	defer q.mu.Unlock()

	q.progress.Running--
	item.DoneAt = time.Now()

	if err != nil {
		item.Status = QueueStatusFailed
		item.Error = err.Error()
		q.progress.Failed++
		q.logger.Warn("Scan failed", zap.String("server", serverName), zap.Error(err))
	} else {
		item.Status = QueueStatusCompleted
		if job != nil {
			item.JobID = job.ID
		}
		q.progress.Completed++
	}
}

func countByStatus(items []QueueItem, status string) int {
	count := 0
	for _, item := range items {
		if item.Status == status {
			count++
		}
	}
	return count
}
