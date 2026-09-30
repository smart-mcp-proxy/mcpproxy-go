package server

import (
	"context"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// The access chain (Spec 108 FR-032, FR-035): for one subject and one
// (server, tool), the ordered verdict of every gate a real call would meet.
// It is the ONE evaluator behind GET /tools?client= / ?profile= (this PR), the
// MCP effective_tools operation (108-h) and the access explainer (108-f).
//
// It lives in this package, not internal/runtime, because every predicate it
// consults (the profile resolver, the shared per-tool gate, the target-tier
// derivation) is unexported here. Each step calls the REAL predicate the
// dispatch path calls; none of the logic is copied (FR-035, R2), and
// TestEvaluateAccess_CallableEqualsDispatchOutcome runs the real dispatch for
// every row.

// AccessEvaluator evaluates the access chain for one subject. It resolves the
// subject once (its credential, binding and profile index snapshot) and then
// answers per (server, tool). It is not safe to keep across a config change.
type AccessEvaluator struct {
	p       *MCPProxyServer
	subject profile.AccessSubject

	// credentialFail is set when the subject's connection cannot authenticate
	// at all (revoked or expired credential, no credential under
	// require_mcp_auth, or no readable record); every step then reports the
	// credential reason and the rest are skipped.
	credentialFail   bool
	credentialDetail string
	skipCredential   bool

	res        ProfileResolution
	idx        *profileIndex
	authCtx    *auth.AuthContext // the request identity after ScopedView; nil = administrator
	scopedView bool
	dangling   bool
	cfg        *config.Config
}

// EvaluateAccess is the one-shot form of NewAccessEvaluator(...).Evaluate.
// A subject that cannot be resolved (unknown profile) yields a not-visible,
// not-callable verdict whose only failing step is profile.
func (p *MCPProxyServer) EvaluateAccess(subject profile.AccessSubject, server, tool string) profile.AccessVerdict {
	ev, err := p.NewAccessEvaluator(subject)
	if err != nil {
		return failedAccessVerdict(profile.StepProfile, err.Error())
	}
	return ev.Evaluate(server, tool)
}

func failedAccessVerdict(step profile.ExplainStep, detail string) profile.AccessVerdict {
	steps := make([]profile.AccessStep, 0, len(profile.StepOrder()))
	failed := false
	for _, s := range profile.StepOrder() {
		switch {
		case s == step:
			steps = append(steps, profile.AccessStep{Step: s, Status: profile.AccessStepFail, Detail: detail})
			failed = true
		case failed:
			steps = append(steps, profile.AccessStep{Step: s, Status: profile.AccessStepSkip})
		default:
			steps = append(steps, profile.AccessStep{Step: s, Status: profile.AccessStepPass})
		}
	}
	return profile.AccessVerdict{Reason: profile.AccessReasonFromStep(step), Steps: steps}
}

// NewAccessEvaluator resolves the subject.
//
//   - profile: the profile's own reach, as a /mcp/p/<slug> request resolves it.
//     The credential and token_permission steps are skipped.
//   - client, credential state client: the credential record's binding through
//     the real resolver (locked -> source pin, switchable -> source binding).
//   - client, admin_key: evaluated as administrator (the client holds the admin
//     key; no profile applies).
//   - client, none: evaluated as the anonymous caller its keyless connection
//     would be: refused outright under require_mcp_auth, otherwise resolved
//     through anonymous_profile and the FR-008a binding guard.
//   - client, revoked / expired (or an unreadable record): the credential
//     step fails for every row.
func (p *MCPProxyServer) NewAccessEvaluator(subject profile.AccessSubject) (*AccessEvaluator, error) {
	cfg := p.currentConfig()
	base := auth.WithAuthContext(context.Background(), auth.AdminContext())
	idx := p.profileIndexCurrent(base)
	ev := &AccessEvaluator{p: p, subject: subject, idx: idx, cfg: cfg}

	switch subject.Kind {
	case profile.AccessSubjectProfile:
		scope := profileScopeFromIndex(idx, subject.Profile)
		if scope == nil {
			return nil, profile.ErrUnknownProfile
		}
		ev.skipCredential = true
		ctx := profile.WithProfileScope(base, scope)
		ev.res = p.resolveProfileV3(ctx, idx)
		ev.authCtx = nil // profile reach is independent of any token
		ev.markDangling()
		return ev, nil

	case profile.AccessSubjectClient:
		record, err := p.clientCredentialRecord(subject.ClientID)
		if err != nil {
			return nil, err
		}
		if record == nil && connect.FindClient(subject.ClientID) == nil {
			return nil, profile.ErrUnknownClient
		}
		state := subject.CredentialState
		if state == "" || state == profile.CredentialStateClient || state == profile.CredentialStateUnknown {
			state = credentialStateOf(record, time.Now())
		}
		switch state {
		case profile.CredentialStateClient:
			if record == nil {
				ev.failCredential("no active client credential")
				return ev, nil
			}
			ac := record.AuthContext()
			ev.res = p.resolveProfileV3(auth.WithAuthContext(context.Background(), ac), idx)
			ev.authCtx = ac
		case profile.CredentialStateAdminKey:
			ev.res = p.resolveProfileV3(base, idx)
			ev.authCtx = nil
		case profile.CredentialStateNone:
			if cfg != nil && cfg.RequireMCPAuth {
				ev.failCredential("the client holds no credential and require_mcp_auth is on")
				return ev, nil
			}
			ac := auth.AnonymousContext()
			ctx := auth.WithAuthContext(context.Background(), ac)
			ev.res = p.resolveProfileV3(ctx, idx)
			ev.authCtx = auth.ScopedView(ac, ev.res.anonymousConfinementActive())
		default: // revoked, expired
			ev.failCredential(fmt.Sprintf("the client's credential is %s", state))
			return ev, nil
		}
		ev.markDangling()
		return ev, nil
	}
	return nil, fmt.Errorf("unknown access subject kind %q", subject.Kind)
}

func (ev *AccessEvaluator) failCredential(detail string) {
	ev.credentialFail = true
	ev.credentialDetail = detail
}

// markDangling records the FR-020 "profile" step failures: a base that no
// longer resolves, or the FR-008a anonymous binding guard.
func (ev *AccessEvaluator) markDangling() {
	ev.dangling = ev.res.BindingGuarded ||
		(ev.res.Base != "" && ev.idx != nil && ev.idx.position(ev.res.Base) < 0)
}

// clientCredentialRecord returns the kind=client credential record of a
// client, nil when it has none.
func (p *MCPProxyServer) clientCredentialRecord(clientID string) (*auth.AgentToken, error) {
	if p.storage == nil || clientID == "" {
		return nil, nil
	}
	all, err := p.storage.ListAgentTokens()
	if err != nil {
		return nil, fmt.Errorf("cannot read client credentials: %w", err)
	}
	name := auth.ClientTokenName(clientID)
	for i := range all {
		if all[i].Name == name && all[i].Kind == auth.KindClient && all[i].UserID == "" {
			rec := all[i]
			return &rec, nil
		}
	}
	return nil, nil
}

func credentialStateOf(t *auth.AgentToken, now time.Time) profile.CredentialState {
	switch {
	case t == nil:
		return profile.CredentialStateNone
	case t.Revoked:
		return profile.CredentialStateRevoked
	case !t.ExpiresAt.After(now):
		return profile.CredentialStateExpired
	default:
		return profile.CredentialStateClient
	}
}

// globalGateBlocksUpstream is the FR-032 global_gate step for an upstream tool
// of the given tier. On main, read_only_mode and disable_management gate only
// the MANAGEMENT operations (upstream_servers mutations); they do not block an
// upstream tool call, so the step passes for every upstream row. If a future
// dispatch gate is added it must be added here and in the dispatch path
// together: callable has to equal the real outcome (SC-009).
func globalGateBlocksUpstream(_ *config.Config, _ profile.Tier) (blocked bool, detail string) {
	return false, ""
}

// Evaluate walks the chain for one (server, tool).
func (ev *AccessEvaluator) Evaluate(server, tool string) profile.AccessVerdict {
	p := ev.p
	annotations, found := p.EffectiveAnnotations(server, tool)
	intrinsic := profile.IntrinsicTier(annotations, found)
	verdict := profile.AccessVerdict{ProfileTier: intrinsic}
	steps := make([]profile.AccessStep, 0, len(profile.StepOrder()))
	firstFail := profile.AccessReasonNone

	record := func(step profile.ExplainStep, status profile.AccessStepStatus, reason profile.AccessReason, detail string) {
		steps = append(steps, profile.AccessStep{Step: step, Status: status, Detail: detail})
		if status == profile.AccessStepFail && firstFail == profile.AccessReasonNone {
			firstFail = reason
		}
	}
	finish := func() profile.AccessVerdict {
		verdict.Steps = steps
		verdict.Reason = firstFail
		verdict.Callable = firstFail == profile.AccessReasonNone
		visible := true
		for _, s := range steps {
			switch s.Step {
			case profile.StepCredential, profile.StepProfile, profile.StepServerInScope, profile.StepToolRule, profile.StepTierCap:
				if s.Status == profile.AccessStepFail {
					visible = false
				}
			}
		}
		verdict.Visible = visible
		return verdict
	}

	// 1. credential
	switch {
	case ev.skipCredential:
		record(profile.StepCredential, profile.AccessStepSkip, "", "a profile subject holds no credential")
	case ev.credentialFail:
		record(profile.StepCredential, profile.AccessStepFail, profile.AccessReasonCredential, ev.credentialDetail)
		for _, s := range profile.StepOrder()[1:] {
			record(s, profile.AccessStepSkip, "", "")
		}
		return finish()
	default:
		record(profile.StepCredential, profile.AccessStepPass, "", "")
	}

	// 2. profile: a base that no longer resolves (or the anonymous binding
	// guard) is authoritative deny-all, never a fall-through (FR-020).
	if ev.dangling {
		record(profile.StepProfile, profile.AccessStepFail, profile.AccessReasonProfile, "the profile binding does not resolve")
	} else {
		record(profile.StepProfile, profile.AccessStepPass, "", "")
	}

	// 3. server_in_scope: the profile's servers first (server_not_in_profile),
	// then the token's servers.
	switch {
	case ev.res.Scope != nil && !ev.res.Scope.Allows(server):
		record(profile.StepServerInScope, profile.AccessStepFail, profile.AccessReasonServerNotInProfile, "the server is not in the profile")
	case ev.authCtx != nil && !ev.authCtx.IsAdmin() && !ev.authCtx.CanAccessServer(server):
		record(profile.StepServerInScope, profile.AccessStepFail, profile.AccessReasonServerInScope, "the server is outside the credential's servers")
	default:
		record(profile.StepServerInScope, profile.AccessStepPass, "", "")
	}

	// 4. tool_rule and 5. tier_cap: the compiled policy's FR-010 decision.
	ruleStatus, ruleReason, ruleDetail := profile.AccessStepPass, profile.AccessReasonNone, ""
	tierStatus, tierReason, tierDetail := profile.AccessStepPass, profile.AccessReasonNone, ""
	if policy := ev.res.Policy; policy != nil {
		admitted, reason, tier := policy.Decide(server, tool, intrinsic)
		verdict.ProfileTier = tier
		if !admitted {
			switch reason {
			case profile.ReasonDeniedByRule:
				ruleStatus, ruleReason, ruleDetail = profile.AccessStepFail, profile.AccessReasonDeniedByRule, "a deny rule matches"
				tierStatus = profile.AccessStepSkip
			case profile.ReasonUnannotatedHidden, profile.ReasonAboveTierCap:
				tierStatus, tierReason = profile.AccessStepFail, profile.AccessReasonFromDecision(reason)
				tierDetail = string(reason)
			}
			// server_not_in_profile is reported by step 3 above.
		}
	}
	record(profile.StepToolRule, ruleStatus, ruleReason, ruleDetail)
	record(profile.StepTierCap, tierStatus, tierReason, tierDetail)

	// 6. token_permission: the target tool's tier against the credential's
	// permissions, exactly as dispatch authorizes a scoped caller
	// (handleCallToolVariant: tierForAnnotations + HasPermission).
	switch {
	case ev.skipCredential:
		record(profile.StepTokenPermission, profile.AccessStepSkip, "", "a profile subject holds no token")
	case ev.authCtx != nil && !ev.authCtx.IsAdmin():
		perm := tierForAnnotations(annotations, found)
		if perm != "" && !ev.authCtx.HasPermission(perm) {
			record(profile.StepTokenPermission, profile.AccessStepFail, profile.AccessReasonTokenPermission, "the credential lacks '"+perm+"' permission")
		} else {
			record(profile.StepTokenPermission, profile.AccessStepPass, "", "")
		}
	default:
		record(profile.StepTokenPermission, profile.AccessStepPass, "", "")
	}

	// 7. global_gate
	if blocked, detail := globalGateBlocksUpstream(ev.cfg, verdict.ProfileTier); blocked {
		record(profile.StepGlobalGate, profile.AccessStepFail, profile.AccessReasonGlobalGate, detail)
	} else {
		record(profile.StepGlobalGate, profile.AccessStepPass, "", "")
	}

	// 8. server_state and 9. tool_approval: the shared per-tool gate every
	// dispatch path consults.
	gate := p.evaluateExactToolGate(server, tool)
	stateStatus, stateDetail := profile.AccessStepPass, ""
	approvalStatus, approvalDetail := profile.AccessStepPass, ""
	switch gate.class {
	case preflight.ToolClassServerNotConfigured:
		stateStatus, stateDetail = profile.AccessStepFail, "the server is not configured"
	case preflight.ToolClassServerQuarantined:
		stateStatus, stateDetail = profile.AccessStepFail, "the server is quarantined"
	case preflight.ToolClassServerDisabled:
		stateStatus, stateDetail = profile.AccessStepFail, "the server is disabled"
	case preflight.ToolClassPendingApproval, preflight.ToolClassChanged:
		approvalStatus, approvalDetail = profile.AccessStepFail, "the tool is awaiting approval ("+string(gate.class)+")"
	case preflight.ToolClassBlockedByUser, preflight.ToolClassDeniedByConfig:
		approvalStatus, approvalDetail = profile.AccessStepFail, "the tool is disabled ("+string(gate.class)+")"
	}
	if gate.storageErr != nil && approvalStatus == profile.AccessStepPass && stateStatus == profile.AccessStepPass {
		approvalStatus, approvalDetail = profile.AccessStepFail, "the tool's approval state could not be read"
	}
	if stateStatus == profile.AccessStepPass {
		if client, exists := p.upstreamManager.GetClient(server); !exists || !client.IsConnected() {
			stateStatus, stateDetail = profile.AccessStepFail, "the server is not connected"
		}
	}
	record(profile.StepServerState, stateStatus, profile.AccessReasonServerState, stateDetail)
	record(profile.StepToolApproval, approvalStatus, profile.AccessReasonToolApproval, approvalDetail)

	return finish()
}
