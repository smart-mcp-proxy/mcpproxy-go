package managed

import (
	"context"
	"errors"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
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

	// Issue #1699: an agent token's call budget is spent before the call takes
	// (or queues for) a concurrency slot, so a caller that is over budget costs
	// the proxy nothing and cannot crowd the queue.
	if err := mc.admitTokenRate(ctx, adm, serverName, toolName); err != nil {
		return nil, err
	}

	// The registry resolves everything this admission needs — both limiter
	// tiers and the ONE absolute queue deadline spanning them (FR-004) — from a
	// single published generation, so no value here can come from a different
	// generation than the caps it is applied to (FR-021).
	start := time.Now()
	release, err := adm.registry.Acquire(ctx, serverName)
	if err != nil {
		mc.reportRejection(ctx, adm, serverName, toolName, time.Since(start), err)
		return nil, err
	}
	return release, nil
}

// admitTokenRate charges one call to the calling agent token's budget. It
// refuses with a *limiter.LimitError{ScopeToken, ReasonRateLimited} when the
// token is over it, after reporting the rejection to the observer.
//
// Only an agent-token caller has a budget. The admin API key, the unix socket,
// an unauthenticated /mcp caller and every in-process call (activity replay
// under the admin's own context, a call with no request context at all) carry
// no agent identity and pass straight through.
func (mc *Client) admitTokenRate(ctx context.Context, adm *admissionControl, serverName, toolName string) error {
	rates := adm.registry.TokenRates()
	if rates == nil {
		return nil
	}
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || ac.Type != auth.AuthTypeAgent || ac.AgentName == "" {
		return nil
	}

	var override *limiter.RateSpec
	if ac.RateLimit != nil {
		override = &limiter.RateSpec{PerMinute: ac.RateLimit.PerMinute, Burst: ac.RateLimit.Burst}
	}
	// Names are unique per owner, not globally (server edition), so the owner
	// is part of the key.
	decision := rates.Allow(ac.UserID+"/"+ac.AgentName, override)
	if decision.Allowed {
		return nil
	}

	limitErr := &limiter.LimitError{
		Scope:      limiter.ScopeToken,
		Reason:     limiter.ReasonRateLimited,
		Limit:      decision.Spec.PerMinute,
		Burst:      decision.Spec.Burst,
		RetryAfter: decision.RetryAfter,
	}
	mc.reportRejection(ctx, adm, serverName, toolName, 0, limitErr)
	return limitErr
}

// reportRejection forwards a shed to the origin-independent observer. Only
// queue_full / queue_timeout / rate_limited are sheds; server_unavailable is
// the existing "server went away" semantics (FR-009) and is reported through
// the normal error path, not as a rejection.
func (mc *Client) reportRejection(ctx context.Context, adm *admissionControl, serverName, toolName string, waited time.Duration, err error) {
	if adm.observe == nil {
		return
	}
	var limitErr *limiter.LimitError
	if !errors.As(err, &limitErr) {
		return
	}
	if !limitErr.Shed() {
		return
	}
	rej := limiter.Rejection{
		Server:     serverName,
		Tool:       toolName,
		Scope:      limitErr.Scope,
		Reason:     limitErr.Reason,
		Limit:      limitErr.Limit,
		Burst:      limitErr.Burst,
		RetryAfter: limitErr.RetryAfter,
		Waited:     waited,
		Message:    limitErr.Error(),
	}
	if limitErr.Reason == limiter.ReasonRateLimited {
		if ac := auth.AuthContextFromContext(ctx); ac != nil {
			rej.Token = limiter.TokenIdentity{Name: ac.AgentName, Prefix: ac.TokenPrefix}
			if ac.IsClientCredential() {
				rej.Token.ClientID = ac.ClientID
			}
		}
	}
	adm.observe(ctx, rej)
}
