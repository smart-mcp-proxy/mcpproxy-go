# Quickstart: Data Flow Security with Agent Hook Integration

**Phase**: 1 - Design
**Date**: 2026-02-04

## Overview

This guide provides a rapid path to implementing and testing the data flow security feature. It covers the server/tool classifier, content hashing, flow tracker, hook evaluation endpoint, CLI hook commands, and session correlation.

## Prerequisites

- Go 1.24+
- MCPProxy codebase cloned and buildable
- Familiarity with `internal/server/mcp.go` (MCP tool call pipeline)
- Familiarity with `internal/security/detector.go` (sensitive data detection)
- Understanding of `internal/httpapi/server.go` routing patterns
- Claude Code installed (for hook integration testing)

## Step 1: Create the Flow Security Package

```bash
mkdir -p internal/security/flow
```

### Server/Tool Classifier

```go
// internal/security/flow/classifier.go
package flow

import (
	"strings"
)

// Classification represents the data flow role of a server or tool.
type Classification string

const (
	ClassInternal Classification = "internal"
	ClassExternal Classification = "external"
	ClassHybrid   Classification = "hybrid"
	ClassUnknown  Classification = "unknown"
)

// ClassificationResult holds the outcome of classifying a server or tool.
type ClassificationResult struct {
	Classification Classification
	Confidence     float64
	Method         string // "heuristic", "config", "annotation"
	Reason         string
	CanExfiltrate  bool
	CanReadData    bool
}

// Classifier classifies servers and tools as internal/external/hybrid.
type Classifier struct {
	internalPatterns []string
	externalPatterns []string
	hybridPatterns   []string
	overrides        map[string]Classification
}

// NewClassifier creates a classifier with default patterns and config overrides.
func NewClassifier(overrides map[string]Classification) *Classifier {
	return &Classifier{
		internalPatterns: []string{
			"database", "db", "postgres", "mysql", "mongo", "redis",
			"file", "filesystem", "git", "github", "gitlab", "bitbucket",
			"code", "repo", "source", "vault", "secret",
		},
		externalPatterns: []string{
			"slack", "discord", "email", "smtp", "webhook",
			"http", "api-gateway", "notification", "sms", "twilio",
			"teams", "telegram", "matrix", "irc",
		},
		hybridPatterns: []string{
			"cloud", "aws", "gcp", "azure",
		},
		overrides: overrides,
	}
}

// internalToolClassifications maps agent-internal tool names to their classification.
var internalToolClassifications = map[string]ClassificationResult{
	"Read":      {Classification: ClassInternal, Confidence: 1.0, Method: "builtin", CanReadData: true},
	"Write":     {Classification: ClassInternal, Confidence: 1.0, Method: "builtin", CanReadData: false},
	"Edit":      {Classification: ClassInternal, Confidence: 1.0, Method: "builtin", CanReadData: false},
	"Glob":      {Classification: ClassInternal, Confidence: 1.0, Method: "builtin", CanReadData: true},
	"Grep":      {Classification: ClassInternal, Confidence: 1.0, Method: "builtin", CanReadData: true},
	"Task":      {Classification: ClassInternal, Confidence: 0.9, Method: "builtin", CanReadData: true},
	"WebFetch":  {Classification: ClassExternal, Confidence: 1.0, Method: "builtin", CanExfiltrate: true},
	"WebSearch": {Classification: ClassExternal, Confidence: 0.8, Method: "builtin", CanExfiltrate: false},
	"Bash":      {Classification: ClassHybrid, Confidence: 0.7, Method: "builtin", CanReadData: true, CanExfiltrate: true},
}

// Classify returns the classification for a server or tool name.
func (c *Classifier) Classify(serverName, toolName string) ClassificationResult {
	// Check agent-internal tools first
	if serverName == "" {
		if result, ok := internalToolClassifications[toolName]; ok {
			return result
		}
	}

	// Check config overrides
	if override, ok := c.overrides[serverName]; ok {
		return ClassificationResult{
			Classification: override,
			Confidence:     1.0,
			Method:         "config",
			Reason:         "Manual override via configuration",
		}
	}

	// Heuristic pattern matching on server name
	nameLower := strings.ToLower(serverName)
	return c.classifyByName(nameLower)
}

func (c *Classifier) classifyByName(name string) ClassificationResult {
	for _, pattern := range c.externalPatterns {
		if strings.Contains(name, pattern) {
			return ClassificationResult{
				Classification: ClassExternal,
				Confidence:     0.8,
				Method:         "heuristic",
				Reason:         "Name matches external pattern: " + pattern,
				CanExfiltrate:  true,
			}
		}
	}
	for _, pattern := range c.internalPatterns {
		if strings.Contains(name, pattern) {
			return ClassificationResult{
				Classification: ClassInternal,
				Confidence:     0.8,
				Method:         "heuristic",
				Reason:         "Name matches internal pattern: " + pattern,
				CanReadData:    true,
			}
		}
	}
	for _, pattern := range c.hybridPatterns {
		if strings.Contains(name, pattern) {
			return ClassificationResult{
				Classification: ClassHybrid,
				Confidence:     0.6,
				Method:         "heuristic",
				Reason:         "Name matches hybrid pattern: " + pattern,
				CanReadData:    true,
				CanExfiltrate:  true,
			}
		}
	}
	return ClassificationResult{
		Classification: ClassUnknown,
		Confidence:     0.0,
		Method:         "none",
		Reason:         "No matching pattern found",
	}
}
```

## Step 2: Content Hashing

```go
// internal/security/flow/hasher.go
package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// HashContent produces a truncated SHA256 hash (128 bits = 32 hex chars).
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:16]) // 128 bits
}

// HashContentNormalized produces a normalized hash (lowercase, trimmed).
func HashContentNormalized(content string) string {
	normalized := strings.ToLower(strings.TrimSpace(content))
	return HashContent(normalized)
}

// ExtractFieldHashes extracts per-field hashes from JSON content.
// Only hashes string values >= minLength characters.
func ExtractFieldHashes(jsonContent string, minLength int) map[string]string {
	hashes := make(map[string]string)

	var data interface{}
	if err := json.Unmarshal([]byte(jsonContent), &data); err != nil {
		// Not JSON — hash the full content
		if len(jsonContent) >= minLength {
			hashes[HashContent(jsonContent)] = "full_content"
		}
		return hashes
	}

	extractStrings(data, "", minLength, hashes)
	return hashes
}

func extractStrings(data interface{}, path string, minLen int, hashes map[string]string) {
	switch v := data.(type) {
	case string:
		if len(v) >= minLen {
			hashes[HashContent(v)] = path
			hashes[HashContentNormalized(v)] = path + ".normalized"
		}
	case map[string]interface{}:
		for key, val := range v {
			p := path + "." + key
			if path == "" {
				p = key
			}
			extractStrings(val, p, minLen, hashes)
		}
	case []interface{}:
		for i, val := range v {
			extractStrings(val, path+"["+string(rune('0'+i))+"]", minLen, hashes)
		}
	}
}
```

## Step 3: Flow Tracker

```go
// internal/security/flow/tracker.go
package flow

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// FlowTracker tracks data origins and detects cross-boundary flows per session.
type FlowTracker struct {
	sessions map[string]*FlowSession
	mu       sync.RWMutex
	config   *FlowTrackingConfig
}

type FlowTrackingConfig struct {
	SessionTimeoutMin    int
	MaxOriginsPerSession int
	HashMinLength        int
	MaxResponseHashBytes int
}

// FlowSession tracks origins and flows within a single agent session.
type FlowSession struct {
	ID                string
	StartTime         time.Time
	LastActivity      time.Time
	LinkedMCPSessions []string
	Origins           map[string]*DataOrigin // content hash → origin
	Flows             []*FlowEdge
	mu                sync.RWMutex
}

// NewFlowTracker creates a flow tracker with the given configuration.
func NewFlowTracker(config *FlowTrackingConfig) *FlowTracker {
	return &FlowTracker{
		sessions: make(map[string]*FlowSession),
		config:   config,
	}
}

// RecordOrigin stores data origin hashes from a PostToolUse response.
func (ft *FlowTracker) RecordOrigin(sessionID, toolName, serverName string,
	classification Classification, responseContent string,
	hasSensitive bool, sensitiveTypes []string) {

	session := ft.getOrCreateSession(sessionID)
	session.mu.Lock()
	defer session.mu.Unlock()

	session.LastActivity = time.Now()

	// Truncate if needed
	content := responseContent
	if len(content) > ft.config.MaxResponseHashBytes {
		content = content[:ft.config.MaxResponseHashBytes]
	}

	// Full content hash
	fullHash := HashContent(content)
	origin := &DataOrigin{
		ContentHash:      fullHash,
		ToolName:         toolName,
		ServerName:       serverName,
		Classification:   classification,
		HasSensitiveData: hasSensitive,
		SensitiveTypes:   sensitiveTypes,
		Timestamp:        time.Now(),
	}
	session.Origins[fullHash] = origin

	// Per-field hashes
	fieldHashes := ExtractFieldHashes(content, ft.config.HashMinLength)
	for hash := range fieldHashes {
		if _, exists := session.Origins[hash]; !exists {
			session.Origins[hash] = origin
		}
	}

	// Evict oldest if over limit
	ft.evictOldest(session)
}

// CheckFlow evaluates PreToolUse arguments for data flow matches.
func (ft *FlowTracker) CheckFlow(sessionID, toolName, serverName string,
	destClassification Classification, argsJSON string) []*FlowEdge {

	session := ft.getSession(sessionID)
	if session == nil {
		return nil
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	session.LastActivity = time.Now()

	var edges []*FlowEdge

	// Hash the arguments at multiple granularities
	argsHashes := make(map[string]bool)

	// Full content hash
	argsHashes[HashContent(argsJSON)] = true
	argsHashes[HashContentNormalized(argsJSON)] = true

	// Per-field hashes
	fieldHashes := ExtractFieldHashes(argsJSON, ft.config.HashMinLength)
	for hash := range fieldHashes {
		argsHashes[hash] = true
	}

	// Check each hash against recorded origins
	for hash := range argsHashes {
		if origin, found := session.Origins[hash]; found {
			flowType := determineFlowType(origin.Classification, destClassification)
			riskLevel := assessRisk(flowType, origin.HasSensitiveData)

			edge := &FlowEdge{
				ID:               ulid.Make().String(),
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
			session.Flows = append(session.Flows, edge)
		}
	}

	return edges
}

func determineFlowType(from, to Classification) FlowType {
	// Hybrid treated as internal for source, external for destination
	fromEff := from
	if fromEff == ClassHybrid {
		fromEff = ClassInternal
	}
	toEff := to
	if toEff == ClassHybrid {
		toEff = ClassExternal
	}

	switch {
	case fromEff == ClassInternal && toEff == ClassExternal:
		return FlowInternalToExternal
	case fromEff == ClassExternal && toEff == ClassInternal:
		return FlowExternalToInternal
	case fromEff == ClassInternal && toEff == ClassInternal:
		return FlowInternalToInternal
	default:
		return FlowExternalToExternal
	}
}

func assessRisk(flowType FlowType, hasSensitiveData bool) RiskLevel {
	if flowType != FlowInternalToExternal {
		return RiskNone
	}
	if hasSensitiveData {
		return RiskCritical
	}
	return RiskMedium
}

// Helper methods

func (ft *FlowTracker) getOrCreateSession(id string) *FlowSession {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	if s, ok := ft.sessions[id]; ok {
		return s
	}
	s := &FlowSession{
		ID:        id,
		StartTime: time.Now(),
		Origins:   make(map[string]*DataOrigin),
	}
	ft.sessions[id] = s
	return s
}

func (ft *FlowTracker) getSession(id string) *FlowSession {
	ft.mu.RLock()
	defer ft.mu.RUnlock()
	return ft.sessions[id]
}

func (ft *FlowTracker) evictOldest(session *FlowSession) {
	if len(session.Origins) <= ft.config.MaxOriginsPerSession {
		return
	}
	// Find and remove oldest origins until under limit
	for len(session.Origins) > ft.config.MaxOriginsPerSession {
		var oldestHash string
		var oldestTime time.Time
		for hash, origin := range session.Origins {
			if oldestHash == "" || origin.Timestamp.Before(oldestTime) {
				oldestHash = hash
				oldestTime = origin.Timestamp
			}
		}
		delete(session.Origins, oldestHash)
	}
}
```

## Step 4: Hook Evaluate CLI Command

```go
// cmd/mcpproxy/hook_cmd.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Agent hook integration commands",
}

var hookEvaluateCmd = &cobra.Command{
	Use:   "evaluate",
	Short: "Evaluate a tool call from agent hook (reads JSON from stdin)",
	RunE:  runHookEvaluate,
}

var hookInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install hook configuration for an agent",
	RunE:  runHookInstall,
}

var hookStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show hook installation status",
	RunE:  runHookStatus,
}

func runHookEvaluate(cmd *cobra.Command, args []string) error {
	// FAST PATH: No config loading, no file logger

	// 1. Read JSON from stdin
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		// Fail open
		return outputClaudeCodeResponse("approve", "")
	}

	// 2. Detect socket path
	socketPath := detectSocketPath()
	if socketPath == "" {
		return outputClaudeCodeResponse("approve", "")
	}

	// 3. POST to daemon via Unix socket
	resp, err := postToSocket(socketPath, "/api/v1/hooks/evaluate", input)
	if err != nil {
		// Fail open
		return outputClaudeCodeResponse("approve", "")
	}

	// 4. Translate response to Claude Code protocol
	var evalResp struct {
		Decision  string `json:"decision"`
		Reason    string `json:"reason"`
		RiskLevel string `json:"risk_level"`
	}
	json.Unmarshal(resp, &evalResp)

	// Map internal decision to Claude Code protocol
	decision := "approve"
	switch evalResp.Decision {
	case "deny":
		decision = "block"
	case "ask":
		decision = "ask"
	}

	return outputClaudeCodeResponse(decision, evalResp.Reason)
}

func outputClaudeCodeResponse(decision, reason string) error {
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"result": map[string]interface{}{
			"decision": decision,
		},
	}
	if reason != "" {
		response["result"].(map[string]interface{})["reason"] = reason
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}
```

## Step 5: Hook Evaluate HTTP Endpoint

```go
// internal/httpapi/hooks.go
package httpapi

import (
	"encoding/json"
	"net/http"
)

// HandleHookEvaluate processes hook evaluation requests.
func (s *Server) HandleHookEvaluate(w http.ResponseWriter, r *http.Request) {
	var req HookEvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Delegate to flow service
	resp := s.flowService.Evaluate(req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Register in setupRoutes():
//   r.Route("/api/v1", func(r chi.Router) {
//       ...
//       r.Post("/hooks/evaluate", s.HandleHookEvaluate)
//   })
```

## Step 6: Write Tests First (TDD)

```go
// internal/security/flow/flow_test.go
package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifier_InternalTools(t *testing.T) {
	c := NewClassifier(nil)

	tests := []struct {
		toolName string
		expected Classification
	}{
		{"Read", ClassInternal},
		{"Write", ClassInternal},
		{"Glob", ClassInternal},
		{"Grep", ClassInternal},
		{"WebFetch", ClassExternal},
		{"Bash", ClassHybrid},
	}
	for _, tc := range tests {
		t.Run(tc.toolName, func(t *testing.T) {
			result := c.Classify("", tc.toolName)
			assert.Equal(t, tc.expected, result.Classification)
		})
	}
}

func TestClassifier_ServerHeuristics(t *testing.T) {
	c := NewClassifier(nil)

	tests := []struct {
		serverName string
		expected   Classification
	}{
		{"postgres-db", ClassInternal},
		{"github-private", ClassInternal},
		{"slack-notifications", ClassExternal},
		{"email-sender", ClassExternal},
		{"aws-lambda", ClassHybrid},
	}
	for _, tc := range tests {
		t.Run(tc.serverName, func(t *testing.T) {
			result := c.Classify(tc.serverName, "some_tool")
			assert.Equal(t, tc.expected, result.Classification)
		})
	}
}

func TestClassifier_ConfigOverride(t *testing.T) {
	c := NewClassifier(map[string]Classification{
		"my-private-slack": ClassInternal,
	})

	result := c.Classify("my-private-slack", "post_message")
	assert.Equal(t, ClassInternal, result.Classification)
	assert.Equal(t, "config", result.Method)
}

func TestFlowTracker_DetectsExfiltration(t *testing.T) {
	tracker := NewFlowTracker(&FlowTrackingConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	})

	// Agent reads a file containing a secret
	secretContent := `{"api_key": "sk-proj-abc123def456ghi789jkl012mno345"}`
	tracker.RecordOrigin("session-1", "Read", "", ClassInternal,
		secretContent, true, []string{"api_token"})

	// Agent tries to send that data to an external URL
	edges := tracker.CheckFlow("session-1", "WebFetch", "", ClassExternal, secretContent)

	assert.Len(t, edges, 1)
	assert.Equal(t, FlowInternalToExternal, edges[0].FlowType)
	assert.Equal(t, RiskCritical, edges[0].RiskLevel)
}

func TestFlowTracker_AllowsInternalToInternal(t *testing.T) {
	tracker := NewFlowTracker(&FlowTrackingConfig{
		SessionTimeoutMin:    30,
		MaxOriginsPerSession: 10000,
		HashMinLength:        20,
		MaxResponseHashBytes: 65536,
	})

	content := `{"data": "some internal content that is long enough"}`
	tracker.RecordOrigin("session-1", "Read", "", ClassInternal,
		content, false, nil)

	edges := tracker.CheckFlow("session-1", "Write", "", ClassInternal, content)

	if len(edges) > 0 {
		assert.Equal(t, FlowInternalToInternal, edges[0].FlowType)
		assert.Equal(t, RiskNone, edges[0].RiskLevel)
	}
}

func TestContentHashing_FieldExtraction(t *testing.T) {
	content := `{"key": "sk-proj-abc123def456ghi789jkl012mno345", "short": "hi"}`
	hashes := ExtractFieldHashes(content, 20)

	// Should hash the long key value but not "hi"
	assert.Greater(t, len(hashes), 0)

	// Verify the long value hash is present
	expectedHash := HashContent("sk-proj-abc123def456ghi789jkl012mno345")
	_, found := hashes[expectedHash]
	assert.True(t, found, "Long field value should be hashed")
}
```

## Step 7: Test Hook CLI Manually

```bash
# Build MCPProxy
make build

# Start server
./mcpproxy serve --log-level=debug

# Test hook evaluate (simulating a PreToolUse for Read)
echo '{"event":"PreToolUse","session_id":"test-session","tool_name":"Read","tool_input":{"file_path":"/home/user/.env"}}' | ./mcpproxy hook evaluate

# Install hooks for Claude Code
./mcpproxy hook install --agent claude-code --scope project

# Check hook status
./mcpproxy hook status
```

## Next Steps

1. Implement session correlation (Mechanism A) in `handleCallToolVariant`
2. Add policy engine with configurable actions
3. Add activity log integration for hook evaluations
4. Add CLI filter flags (`--flow-type`, `--risk-level`)
5. Add SSE event for `flow.alert`
6. Add E2E tests with full hook→daemon→MCP pipeline

## References

- [spec.md](./spec.md) - Full feature specification
- [research.md](./research.md) - Research decisions (8 topics)
- [data-model.md](./data-model.md) - Entity model and enumerations
- [contracts/hook-evaluate-api.yaml](./contracts/hook-evaluate-api.yaml) - OpenAPI contract
- [contracts/go-types.go](./contracts/go-types.go) - Go type definitions
- [contracts/config-schema.json](./contracts/config-schema.json) - Configuration schema
