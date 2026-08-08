package config

import (
	"fmt"
	"time"
)

// defaultQueueTimeout is the wait budget applied to an ACTIVE limiter scope
// that does not configure queue_timeout explicitly (spec 093, FR-020). It is
// deliberately not part of DefaultConfig: an unset key must keep a config
// byte-identical to a pre-feature one, and the value only matters once a
// limiter exists.
const defaultQueueTimeout = 30 * time.Second

// ConcurrencyDefaults is the per-server DEFAULT SET — scope (b) of FR-020.
// Every setting is tri-state (nil = not configured); the same three settings
// exist verbatim on ServerConfig as per-server overrides and at the top level
// as the global aggregate limiter.
type ConcurrencyDefaults struct {
	MaxConcurrentRequests *int      `json:"max_concurrent_requests,omitempty" mapstructure:"max_concurrent_requests"`
	QueueSize             *int      `json:"queue_size,omitempty" mapstructure:"queue_size"`
	QueueTimeout          *Duration `json:"queue_timeout,omitempty" mapstructure:"queue_timeout" swaggertype:"string"`
}

// ResolvedConcurrency is one scope's settings after tri-state resolution. It is
// the shape the limiter registry consumes.
type ResolvedConcurrency struct {
	MaxConcurrentRequests int
	QueueSize             int
	QueueTimeout          time.Duration
}

// Enabled reports whether this scope actually caps concurrency.
func (r ResolvedConcurrency) Enabled() bool { return r.MaxConcurrentRequests > 0 }

// resolveConcurrency applies the "override → default set → unset" precedence
// per setting, then fills in the queue-timeout default for an active limiter.
func resolveConcurrency(overrideMax, defaultMax *int, overrideQueue, defaultQueue *int, overrideTimeout, defaultTimeout *Duration) ResolvedConcurrency {
	pick := func(override, def *int) int {
		if override != nil {
			return *override
		}
		if def != nil {
			return *def
		}
		return 0
	}
	pickDur := func(override, def *Duration) time.Duration {
		if override != nil {
			return override.Duration()
		}
		if def != nil {
			return def.Duration()
		}
		return 0
	}

	res := ResolvedConcurrency{
		MaxConcurrentRequests: pick(overrideMax, defaultMax),
		QueueSize:             pick(overrideQueue, defaultQueue),
		QueueTimeout:          pickDur(overrideTimeout, defaultTimeout),
	}
	if !res.Enabled() {
		// A disabled scope has no queue and no wait budget: the pending
		// capacity of a limiter that does not exist is meaningless, and this
		// is what makes an explicit `max_concurrent_requests: 0` a complete
		// opt-out for a server that inherits a queue size from the default set.
		res.QueueSize = 0
		res.QueueTimeout = 0
		return res
	}
	if res.QueueTimeout <= 0 {
		res.QueueTimeout = defaultQueueTimeout
	}
	return res
}

// ResolveGlobalConcurrency resolves the global aggregate limiter — scope (a) of
// FR-020. Absent or 0 max = no global limiter.
func (c *Config) ResolveGlobalConcurrency() ResolvedConcurrency {
	if c == nil {
		return ResolvedConcurrency{}
	}
	return resolveConcurrency(c.MaxConcurrentRequests, nil, c.QueueSize, nil, c.QueueTimeout, nil)
}

// ResolveServerConcurrency resolves a server's per-server limiter — scopes (b)
// and (c) of FR-020: per-server override → per-server default set → unset. The
// global aggregate limiter is never an inheritance source here; it applies on
// top of the resolved value (effective concurrency = min of the two).
func (c *Config) ResolveServerConcurrency(sc *ServerConfig) ResolvedConcurrency {
	if c == nil {
		return ResolvedConcurrency{}
	}
	var defMax, defQueue *int
	var defTimeout *Duration
	if c.ServerConcurrencyDefaults != nil {
		defMax = c.ServerConcurrencyDefaults.MaxConcurrentRequests
		defQueue = c.ServerConcurrencyDefaults.QueueSize
		defTimeout = c.ServerConcurrencyDefaults.QueueTimeout
	}
	var srvMax, srvQueue *int
	var srvTimeout *Duration
	if sc != nil {
		srvMax = sc.MaxConcurrentRequests
		srvQueue = sc.QueueSize
		srvTimeout = sc.QueueTimeout
	}
	return resolveConcurrency(srvMax, defMax, srvQueue, defQueue, srvTimeout, defTimeout)
}

// validateConcurrencyScope implements FR-023 for one resolved scope: negative
// values are rejected, and a positive queue size is rejected when the scope's
// concurrency limit resolves to disabled/unlimited. scopeLabel names the scope
// in the error field (e.g. "" for the global aggregate,
// "server_concurrency_defaults", or "mcpServers[0] (db)").
func validateConcurrencyScope(fieldPrefix, scopeName string, maxPtr, queuePtr *int, timeoutPtr *Duration, resolvedMax, resolvedQueue int, explicitlyDisabled bool) []ValidationError {
	var errs []ValidationError
	field := func(name string) string {
		if fieldPrefix == "" {
			return name
		}
		return fieldPrefix + "." + name
	}

	if maxPtr != nil && *maxPtr < 0 {
		errs = append(errs, ValidationError{
			Field:   field("max_concurrent_requests"),
			Message: fmt.Sprintf("cannot be negative (%s): use 0 to disable the limiter or a positive cap", scopeName),
		})
	}
	if queuePtr != nil && *queuePtr < 0 {
		errs = append(errs, ValidationError{
			Field:   field("queue_size"),
			Message: fmt.Sprintf("cannot be negative (%s): use 0 for no pending capacity or a positive queue length", scopeName),
		})
	}
	if timeoutPtr != nil && timeoutPtr.Duration() < 0 {
		errs = append(errs, ValidationError{
			Field:   field("queue_timeout"),
			Message: fmt.Sprintf("cannot be negative (%s): use a positive duration such as \"30s\"", scopeName),
		})
	}

	// A queue attached to a limit that is not active can never admit anything.
	// Skipped when the scope explicitly opted out with max_concurrent_requests:
	// 0 — that is the documented way to disable a server that would otherwise
	// inherit a queue size from the per-server default set (FR-020(c)).
	if resolvedQueue > 0 && resolvedMax <= 0 && !explicitlyDisabled {
		errs = append(errs, ValidationError{
			Field: field("queue_size"),
			Message: fmt.Sprintf("queue_size %d requires max_concurrent_requests > 0 (%s): set a positive max_concurrent_requests, or queue_size 0 to remove the queue",
				resolvedQueue, scopeName),
		})
	}
	return errs
}

// validateConcurrency validates all three scopes of FR-020 after resolution.
func (c *Config) validateConcurrency() []ValidationError {
	var errs []ValidationError

	// (a) global aggregate limiter.
	global := c.ResolveGlobalConcurrency()
	globalQueue := global.QueueSize
	if !global.Enabled() && c.QueueSize != nil {
		globalQueue = *c.QueueSize
	}
	errs = append(errs, validateConcurrencyScope("", "global aggregate limiter",
		c.MaxConcurrentRequests, c.QueueSize, c.QueueTimeout,
		global.MaxConcurrentRequests, globalQueue, false)...)

	// (b) per-server default set. Resolved standalone: it is what a server with
	// no overrides inherits.
	if d := c.ServerConcurrencyDefaults; d != nil {
		resolved := resolveConcurrency(nil, d.MaxConcurrentRequests, nil, d.QueueSize, nil, d.QueueTimeout)
		queue := resolved.QueueSize
		if !resolved.Enabled() && d.QueueSize != nil {
			queue = *d.QueueSize
		}
		errs = append(errs, validateConcurrencyScope("server_concurrency_defaults", "per-server default set",
			d.MaxConcurrentRequests, d.QueueSize, d.QueueTimeout,
			resolved.MaxConcurrentRequests, queue, false)...)
	}

	// (c) per-server overrides, validated on the RESOLVED values so an
	// inherited limit satisfies an explicit queue size (and vice versa).
	for i, server := range c.Servers {
		if server == nil {
			continue
		}
		resolved := c.ResolveServerConcurrency(server)
		queue := resolved.QueueSize
		if !resolved.Enabled() {
			// Re-derive the pre-disable queue value so an orphaned queue is
			// still reported (unless the server explicitly opted out).
			switch {
			case server.QueueSize != nil:
				queue = *server.QueueSize
			case c.ServerConcurrencyDefaults != nil && c.ServerConcurrencyDefaults.QueueSize != nil:
				queue = *c.ServerConcurrencyDefaults.QueueSize
			}
		}
		explicitOptOut := server.MaxConcurrentRequests != nil && *server.MaxConcurrentRequests == 0
		errs = append(errs, validateConcurrencyScope(
			fmt.Sprintf("mcpServers[%d]", i),
			fmt.Sprintf("server %q", server.Name),
			server.MaxConcurrentRequests, server.QueueSize, server.QueueTimeout,
			resolved.MaxConcurrentRequests, queue, explicitOptOut)...)
	}

	return errs
}
