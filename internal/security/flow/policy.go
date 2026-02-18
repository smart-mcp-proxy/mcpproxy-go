package flow

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// PolicyConfig configures the PolicyEvaluator.
type PolicyConfig struct {
	InternalToExternal    PolicyAction            // Default action for internal→external flows
	SensitiveDataExternal PolicyAction            // Action when sensitive data flows externally
	RequireJustification  bool                    // Whether to require justification for flows
	SuspiciousEndpoints   []string                // Endpoints that always deny
	ToolOverrides         map[string]PolicyAction // tool name → override action
}

// PolicyEvaluator evaluates flow edges against configured policy rules.
type PolicyEvaluator struct {
	mu     sync.RWMutex
	config *PolicyConfig
}

// NewPolicyEvaluator creates a PolicyEvaluator with the given configuration.
func NewPolicyEvaluator(config *PolicyConfig) *PolicyEvaluator {
	return &PolicyEvaluator{config: config}
}

// UpdateConfig replaces the policy configuration for hot-reload.
func (pe *PolicyEvaluator) UpdateConfig(config *PolicyConfig) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.config = config
}

// Evaluate returns the policy decision for a set of flow edges.
// mode is "proxy_only" or "hook_enhanced".
// In proxy_only mode, PolicyAsk degrades to PolicyWarn since there's no agent UI.
func (pe *PolicyEvaluator) Evaluate(edges []*FlowEdge, mode string) (PolicyAction, string) {
	if len(edges) == 0 {
		return PolicyAllow, "no data flows detected"
	}

	pe.mu.RLock()
	defer pe.mu.RUnlock()

	highestAction := PolicyAllow
	highestSeverity := -1
	reason := "no escalation needed"

	for _, edge := range edges {
		action, edgeReason := pe.evaluateEdgeLocked(edge)
		severity := policyActionSeverity(action)

		if severity > highestSeverity {
			highestAction = action
			highestSeverity = severity
			reason = edgeReason
		}
	}

	// Degrade PolicyAsk to PolicyWarn in proxy_only mode
	if mode == "proxy_only" && highestAction == PolicyAsk {
		highestAction = PolicyWarn
		reason = fmt.Sprintf("degraded from ask to warn in proxy_only mode: %s", reason)
	}

	return highestAction, reason
}

// evaluateEdgeLocked evaluates a single edge. Caller must hold pe.mu.RLock().
func (pe *PolicyEvaluator) evaluateEdgeLocked(edge *FlowEdge) (PolicyAction, string) {
	// 1. Check tool overrides first
	if override, ok := pe.config.ToolOverrides[edge.ToToolName]; ok {
		return override, fmt.Sprintf("tool override for %s: %s", edge.ToToolName, override)
	}

	// 2. Check suspicious endpoints
	for _, endpoint := range pe.config.SuspiciousEndpoints {
		if strings.Contains(strings.ToLower(edge.ToServerName), strings.ToLower(endpoint)) {
			return PolicyDeny, fmt.Sprintf("suspicious endpoint detected: %s", endpoint)
		}
	}

	// 3. Evaluate based on flow type
	switch edge.FlowType {
	case FlowInternalToExternal:
		return pe.evaluateInternalToExternal(edge)
	case FlowInternalToInternal, FlowExternalToExternal, FlowExternalToInternal:
		return PolicyAllow, fmt.Sprintf("safe flow type: %s", edge.FlowType)
	default:
		return PolicyAllow, "unknown flow type, allowing by default"
	}
}

func (pe *PolicyEvaluator) evaluateInternalToExternal(edge *FlowEdge) (PolicyAction, string) {
	// Sensitive data flowing externally — use configured action (default: deny)
	if edge.FromOrigin != nil && edge.FromOrigin.HasSensitiveData {
		sensitiveTypes := ""
		if edge.FromOrigin != nil && len(edge.FromOrigin.SensitiveTypes) > 0 {
			sensitiveTypes = strings.Join(edge.FromOrigin.SensitiveTypes, ", ")
		}
		return pe.config.SensitiveDataExternal, fmt.Sprintf(
			"sensitive data (%s) flowing from %s to external tool %s",
			sensitiveTypes, edge.FromOrigin.ToolName, edge.ToToolName,
		)
	}

	// Non-sensitive internal→external — use configured default (default: ask)
	return pe.config.InternalToExternal, fmt.Sprintf(
		"internal data flowing to external tool %s (risk: %s)",
		edge.ToToolName, edge.RiskLevel,
	)
}

// urlPattern matches URL-like strings in tool arguments.
var urlPattern = regexp.MustCompile(`https?://[^\s"'\` + "`" + `\]\)}>]+`)

// CheckArgsForSuspiciousURLs scans tool arguments for URLs containing suspicious endpoints.
// Returns PolicyDeny with reason if a match is found, PolicyAllow otherwise.
func (pe *PolicyEvaluator) CheckArgsForSuspiciousURLs(argsJSON string) (PolicyAction, string) {
	if argsJSON == "" {
		return PolicyAllow, ""
	}

	pe.mu.RLock()
	endpoints := pe.config.SuspiciousEndpoints
	pe.mu.RUnlock()

	if len(endpoints) == 0 {
		return PolicyAllow, ""
	}

	// Extract all URLs from the args
	urls := urlPattern.FindAllString(argsJSON, -1)

	for _, u := range urls {
		lowerURL := strings.ToLower(u)
		for _, endpoint := range endpoints {
			if strings.Contains(lowerURL, strings.ToLower(endpoint)) {
				return PolicyDeny, fmt.Sprintf("suspicious endpoint URL detected in tool arguments: %s (matched: %s)", u, endpoint)
			}
		}
	}

	return PolicyAllow, ""
}
