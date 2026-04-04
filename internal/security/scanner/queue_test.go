package scanner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestQueueScanAll(t *testing.T) {
	q := NewScanQueue(zap.NewNop())

	servers := []ServerStatus{
		{Name: "server-1", Enabled: true},
		{Name: "server-2", Enabled: true},
		{Name: "server-3", Enabled: false}, // Should be skipped
	}

	var scanned int32
	scanFunc := func(ctx context.Context, name string) (*ScanJob, error) {
		atomic.AddInt32(&scanned, 1)
		time.Sleep(10 * time.Millisecond) // Simulate work
		return &ScanJob{ID: "job-" + name}, nil
	}

	progress, err := q.StartScanAll(servers, scanFunc)
	if err != nil {
		t.Fatalf("StartScanAll: %v", err)
	}
	if progress.Total != 3 {
		t.Errorf("expected total 3, got %d", progress.Total)
	}
	if progress.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", progress.Skipped)
	}

	// Wait for completion
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		p := q.GetProgress()
		if p != nil && p.Status != "running" {
			break
		}
	}

	p := q.GetProgress()
	if p == nil {
		t.Fatal("progress is nil")
	}
	if p.Status != "completed" {
		t.Errorf("expected completed, got %s", p.Status)
	}
	if p.Completed != 2 {
		t.Errorf("expected 2 completed, got %d", p.Completed)
	}
	if p.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", p.Skipped)
	}
	if atomic.LoadInt32(&scanned) != 2 {
		t.Errorf("expected 2 scans, got %d", scanned)
	}
}

func TestQueueCancelAll(t *testing.T) {
	q := NewScanQueue(zap.NewNop())

	servers := []ServerStatus{
		{Name: "s1", Enabled: true},
		{Name: "s2", Enabled: true},
		{Name: "s3", Enabled: true},
		{Name: "s4", Enabled: true},
		{Name: "s5", Enabled: true},
	}

	// Slow scan function
	scanFunc := func(ctx context.Context, name string) (*ScanJob, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return &ScanJob{ID: "job-" + name}, nil
		}
	}

	_, err := q.StartScanAll(servers, scanFunc)
	if err != nil {
		t.Fatalf("StartScanAll: %v", err)
	}

	// Cancel quickly
	time.Sleep(50 * time.Millisecond)
	if err := q.CancelAll(); err != nil {
		t.Fatalf("CancelAll: %v", err)
	}

	p := q.GetProgress()
	if p.Status != "cancelled" {
		t.Errorf("expected cancelled, got %s", p.Status)
	}
}

func TestQueueAlreadyRunning(t *testing.T) {
	q := NewScanQueue(zap.NewNop())

	servers := []ServerStatus{{Name: "s1", Enabled: true}}
	scanFunc := func(ctx context.Context, name string) (*ScanJob, error) {
		time.Sleep(200 * time.Millisecond)
		return nil, nil
	}

	q.StartScanAll(servers, scanFunc)
	_, err := q.StartScanAll(servers, scanFunc)
	if err == nil {
		t.Error("expected error for concurrent batch scan")
	}

	// Wait for first to finish
	time.Sleep(300 * time.Millisecond)
}

func TestQueueIsRunning(t *testing.T) {
	q := NewScanQueue(zap.NewNop())
	if q.IsRunning() {
		t.Error("should not be running initially")
	}
}

func TestQueueSkipDisabled(t *testing.T) {
	q := NewScanQueue(zap.NewNop())

	servers := []ServerStatus{
		{Name: "disabled-1", Enabled: false},
		{Name: "disabled-2", Enabled: false},
	}

	scanFunc := func(ctx context.Context, name string) (*ScanJob, error) {
		t.Error("should not scan disabled servers")
		return nil, nil
	}

	q.StartScanAll(servers, scanFunc)

	// Wait briefly
	time.Sleep(100 * time.Millisecond)

	p := q.GetProgress()
	if p.Completed != 0 {
		t.Errorf("expected 0 completed, got %d", p.Completed)
	}
	if p.Skipped != 2 {
		t.Errorf("expected 2 skipped, got %d", p.Skipped)
	}
}
