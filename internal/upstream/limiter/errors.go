package limiter

import (
	"errors"
	"fmt"
	"time"
)

// Scope identifies which limiter tier produced a decision (spec 093, FR-010).
type Scope string

const (
	// ScopeServer is a per-upstream-server limiter.
	ScopeServer Scope = "server"
	// ScopeGlobal is the proxy-wide aggregate limiter.
	ScopeGlobal Scope = "global"
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
}

func (e *LimitError) Error() string {
	switch {
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

// Is makes errors.Is(err, ErrQueueFull|ErrQueueTimeout|ErrServerUnavailable)
// work against the typed error.
func (e *LimitError) Is(target error) bool {
	switch target {
	case ErrQueueFull:
		return e.Reason == ReasonQueueFull
	case ErrQueueTimeout:
		return e.Reason == ReasonQueueTimeout
	case ErrServerUnavailable:
		return e.Reason == ReasonServerUnavailable
	default:
		return false
	}
}
