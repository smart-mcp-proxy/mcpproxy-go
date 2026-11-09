package limiter

import (
	"context"
	"time"
)

// Rejection describes one shed call. It is the payload of the ORIGIN-INDEPENDENT
// rejection seam required by FR-012/FR-013: the observer is invoked at the
// admission point inside the managed client, which every in-process dispatch
// path funnels through (MCP tool-call variants, the REST tool-call endpoint,
// sandboxed code execution, activity replay). Paths that never reach the MCP
// dispatch layer therefore still produce a "rejected" activity record and a
// rejection metric.
type Rejection struct {
	// Server is the upstream the call targeted. Always set, even for a global
	// rejection — the caller-facing MESSAGE must not blame it (FR-010), but the
	// activity record and the metric label still need to know where the call was
	// headed.
	Server string
	// Tool is the upstream tool name (without the server prefix).
	Tool string
	// Scope is the tier that shed the call: server, global or token.
	Scope Scope
	// Reason is queue_full, queue_timeout or rate_limited.
	Reason Reason
	// Limit is the cap that was in force in the shedding scope.
	Limit int
	// RetryAfter is the shedding scope's effective queue_timeout, used as the
	// REST Retry-After hint (FR-011).
	RetryAfter time.Duration
	// Burst is the bucket size of a ScopeToken rejection (0 otherwise).
	Burst int
	// Token names the agent token that was refused. Set for ScopeToken only.
	Token TokenIdentity
	// Waited is how long the call spent in the queue before being shed (0 for a
	// queue_full shed, which never waits).
	Waited time.Duration
	// Message is the caller-facing explanation (LimitError.Error()).
	Message string
}

// TokenIdentity is the agent token behind a rate-limited call, as much of it as
// the activity record needs. Never the secret.
type TokenIdentity struct {
	Name string
	// Prefix is the 12-character display prefix of the token.
	Prefix string
	// ClientID is set for a client credential.
	ClientID string
}

// Observer receives every shed call. Implementations must not block: they run
// on the caller's goroutine at the moment of rejection.
type Observer func(ctx context.Context, rej Rejection)
