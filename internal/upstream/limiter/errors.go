package limiter

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Scope identifies which limiter tier produced a decision (spec 093, FR-010).
type Scope string

const (
	// ScopeServer is a per-upstream-server limiter.
	ScopeServer Scope = "server"
	// ScopeGlobal is the proxy-wide aggregate limiter.
	ScopeGlobal Scope = "global"
	// ScopeToken is a per-agent-token call budget (issue #1699).
	ScopeToken Scope = "token"
)

// Reason is the stable machine-readable cause of a rejection (FR-012).
type Reason string

const (
	// ReasonQueueFull means the wait queue had no pending capacity, so the call
	// was shed immediately without waiting.
	ReasonQueueFull Reason = "queue_full"
	// ReasonQueueTimeout means the call waited past its absolute queue deadline.
	ReasonQueueTimeout Reason = "queue_timeout"
	// ReasonServerUnavailable means the target server was disabled, quarantined
	// or removed while the call was queued (or just before it enqueued).
	ReasonServerUnavailable Reason = "server_unavailable"
	// ReasonRateLimited means the calling agent token used up its call budget
	// (rate_limit / agent_token_rate_limit). The call was refused before it
	// queued for a concurrency slot and never reached the upstream.
	ReasonRateLimited Reason = "rate_limited"
)

// Sentinel errors for errors.Is matching at the shed-semantics seams (MCP
// isError results, REST 429 mapping, activity "rejected" status). The concrete
// error returned by Acquire is always a *LimitError, which reports Is() true
// for the sentinel matching its Reason.
var (
	// ErrQueueFull matches a rejection caused by a full (or zero-sized) queue.
	ErrQueueFull = errors.New("concurrency limit: queue full")
	// ErrQueueTimeout matches a rejection caused by the queue deadline expiring.
	ErrQueueTimeout = errors.New("concurrency limit: queue timeout")
	// ErrServerUnavailable matches a rejection caused by the target server being
	// disabled/quarantined/removed (FR-009).
	ErrServerUnavailable = errors.New("concurrency limit: server unavailable")
	// ErrRateLimited matches a call refused because its agent token is over its
	// call budget.
	ErrRateLimited = errors.New("rate limit: token over its call budget")
)

// LimitError is the typed rejection identity that must survive end-to-end
// through every dispatch path (FR-011). It carries everything the shed seams
// need: which tier shed the call, why, which server (empty for the global
// scope — the message must never blame a server for a proxy-wide limit), the
// limit that was in force, and the Retry-After hint derived from the shedding
// scope's effective queue_timeout.
type LimitError struct {
	Scope      Scope
	Reason     Reason
	Server     string
	Limit      int
	RetryAfter time.Duration
	// Burst is the bucket size of a ScopeToken rejection (0 for the other
	// scopes, and for a token whose burst equals its per-minute rate).
	Burst int
}

// Shed reports whether the call was refused by a limit (queue full, queue
// timeout or the token's call budget) rather than because the server went
// away. Every dispatch path answers a shed the same way — a retry-friendly
// error result, or 429 with Retry-After — and the admission observer has
// already written the call's "rejected" record.
func (e *LimitError) Shed() bool {
	if e == nil {
		return false
	}
	switch e.Reason {
	case ReasonQueueFull, ReasonQueueTimeout, ReasonRateLimited:
		return true
	default:
		return false
	}
}

func (e *LimitError) Error() string {
	switch {
	case e.Reason == ReasonRateLimited:
		// One wording everywhere (MCP result, REST body, activity record, the
		// text a sandboxed script sees): it already carries the retry hint.
		return e.UserMessage()
	case e.Reason == ReasonServerUnavailable:
		if e.Server != "" {
			return fmt.Sprintf("upstream %q is not available (disabled, quarantined or removed)", e.Server)
		}
		return "upstream is not available (disabled, quarantined or removed)"
	case e.Scope == ScopeGlobal:
		return fmt.Sprintf("mcpproxy is busy: the proxy-wide concurrency limit (%d) is saturated (%s) — please retry shortly",
			e.Limit, e.Reason)
	default:
		return fmt.Sprintf("upstream server %q is busy: its concurrency limit (%d) is saturated (%s) — please retry shortly",
			e.Server, e.Limit, e.Reason)
	}
}

// RetryAdvice is the closing sentence of every shed message. Agents read
// tool-call error text; telling them the failure is transient is what turns a
// rejection into a retry instead of an abandoned task (FR-010).
const RetryAdvice = "Retry in a few seconds."

// UserMessage renders the caller-facing explanation of a shed (FR-010). It is
// the single rendering of that text: the MCP isError result, the REST 429 body
// and the activity record all use it, so an operator never sees two different
// descriptions of the same rejection.
//
// The global branch NEVER names a server: the proxy-wide limiter shed the call
// because the whole instance is saturated, and blaming the upstream the call
// happened to target would send an operator debugging the wrong thing.
func (e *LimitError) UserMessage() string {
	if e.Reason == ReasonServerUnavailable {
		return e.Error()
	}
	if e.Reason == ReasonRateLimited {
		secs := int(math.Ceil(e.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		unit := "seconds"
		if secs == 1 {
			unit = "second"
		}
		return fmt.Sprintf(
			"Rate limit reached: this agent token may make %d tool calls per minute%s and has used its allowance. Retry in %d %s.",
			e.Limit, e.burstClause(), secs, unit)
	}

	cause := "its wait queue is full"
	if e.Reason == ReasonQueueTimeout {
		cause = "the call waited longer than the configured queue timeout"
	}

	if e.Scope == ScopeGlobal {
		return fmt.Sprintf(
			"mcpproxy is busy: the proxy-wide concurrency limit of %d simultaneous tool calls is saturated and %s. %s",
			e.Limit, cause, RetryAdvice)
	}
	return fmt.Sprintf(
		"Server %q is busy: it is already running its maximum of %d simultaneous tool calls and %s. %s",
		e.Server, e.Limit, cause, RetryAdvice)
}

// burstClause renders the bucket size when it differs from the per-minute
// rate, e.g. " (bursts of up to 5)".
func (e *LimitError) burstClause() string {
	if e.Burst > 0 && e.Burst != e.Limit {
		return fmt.Sprintf(" (bursts of up to %d)", e.Burst)
	}
	return ""
}

// Is makes errors.Is(err, ErrQueueFull|ErrQueueTimeout|ErrServerUnavailable|ErrRateLimited)
// work against the typed error.
func (e *LimitError) Is(target error) bool {
	switch target {
	case ErrQueueFull:
		return e.Reason == ReasonQueueFull
	case ErrQueueTimeout:
		return e.Reason == ReasonQueueTimeout
	case ErrServerUnavailable:
		return e.Reason == ReasonServerUnavailable
	case ErrRateLimited:
		return e.Reason == ReasonRateLimited
	default:
		return false
	}
}
