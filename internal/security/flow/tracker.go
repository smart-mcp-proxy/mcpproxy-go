package flow

import (
	"sync"
	"time"
)

// TrackerConfig configures the FlowTracker.
type TrackerConfig struct {
	SessionTimeoutMin    int // Session expiry in minutes (default: 30)
	MaxOriginsPerSession int // Max origins per session before eviction (default: 10000)
	HashMinLength        int // Minimum string length for per-field hashing (default: 20)
	MaxResponseHashBytes int // Max response bytes to hash (default: 65536)
}

// FlowTracker tracks data origins and detects cross-boundary data flows.
type FlowTracker struct {
	config         *TrackerConfig
	sessions       map[string]*FlowSession // session ID → FlowSession
	mu             sync.RWMutex            // Protects sessions map
	stopCh         chan struct{}
	expiryCallback func(*FlowSummary) // Called before session deletion on expiry
}

// NewFlowTracker creates a FlowTracker with the given configuration.
func NewFlowTracker(config *TrackerConfig) *FlowTracker {
	ft := &FlowTracker{
		config:   config,
		sessions: make(map[string]*FlowSession),
		stopCh:   make(chan struct{}),
	}
	go ft.sessionExpiryLoop()
	return ft
}

// Stop halts the session expiry goroutine.
func (ft *FlowTracker) Stop() {
	select {
	case <-ft.stopCh:
		// Already stopped
	default:
		close(ft.stopCh)
	}
}

// RecordOrigin stores a data origin in the specified session.
func (ft *FlowTracker) RecordOrigin(sessionID string, origin *DataOrigin) {
	session := ft.getOrCreateSession(sessionID)

	session.mu.Lock()
	defer session.mu.Unlock()

	// Evict oldest if at capacity
	if len(session.Origins) >= ft.config.MaxOriginsPerSession {
		ft.evictOldest(session)
	}

	session.Origins[origin.ContentHash] = origin
	session.LastActivity = time.Now()

	if origin.ToolName != "" {
		session.ToolsUsed[origin.ToolName] = true
	}
}

// CheckFlow evaluates tool arguments against recorded origins for data flow matches.
// Returns detected FlowEdges. Returns nil if no session exists or no matches found.
func (ft *FlowTracker) CheckFlow(sessionID string, toolName, serverName string, destClassification Classification, argsJSON string) ([]*FlowEdge, error) {
	session := ft.GetSession(sessionID)
	if session == nil {
		return nil, nil
	}

	// Extract hashes from the arguments
	argHashes := extractArgHashes(argsJSON, ft.config.HashMinLength)
	if len(argHashes) == 0 {
		return nil, nil
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	var edges []*FlowEdge
	matched := make(map[string]bool) // Avoid duplicate edges for same content hash

	for hash := range argHashes {
		if matched[hash] {
			continue
		}
		origin, found := session.Origins[hash]
		if !found {
			continue
		}
		matched[hash] = true

		flowType := determineFlowType(origin.Classification, destClassification)
		riskLevel := assessRisk(flowType, origin.HasSensitiveData)

		edge := &FlowEdge{
			FromOrigin:       origin,
			ToToolName:       toolName,
			ToServerName:     serverName,
			ToClassification: destClassification,
			FlowType:         flowType,
			RiskLevel:        riskLevel,
			ContentHash:      hash,
			Timestamp:        time.Now(),
		}
		edges = append(edges, edge)
	}

	// Update session activity
	session.LastActivity = time.Now()
	if toolName != "" {
		session.ToolsUsed[toolName] = true
	}

	// Append detected flows
	if len(edges) > 0 {
		session.Flows = append(session.Flows, edges...)
	}

	return edges, nil
}

// GetSession returns the flow session for a given session ID, or nil if not found.
func (ft *FlowTracker) GetSession(sessionID string) *FlowSession {
	ft.mu.RLock()
	defer ft.mu.RUnlock()
	return ft.sessions[sessionID]
}

func (ft *FlowTracker) getOrCreateSession(sessionID string) *FlowSession {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	if session, ok := ft.sessions[sessionID]; ok {
		return session
	}

	session := &FlowSession{
		ID:        sessionID,
		StartTime: time.Now(),
		Origins:   make(map[string]*DataOrigin),
		ToolsUsed: make(map[string]bool),
	}
	ft.sessions[sessionID] = session
	return session
}

// evictOldest removes the oldest origin from the session to make room.
// Must be called with session.mu held.
func (ft *FlowTracker) evictOldest(session *FlowSession) {
	var oldestHash string
	var oldestTime time.Time

	for hash, origin := range session.Origins {
		if oldestHash == "" || origin.Timestamp.Before(oldestTime) {
			oldestHash = hash
			oldestTime = origin.Timestamp
		}
	}

	if oldestHash != "" {
		delete(session.Origins, oldestHash)
	}
}

func (ft *FlowTracker) sessionExpiryLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ft.stopCh:
			return
		case <-ticker.C:
			ft.expireSessions()
		}
	}
}

// SetExpiryCallback sets a callback invoked with a FlowSummary before each
// expired session is deleted. The callback runs while the tracker lock is held,
// so it should be non-blocking (e.g., emit an event to a channel).
func (ft *FlowTracker) SetExpiryCallback(callback func(*FlowSummary)) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.expiryCallback = callback
}

func (ft *FlowTracker) expireSessions() {
	timeout := time.Duration(ft.config.SessionTimeoutMin) * time.Minute

	ft.mu.Lock()
	defer ft.mu.Unlock()

	now := time.Now()
	for id, session := range ft.sessions {
		session.mu.RLock()
		lastActivity := session.LastActivity
		session.mu.RUnlock()

		if now.Sub(lastActivity) > timeout {
			// Generate summary and invoke callback before deletion
			if ft.expiryCallback != nil {
				summary := GenerateFlowSummary(session, "")
				ft.expiryCallback(summary)
			}
			delete(ft.sessions, id)
		}
	}
}

// GenerateFlowSummary computes aggregate statistics from a FlowSession.
// The coverageMode parameter indicates "proxy_only" or "full".
func GenerateFlowSummary(session *FlowSession, coverageMode string) *FlowSummary {
	session.mu.RLock()
	defer session.mu.RUnlock()

	summary := &FlowSummary{
		SessionID:             session.ID,
		CoverageMode:          coverageMode,
		TotalOrigins:          len(session.Origins),
		TotalFlows:            len(session.Flows),
		FlowTypeDistribution:  make(map[string]int),
		RiskLevelDistribution: make(map[string]int),
		LinkedMCPSessions:     session.LinkedMCPSessions,
	}

	// Calculate duration
	if !session.StartTime.IsZero() && !session.LastActivity.IsZero() {
		duration := session.LastActivity.Sub(session.StartTime)
		if duration < 0 {
			duration = 0
		}
		summary.DurationMinutes = int(duration.Minutes())
	}

	// Build distributions and detect sensitive flows
	for _, edge := range session.Flows {
		summary.FlowTypeDistribution[string(edge.FlowType)]++
		summary.RiskLevelDistribution[string(edge.RiskLevel)]++

		if edge.RiskLevel == RiskCritical {
			summary.HasSensitiveFlows = true
		}
		if edge.FromOrigin != nil && edge.FromOrigin.HasSensitiveData && edge.FlowType == FlowInternalToExternal {
			summary.HasSensitiveFlows = true
		}
	}

	// Collect tools used
	for tool := range session.ToolsUsed {
		summary.ToolsUsed = append(summary.ToolsUsed, tool)
	}

	return summary
}

// extractArgHashes extracts content hashes from tool arguments JSON.
// It produces both full-content and per-field hashes for matching.
func extractArgHashes(argsJSON string, minLength int) map[string]bool {
	hashes := make(map[string]bool)

	// Try to extract per-field hashes from JSON
	fieldHashes := ExtractFieldHashes(argsJSON, minLength)
	for h := range fieldHashes {
		hashes[h] = true
	}

	// Also hash the full content if long enough
	if len(argsJSON) >= minLength {
		hashes[HashContent(argsJSON)] = true
	}

	return hashes
}

// determineFlowType classifies the direction of data movement.
func determineFlowType(fromClass, toClass Classification) FlowType {
	fromInternal := fromClass == ClassInternal || fromClass == ClassHybrid
	fromExternal := fromClass == ClassExternal
	toInternal := toClass == ClassInternal
	toExternal := toClass == ClassExternal || toClass == ClassHybrid

	switch {
	case fromInternal && toExternal:
		return FlowInternalToExternal
	case fromExternal && toInternal:
		return FlowExternalToInternal
	case fromInternal && toInternal:
		return FlowInternalToInternal
	case fromExternal && !toInternal:
		return FlowExternalToExternal
	default:
		// Unknown classifications default to internal→internal (safe assumption)
		return FlowInternalToInternal
	}
}

// assessRisk determines the risk level based on flow type and sensitive data.
func assessRisk(flowType FlowType, hasSensitiveData bool) RiskLevel {
	switch flowType {
	case FlowInternalToExternal:
		if hasSensitiveData {
			return RiskCritical
		}
		return RiskHigh
	case FlowExternalToInternal, FlowInternalToInternal, FlowExternalToExternal:
		return RiskNone
	default:
		return RiskLow
	}
}

