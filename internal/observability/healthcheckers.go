package observability

import (
	"context"
	"fmt"

	"go.etcd.io/bbolt"
)

// DatabaseHealthChecker checks the health of a BoltDB database
type DatabaseHealthChecker struct {
	name string
	db   *bbolt.DB
}

// NewDatabaseHealthChecker creates a new database health checker
func NewDatabaseHealthChecker(name string, db *bbolt.DB) *DatabaseHealthChecker {
	return &DatabaseHealthChecker{
		name: name,
		db:   db,
	}
}

// Name returns the name of the health checker
func (dhc *DatabaseHealthChecker) Name() string {
	return dhc.name
}

// HealthCheck performs a database health check
func (dhc *DatabaseHealthChecker) HealthCheck(_ context.Context) error {
	if dhc.db == nil {
		return fmt.Errorf("database is nil")
	}

	// Try to perform a simple read transaction
	return dhc.db.View(func(_ *bbolt.Tx) error {
		// Just verify we can start a transaction
		return nil
	})
}

// ReadinessCheck performs a database readiness check
func (dhc *DatabaseHealthChecker) ReadinessCheck(ctx context.Context) error {
	return dhc.HealthCheck(ctx)
}

// IndexHealthChecker checks the health of the search index
type IndexHealthChecker struct {
	name        string
	getDocCount func() (uint64, error)
}

// NewIndexHealthChecker creates a new index health checker
func NewIndexHealthChecker(name string, getDocCount func() (uint64, error)) *IndexHealthChecker {
	return &IndexHealthChecker{
		name:        name,
		getDocCount: getDocCount,
	}
}

// Name returns the name of the health checker
func (ihc *IndexHealthChecker) Name() string {
	return ihc.name
}

// HealthCheck performs an index health check
func (ihc *IndexHealthChecker) HealthCheck(_ context.Context) error {
	if ihc.getDocCount == nil {
		return fmt.Errorf("getDocCount function is nil")
	}

	// Try to get document count to verify index is accessible
	_, err := ihc.getDocCount()
	return err
}

// ReadinessCheck performs an index readiness check
func (ihc *IndexHealthChecker) ReadinessCheck(ctx context.Context) error {
	return ihc.HealthCheck(ctx)
}

// UpstreamHealthChecker checks the health of upstream servers
type UpstreamHealthChecker struct {
	name         string
	getStats     func() map[string]interface{}
	minConnected int
}

// NewUpstreamHealthChecker creates a new upstream health checker
func NewUpstreamHealthChecker(name string, getStats func() map[string]interface{}, minConnected int) *UpstreamHealthChecker {
	return &UpstreamHealthChecker{
		name:         name,
		getStats:     getStats,
		minConnected: minConnected,
	}
}

// Name returns the name of the health checker
func (uhc *UpstreamHealthChecker) Name() string {
	return uhc.name
}

// HealthCheck performs an upstream servers health check
func (uhc *UpstreamHealthChecker) HealthCheck(_ context.Context) error {
	if uhc.getStats == nil {
		return fmt.Errorf("getStats function is nil")
	}

	stats := uhc.getStats()
	return uhc.checkStats(stats)
}

// ReadinessCheck performs an upstream servers readiness check
func (uhc *UpstreamHealthChecker) ReadinessCheck(_ context.Context) error {
	if uhc.getStats == nil {
		return fmt.Errorf("getStats function is nil")
	}

	stats := uhc.getStats()
	return uhc.checkReadiness(stats)
}

func (uhc *UpstreamHealthChecker) checkStats(stats map[string]interface{}) error {
	// Basic health check - just verify we can get stats
	if stats == nil {
		return fmt.Errorf("stats is nil")
	}
	return nil
}

func (uhc *UpstreamHealthChecker) checkReadiness(stats map[string]interface{}) error {
	if stats == nil {
		return fmt.Errorf("stats is nil")
	}

	// For readiness, check if we have minimum connected servers
	if servers, ok := stats["servers"].(map[string]interface{}); ok {
		connectedCount := 0
		for _, serverStat := range servers {
			if stat, ok := serverStat.(map[string]interface{}); ok {
				if connected, ok := stat["connected"].(bool); ok && connected {
					connectedCount++
				}
			}
		}

		if connectedCount < uhc.minConnected {
			return fmt.Errorf("insufficient connected servers: %d < %d", connectedCount, uhc.minConnected)
		}
	}

	return nil
}

// ComponentHealthChecker is a generic health checker for components with a simple status
type ComponentHealthChecker struct {
	name      string
	isHealthy func() bool
	isReady   func() bool
}

// NewComponentHealthChecker creates a new component health checker
func NewComponentHealthChecker(name string, isHealthy, isReady func() bool) *ComponentHealthChecker {
	return &ComponentHealthChecker{
		name:      name,
		isHealthy: isHealthy,
		isReady:   isReady,
	}
}

// Name returns the name of the health checker
func (chc *ComponentHealthChecker) Name() string {
	return chc.name
}

// HealthCheck performs a component health check
func (chc *ComponentHealthChecker) HealthCheck(_ context.Context) error {
	if chc.isHealthy == nil {
		return fmt.Errorf("isHealthy function is nil")
	}

	if !chc.isHealthy() {
		return fmt.Errorf("component is not healthy")
	}

	return nil
}

// ReadinessCheck performs a component readiness check
func (chc *ComponentHealthChecker) ReadinessCheck(_ context.Context) error {
	if chc.isReady == nil {
		return fmt.Errorf("isReady function is nil")
	}

	if !chc.isReady() {
		return fmt.Errorf("component is not ready")
	}

	return nil
}

// CombinedHealthChecker can act as both health and readiness checker
var _ HealthChecker = (*DatabaseHealthChecker)(nil)
var _ ReadinessChecker = (*DatabaseHealthChecker)(nil)
var _ HealthChecker = (*IndexHealthChecker)(nil)
var _ ReadinessChecker = (*IndexHealthChecker)(nil)
var _ HealthChecker = (*UpstreamHealthChecker)(nil)
var _ ReadinessChecker = (*UpstreamHealthChecker)(nil)
var _ HealthChecker = (*ComponentHealthChecker)(nil)
var _ ReadinessChecker = (*ComponentHealthChecker)(nil)
