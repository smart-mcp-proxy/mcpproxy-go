package config

import "fmt"

// MaxAgentTokenRatePerMinute bounds agent_token_rate_limit.per_minute and
// .burst. It mirrors auth.MaxRateLimitPerMinute (the same bound applies to a
// token's own rate_limit); the config package does not import auth.
const MaxAgentTokenRatePerMinute = 100000

// AgentTokenRateLimit is a per-agent-token call budget: a token bucket that
// holds Burst calls and refills at PerMinute calls a minute.
type AgentTokenRateLimit struct {
	// PerMinute is the sustained rate. 0 = no limit.
	PerMinute int `json:"per_minute" mapstructure:"per_minute"`
	// Burst is how many calls a token may make at once after a quiet spell.
	// 0 = the PerMinute value.
	Burst int `json:"burst,omitempty" mapstructure:"burst"`
}

// Enabled reports whether the budget limits anything. Nil-safe.
func (r *AgentTokenRateLimit) Enabled() bool { return r != nil && r.PerMinute > 0 }

// validate checks the budget. A nil budget is valid (the feature is off).
func (r *AgentTokenRateLimit) validate(field string) []ValidationError {
	if r == nil {
		return nil
	}
	var errs []ValidationError
	if r.PerMinute < 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".per_minute",
			Message: "cannot be negative: use 0 for no limit or a positive number of calls per minute",
		})
	}
	if r.PerMinute > MaxAgentTokenRatePerMinute {
		errs = append(errs, ValidationError{
			Field:   field + ".per_minute",
			Message: fmt.Sprintf("cannot exceed %d", MaxAgentTokenRatePerMinute),
		})
	}
	if r.Burst < 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".burst",
			Message: "cannot be negative: use 0 for the per_minute value or a positive number of calls",
		})
	}
	if r.Burst > MaxAgentTokenRatePerMinute {
		errs = append(errs, ValidationError{
			Field:   field + ".burst",
			Message: fmt.Sprintf("cannot exceed %d", MaxAgentTokenRatePerMinute),
		})
	}
	if r.PerMinute == 0 && r.Burst > 0 {
		errs = append(errs, ValidationError{
			Field:   field + ".burst",
			Message: "needs a per_minute above 0",
		})
	}
	return errs
}
