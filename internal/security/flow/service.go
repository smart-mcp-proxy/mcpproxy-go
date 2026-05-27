package flow

import (
	"encoding/json"
	"strings"
)

// DetectionResult mirrors the security.Result type for loose coupling.
// The real detector (internal/security) is injected via the SensitiveDataDetector interface.
type DetectionResult struct {
	Detected   bool
	Detections []DetectionEntry
}

// DetectionEntry mirrors a single detection from the sensitive data detector.
type DetectionEntry struct {
	Type     string
	Category string
	Severity string
	Location string
}

// SensitiveDataDetector is an interface for scanning content for sensitive data.
// Implemented by internal/security.Detector.
type SensitiveDataDetector interface {
	Scan(arguments, response string) *DetectionResult
}

// FlowService orchestrates classification, tracking, and policy evaluation.
type FlowService struct {
	classifier *Classifier
	tracker    *FlowTracker
	policy     *PolicyEvaluator
	detector   SensitiveDataDetector
	correlator *Correlator
}

// NewFlowService creates a FlowService with all required dependencies.
// The correlator parameter is optional (nil disables session correlation).
func NewFlowService(
	classifier *Classifier,
	tracker *FlowTracker,
	policy *PolicyEvaluator,
	detector SensitiveDataDetector,
	correlator *Correlator,
) *FlowService {
	return &FlowService{
		classifier: classifier,
		tracker:    tracker,
		policy:     policy,
		detector:   detector,
		correlator: correlator,
	}
}

// SetExpiryCallback sets a callback invoked before each expired session is deleted.
// The FlowService wraps the callback to automatically set coverage_mode based on
// whether session correlation (hook-enhanced mode) is active.
func (fs *FlowService) SetExpiryCallback(callback func(*FlowSummary)) {
	fs.tracker.SetExpiryCallback(func(summary *FlowSummary) {
		if fs.correlator != nil {
			summary.CoverageMode = string(CoverageModeFull)
		} else {
			summary.CoverageMode = string(CoverageModeProxyOnly)
		}
		callback(summary)
	})
}

// Stop halts background goroutines (delegates to tracker and correlator).
func (fs *FlowService) Stop() {
	fs.tracker.Stop()
	if fs.correlator != nil {
		fs.correlator.Stop()
	}
}

// RecordOriginProxy records data origins from MCP proxy responses (proxy-only mode).
// Called after call_tool_read responses — classifies the server, scans for sensitive data,
// hashes the response, and records origins.
func (fs *FlowService) RecordOriginProxy(mcpSessionID, serverName, toolName, responseJSON string) {
	// Classify the source server/tool
	classResult := fs.classifier.Classify(serverName, toolName)

	// Scan for sensitive data
	var hasSensitive bool
	var sensitiveTypes []string
	if fs.detector != nil {
		scanResult := fs.detector.Scan("", responseJSON)
		if scanResult != nil && scanResult.Detected {
			hasSensitive = true
			for _, d := range scanResult.Detections {
				sensitiveTypes = append(sensitiveTypes, d.Type)
			}
		}
	}

	// Truncate response for hashing if needed
	content := responseJSON
	if fs.tracker.config.MaxResponseHashBytes > 0 && len(content) > fs.tracker.config.MaxResponseHashBytes {
		content = content[:fs.tracker.config.MaxResponseHashBytes]
	}

	// Hash at multiple granularities
	minLength := fs.tracker.config.HashMinLength

	// Per-field hashes from JSON
	fieldHashes := ExtractFieldHashes(content, minLength)
	for hash := range fieldHashes {
		origin := &DataOrigin{
			ContentHash:      hash,
			ToolName:         toolName,
			ServerName:       serverName,
			Classification:   classResult.Classification,
			HasSensitiveData: hasSensitive,
			SensitiveTypes:   sensitiveTypes,
		}
		fs.tracker.RecordOrigin(mcpSessionID, origin)
	}

	// Full content hash
	if len(content) >= minLength {
		fullHash := HashContent(content)
		if !fieldHashes[fullHash] { // Avoid duplicate
			origin := &DataOrigin{
				ContentHash:      fullHash,
				ToolName:         toolName,
				ServerName:       serverName,
				Classification:   classResult.Classification,
				HasSensitiveData: hasSensitive,
				SensitiveTypes:   sensitiveTypes,
			}
			fs.tracker.RecordOrigin(mcpSessionID, origin)
		}
	}
}

// CheckFlowProxy checks tool arguments for data flow matches (proxy-only mode).
// Called before call_tool_write/call_tool_destructive execution.
func (fs *FlowService) CheckFlowProxy(mcpSessionID, serverName, toolName, argsJSON string) []*FlowEdge {
	destClass := fs.classifier.Classify(serverName, toolName)
	edges, _ := fs.tracker.CheckFlow(mcpSessionID, toolName, serverName, destClass.Classification, argsJSON)
	return edges
}

// CheckSuspiciousURLs checks tool arguments for suspicious endpoint URLs.
// Returns the policy decision and reason.
func (fs *FlowService) CheckSuspiciousURLs(argsJSON string) (PolicyAction, string) {
	return fs.policy.CheckArgsForSuspiciousURLs(argsJSON)
}

// EvaluatePolicy evaluates flow edges against the configured policy.
func (fs *FlowService) EvaluatePolicy(edges []*FlowEdge, mode string) (PolicyAction, string) {
	return fs.policy.Evaluate(edges, mode)
}

// Evaluate processes a hook evaluate request and returns a security decision.
// Dispatches to evaluatePreToolUse or processPostToolUse based on the event type.
func (fs *FlowService) Evaluate(req *HookEvaluateRequest) *HookEvaluateResponse {
	switch req.Event {
	case "PreToolUse":
		return fs.evaluatePreToolUse(req)
	case "PostToolUse":
		return fs.processPostToolUse(req)
	default:
		return &HookEvaluateResponse{
			Decision: PolicyAllow,
			Reason:   "unknown event type: " + req.Event,
		}
	}
}

// evaluatePreToolUse classifies the destination tool and checks for data flow violations.
// For mcp__mcpproxy__* tools, registers a pending correlation for session linking.
func (fs *FlowService) evaluatePreToolUse(req *HookEvaluateRequest) *HookEvaluateResponse {
	// Check for mcp__mcpproxy__* tools — register pending correlation
	if fs.correlator != nil && isMCPProxyTool(req.ToolName) {
		fs.registerCorrelation(req)
	}

	// Classify the destination tool
	destClass := fs.classifier.Classify("", req.ToolName)

	// Marshal tool input for hash matching
	argsJSON := marshalToolInput(req.ToolInput)

	// Check for suspicious URLs in tool arguments (independent of flow detection)
	if urlAction, urlReason := fs.policy.CheckArgsForSuspiciousURLs(argsJSON); urlAction == PolicyDeny {
		return &HookEvaluateResponse{
			Decision:  PolicyDeny,
			Reason:    urlReason,
			RiskLevel: RiskCritical,
			FlowType:  FlowInternalToExternal,
		}
	}

	// Check flow against recorded origins
	edges, _ := fs.tracker.CheckFlow(req.SessionID, req.ToolName, "", destClass.Classification, argsJSON)

	if len(edges) == 0 {
		return &HookEvaluateResponse{
			Decision:  PolicyAllow,
			Reason:    "no data flow detected",
			RiskLevel: RiskNone,
		}
	}

	// Evaluate policy on detected edges
	action, reason := fs.policy.Evaluate(edges, "hook_enhanced")

	// Determine the highest risk level and flow type from edges
	highestSeverity := -1
	var highestRisk RiskLevel
	var flowType FlowType
	for _, edge := range edges {
		sev := policyActionSeverity(riskToAction(edge.RiskLevel))
		if sev > highestSeverity {
			highestSeverity = sev
			highestRisk = edge.RiskLevel
			flowType = edge.FlowType
		}
	}

	return &HookEvaluateResponse{
		Decision:  action,
		Reason:    reason,
		RiskLevel: highestRisk,
		FlowType:  flowType,
	}
}

// processPostToolUse records data origins from tool responses.
// Classifies the source tool, scans for sensitive data, hashes the response at
// multiple granularities, and records all content hashes as DataOrigins.
// Always returns PolicyAllow — PostToolUse only records, never blocks.
func (fs *FlowService) processPostToolUse(req *HookEvaluateRequest) *HookEvaluateResponse {
	if req.ToolResponse == "" {
		return &HookEvaluateResponse{
			Decision: PolicyAllow,
			Reason:   "no response to record",
		}
	}

	// Classify the source tool
	classResult := fs.classifier.Classify("", req.ToolName)

	// Scan for sensitive data
	var hasSensitive bool
	var sensitiveTypes []string
	if fs.detector != nil {
		scanResult := fs.detector.Scan("", req.ToolResponse)
		if scanResult != nil && scanResult.Detected {
			hasSensitive = true
			for _, d := range scanResult.Detections {
				sensitiveTypes = append(sensitiveTypes, d.Type)
			}
		}
	}

	// Truncate response for hashing if needed
	content := req.ToolResponse
	if fs.tracker.config.MaxResponseHashBytes > 0 && len(content) > fs.tracker.config.MaxResponseHashBytes {
		content = content[:fs.tracker.config.MaxResponseHashBytes]
	}

	// Hash at multiple granularities
	minLength := fs.tracker.config.HashMinLength

	// Per-field hashes from JSON
	fieldHashes := ExtractFieldHashes(content, minLength)
	for hash := range fieldHashes {
		origin := &DataOrigin{
			ContentHash:      hash,
			ToolName:         req.ToolName,
			Classification:   classResult.Classification,
			HasSensitiveData: hasSensitive,
			SensitiveTypes:   sensitiveTypes,
		}
		fs.tracker.RecordOrigin(req.SessionID, origin)
	}

	// Full content hash
	if len(content) >= minLength {
		fullHash := HashContent(content)
		if !fieldHashes[fullHash] { // Avoid duplicate
			origin := &DataOrigin{
				ContentHash:      fullHash,
				ToolName:         req.ToolName,
				Classification:   classResult.Classification,
				HasSensitiveData: hasSensitive,
				SensitiveTypes:   sensitiveTypes,
			}
			fs.tracker.RecordOrigin(req.SessionID, origin)
		}
	}

	return &HookEvaluateResponse{
		Decision: PolicyAllow,
		Reason:   "origin recorded",
	}
}

// marshalToolInput converts tool input to JSON string for hash matching.
func marshalToolInput(input map[string]any) string {
	if input == nil {
		return ""
	}
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return string(data)
}

// riskToAction maps risk levels to approximate policy actions for comparison.
func riskToAction(risk RiskLevel) PolicyAction {
	switch risk {
	case RiskCritical:
		return PolicyDeny
	case RiskHigh:
		return PolicyAsk
	case RiskMedium:
		return PolicyWarn
	default:
		return PolicyAllow
	}
}

// ClassifyTool returns the classification of a tool (internal/external/hybrid/unknown).
func (fs *FlowService) ClassifyTool(serverName, toolName string) Classification {
	return fs.classifier.Classify(serverName, toolName).Classification
}

// GetSession returns the flow session for a given session ID.
func (fs *FlowService) GetSession(sessionID string) *FlowSession {
	return fs.tracker.GetSession(sessionID)
}

// --- Session Correlation (T083-T084) ---

// mcpProxyToolPrefix is the namespace prefix for MCP proxy tool calls seen via hooks.
const mcpProxyToolPrefix = "mcp__mcpproxy__"

// isMCPProxyTool returns true if the tool name is an mcp__mcpproxy__* tool.
func isMCPProxyTool(toolName string) bool {
	return strings.HasPrefix(toolName, mcpProxyToolPrefix)
}

// registerCorrelation extracts the inner tool name and args from a
// mcp__mcpproxy__call_tool_* PreToolUse request and registers a pending correlation.
func (fs *FlowService) registerCorrelation(req *HookEvaluateRequest) {
	if req.ToolInput == nil {
		return
	}

	// Extract inner tool name ("name" field = "server:tool")
	innerName, _ := req.ToolInput["name"].(string)
	if innerName == "" {
		return
	}

	// Extract inner args ("args_json" field) and normalize via re-marshal
	// to ensure hash matches the MCP proxy's json.Marshal(args) output.
	argsJSON, _ := req.ToolInput["args_json"].(string)
	normalizedArgs := normalizeJSON(argsJSON)

	// Hash: innerName + normalizedArgs
	argsHash := HashContent(innerName + normalizedArgs)

	fs.correlator.RegisterPending(req.SessionID, argsHash, innerName)
}

// MatchCorrelation attempts to find a pending correlation for the given args hash.
// Returns the hook session ID if found, or empty string otherwise.
func (fs *FlowService) MatchCorrelation(argsHash string) string {
	if fs.correlator == nil {
		return ""
	}
	return fs.correlator.MatchAndConsume(argsHash)
}

// LinkSessions links a hook session to an MCP session by matching a pending correlation.
// If a match is found, the MCP session inherits all origins from the hook session.
func (fs *FlowService) LinkSessions(argsHash, mcpSessionID string) {
	hookSessionID := fs.MatchCorrelation(argsHash)
	if hookSessionID == "" {
		return
	}

	hookSession := fs.tracker.GetSession(hookSessionID)
	if hookSession == nil {
		return
	}

	// Get or create MCP session
	mcpSession := fs.tracker.getOrCreateSession(mcpSessionID)

	// Link: record the hook session ID in the MCP session
	mcpSession.mu.Lock()
	mcpSession.LinkedMCPSessions = append(mcpSession.LinkedMCPSessions, hookSessionID)
	mcpSession.mu.Unlock()

	// Copy origins from hook session to MCP session
	hookSession.mu.RLock()
	defer hookSession.mu.RUnlock()

	for hash, origin := range hookSession.Origins {
		// Record each origin in the MCP session (RecordOrigin handles locking)
		originCopy := &DataOrigin{
			ContentHash:      hash,
			ToolCallID:       origin.ToolCallID,
			ToolName:         origin.ToolName,
			ServerName:       origin.ServerName,
			Classification:   origin.Classification,
			HasSensitiveData: origin.HasSensitiveData,
			SensitiveTypes:   origin.SensitiveTypes,
			Timestamp:        origin.Timestamp,
		}
		fs.tracker.RecordOrigin(mcpSessionID, originCopy)
	}
}

// CorrelationArgsHash computes the correlation hash for a tool name and args JSON.
// Used by the MCP proxy to compute the same hash as the hook PreToolUse registration.
// The argsJSON is normalized via re-marshal to ensure consistent hashing.
func CorrelationArgsHash(toolName, argsJSON string) string {
	return HashContent(toolName + normalizeJSON(argsJSON))
}

// normalizeJSON parses and re-marshals JSON to produce canonical compact output.
// Returns the original string if parsing fails (non-JSON content).
func normalizeJSON(s string) string {
	if s == "" {
		return s
	}
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return s
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return s
	}
	return string(out)
}
