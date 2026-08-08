package managed

import (
	"context"
	"errors"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// admissionControl is the concurrency-limit wiring the manager hands down to
// every managed client. It is swapped atomically (never mutated) so the
// hot-reload path can republish it without a lock.
type admissionControl struct {
	registry *limiter.Registry
	observe  limiter.Observer
}

// SetAdmissionControl installs the limiter registry and the rejection observer
// on this client. The managed client is the single choke point every in-process
// upstream tool call passes through (spec 093, Option A), so admission here
// covers the MCP tool-call variants, the REST tool-call endpoint, sandboxed
// code execution and activity replay alike (FR-003).
//
// Passing a nil registry disables admission for this client (the zero-config
// default, FR-006).
func (mc *Client) SetAdmissionControl(registry *limiter.Registry, observe limiter.Observer) {
	if mc == nil {
		return
	}
	if registry == nil && observe == nil {
		mc.admission.Store(nil)
		return
	}
	mc.admission.Store(&admissionControl{registry: registry, observe: observe})
}

// noopRelease is the release closure for a call that took no limiter slot.
func noopRelease() {}

// acquireAdmission admits one tool call through the per-server and global
// limiter tiers under ONE absolute queue deadline (FR-004).
//
// It runs BEFORE coreClient.CallTool, which is where the call_tool_timeout
// execution context is created — so queue waiting never eats the execution
// budget (FR-005). The caller's own context is still honoured while queued:
// a cancellation is reported as a cancellation, not as shedding.
//
// ListTools and the health-check Ping deliberately do NOT go through here
// (FR-007): they are coalesced/lightweight and must never be able to deadlock
// behind a saturated tool-call queue.
func (mc *Client) acquireAdmission(ctx context.Context, toolName string) (func(), error) {
	adm := mc.admission.Load()
	if adm == nil || adm.registry == nil {
		return noopRelease, nil
	}

	serverCfg := mc.GetConfig()
	serverName := ""
	if serverCfg != nil {
		serverName = serverCfg.Name
	}
	if !adm.registry.Active(serverName) {
		return noopRelease, nil
	}

	// One absolute deadline for the whole admission: the smallest positive
	// queue_timeout among the scopes that actually limit this call.
	var deadline time.Time
	globalCfg := mc.GetGlobalConfig()
	if globalCfg == nil {
		globalCfg = &config.Config{}
	}
	if budget := globalCfg.ResolveQueueBudget(serverCfg); budget > 0 {
		deadline = time.Now().Add(budget)
	}

	start := time.Now()
	release, err := adm.registry.Acquire(ctx, serverName, deadline)
	if err != nil {
		mc.reportRejection(ctx, adm, serverName, toolName, time.Since(start), err)
		return nil, err
	}
	return release, nil
}

// reportRejection forwards a shed to the origin-independent observer. Only
// queue_full / queue_timeout are sheds; server_unavailable is the existing
// "server went away" semantics (FR-009) and is reported through the normal
// error path, not as a rejection.
func (mc *Client) reportRejection(ctx context.Context, adm *admissionControl, serverName, toolName string, waited time.Duration, err error) {
	if adm.observe == nil {
		return
	}
	var limitErr *limiter.LimitError
	if !errors.As(err, &limitErr) {
		return
	}
	if limitErr.Reason != limiter.ReasonQueueFull && limitErr.Reason != limiter.ReasonQueueTimeout {
		return
	}
	adm.observe(ctx, limiter.Rejection{
		Server:     serverName,
		Tool:       toolName,
		Scope:      limitErr.Scope,
		Reason:     limitErr.Reason,
		Limit:      limitErr.Limit,
		RetryAfter: limitErr.RetryAfter,
		Waited:     waited,
		Message:    limitErr.Error(),
	})
}
