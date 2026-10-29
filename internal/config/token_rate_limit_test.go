package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// agent_token_rate_limit (issue #1699).

func rateLimitErrors(c *Config) []string {
	var out []string
	for _, e := range c.ValidateDetailed() {
		if strings.HasPrefix(e.Field, "agent_token_rate_limit") {
			out = append(out, e.Field+": "+e.Message)
		}
	}
	return out
}

func TestAgentTokenRateLimit_OffByDefault(t *testing.T) {
	c := DefaultConfig()
	if c.AgentTokenRateLimit != nil {
		t.Fatalf("the default config must not limit anything, got %+v", c.AgentTokenRateLimit)
	}
	if c.AgentTokenRateLimit.Enabled() {
		t.Fatal("a nil block must not be enabled")
	}
	if errs := rateLimitErrors(c); len(errs) != 0 {
		t.Fatalf("an absent block is valid, got %v", errs)
	}
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "agent_token_rate_limit") {
		t.Fatal("an unset block must not appear in the serialised config")
	}
}

func TestAgentTokenRateLimit_LoadsFromJSON(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"agent_token_rate_limit":{"per_minute":60,"burst":10}}`), &c); err != nil {
		t.Fatal(err)
	}
	if !c.AgentTokenRateLimit.Enabled() || c.AgentTokenRateLimit.PerMinute != 60 || c.AgentTokenRateLimit.Burst != 10 {
		t.Fatalf("got %+v", c.AgentTokenRateLimit)
	}
}

func TestValidateAgentTokenRateLimit(t *testing.T) {
	cases := []struct {
		name      string
		limit     *AgentTokenRateLimit
		wantField string
	}{
		{"absent", nil, ""},
		{"zero is off", &AgentTokenRateLimit{}, ""},
		{"rate only", &AgentTokenRateLimit{PerMinute: 60}, ""},
		{"rate and burst", &AgentTokenRateLimit{PerMinute: 60, Burst: 10}, ""},
		{"burst larger than the rate", &AgentTokenRateLimit{PerMinute: 6, Burst: 100}, ""},
		{"negative rate", &AgentTokenRateLimit{PerMinute: -1}, "agent_token_rate_limit.per_minute"},
		{"negative burst", &AgentTokenRateLimit{PerMinute: 60, Burst: -1}, "agent_token_rate_limit.burst"},
		{"burst without a rate", &AgentTokenRateLimit{Burst: 5}, "agent_token_rate_limit.burst"},
		{"rate above the bound", &AgentTokenRateLimit{PerMinute: MaxAgentTokenRatePerMinute + 1}, "agent_token_rate_limit.per_minute"},
		{"burst above the bound", &AgentTokenRateLimit{PerMinute: 1, Burst: MaxAgentTokenRatePerMinute + 1}, "agent_token_rate_limit.burst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Servers = nil
			c.AgentTokenRateLimit = tc.limit
			errs := rateLimitErrors(c)
			if tc.wantField == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0], tc.wantField) {
				t.Fatalf("expected an error naming %q, got %v", tc.wantField, errs)
			}
		})
	}
}
