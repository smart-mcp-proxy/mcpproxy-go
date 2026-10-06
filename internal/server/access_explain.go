package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// The access explainer (Spec 108 FR-035, SC-009): for one subject and one
// upstream server:tool, the ordered chain of gates a real call would meet, the
// overall verdict and - per first failure - the fixes, in preference order.
//
// It RENDERS the one access chain (access_chain.go, which calls the same
// predicates dispatch calls); it re-derives no decision, so `allowed` cannot
// disagree with a real call (TestExplain_VerdictEqualsDispatchForEveryMatrixRow).
// Built-in management tools do not walk the upstream chain and are not explained.

// splitExplainTool splits "server:tool" at the first ':'.
func splitExplainTool(tool string) (server, name string, ok bool) {
	i := strings.Index(tool, ":")
	if i <= 0 || i == len(tool)-1 {
		return "", "", false
	}
	return tool[:i], tool[i+1:], true
}

// Explain implements runtime.ProfileEvaluator.
func (p *MCPProxyServer) Explain(_ context.Context, subject profile.AccessSubject, tool string) (*runtime.AccessExplanation, error) {
	serverName, toolName, ok := splitExplainTool(tool)
	if !ok {
		return nil, profile.ErrExplainBuiltinTool
	}
	ev, err := p.NewAccessEvaluator(subject)
	if err != nil {
		return nil, err
	}
	verdict := ev.Evaluate(serverName, toolName)
	_, catalogued := p.EffectiveAnnotations(serverName, toolName)
	if !catalogued && verdict.Callable {
		// A name the proxy has no tool for is refused by a real call
		// ("tool not found") before any gate; the chain models only catalogued
		// tools, so the explainer marks the approval step as the failure.
		verdict = withUncataloguedTool(verdict)
	}

	out := &runtime.AccessExplanation{
		Subject: runtime.ExplainSubjectView{Kind: subject.Kind},
		Tool:    tool,
		Profile: runtime.ExplainProfileView{Name: ev.res.Name, Source: ev.res.Source},
		Steps:   make([]runtime.ExplainStepView, 0, len(verdict.Steps)),
		Fixes:   []runtime.Fix{},
	}
	switch subject.Kind {
	case profile.AccessSubjectClient:
		out.Subject.Name = subject.ClientID
	case profile.AccessSubjectToken:
		out.Subject.Name = subject.TokenName
	case profile.AccessSubjectProfile:
		out.Subject.Name = subject.Profile
		out.Profile = runtime.ExplainProfileView{Name: subject.Profile, Source: string(profile.SourceURL)}
	}
	for _, s := range verdict.Steps {
		out.Steps = append(out.Steps, runtime.ExplainStepView{Step: s.Step, Status: s.Status, Detail: s.Detail})
	}

	switch {
	case verdict.Callable:
		out.Verdict = profile.ExplainVerdictAllowed
	case !verdict.Visible || !catalogued:
		// A tool that is not in the catalog is not listed to anyone: hidden.
		out.Verdict = profile.ExplainVerdictHidden
	default:
		out.Verdict = profile.ExplainVerdictBlocked
	}
	if verdict.Callable {
		return out, nil
	}
	out.FirstFailure = verdict.FirstFailure()
	out.Fixes = p.explainFixes(ev, subject, serverName, toolName, verdict, out.FirstFailure)
	return out, nil
}

// withUncataloguedTool turns an otherwise callable verdict for a tool the proxy
// does not know into a not-callable one whose failing step is tool_approval.
func withUncataloguedTool(v profile.AccessVerdict) profile.AccessVerdict {
	steps := make([]profile.AccessStep, len(v.Steps))
	copy(steps, v.Steps)
	for i := range steps {
		if steps[i].Step == profile.StepToolApproval {
			steps[i] = profile.AccessStep{Step: profile.StepToolApproval, Status: profile.AccessStepFail, Detail: "the server has no such tool"}
		}
	}
	v.Steps = steps
	v.Callable = false
	v.Visible = false
	v.Reason = profile.AccessReasonToolApproval
	return v
}

func titleOf(cfg *config.Config, name string) string {
	if cfg != nil {
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == name && cfg.Profiles[i].Title != "" {
				return cfg.Profiles[i].Title
			}
		}
	}
	return name
}

func clientDisplay(id string) string {
	if def := connect.FindClient(id); def != nil {
		return def.Name
	}
	return id
}

// explainFixes lists the remediations of the first failing step in preference
// order (FR-035; F24).
func (p *MCPProxyServer) explainFixes(ev *AccessEvaluator, subject profile.AccessSubject, server, tool string, v profile.AccessVerdict, step profile.ExplainStep) []runtime.Fix {
	fixes := []runtime.Fix{}
	add := func(action profile.FixAction, target, label string) {
		fixes = append(fixes, runtime.Fix{Step: step, Action: action, Target: target, Label: label})
	}
	// addMove is add for move_client, which also names the destination profile.
	addMove := func(client, dest, label string) {
		fixes = append(fixes, runtime.Fix{Step: step, Action: profile.FixMoveClient, Target: client, Profile: dest, Label: label})
	}
	toolID := server + ":" + tool
	profName := ev.res.Name
	if subject.Kind == profile.AccessSubjectProfile {
		profName = subject.Profile
	}
	cfg := ev.cfg
	profLabel := titleOf(cfg, profName)

	isClient := subject.Kind == profile.AccessSubjectClient
	// move_client: the first OTHER profile that would admit the tool, for a
	// client that holds a client credential (only then does a binding exist).
	moveClient := func() {
		if !isClient || cfg == nil {
			return
		}
		if subject.CredentialState != profile.CredentialStateClient && subject.CredentialState != "" {
			return
		}
		for i := range cfg.Profiles {
			other := cfg.Profiles[i].Name
			if other == profName {
				continue
			}
			alt := subject
			alt.Profile = other
			altEv, err := p.NewAccessEvaluator(alt)
			if err != nil {
				continue
			}
			if altEv.Evaluate(server, tool).Callable {
				addMove(subject.ClientID, other, fmt.Sprintf("Move %s to %s", clientDisplay(subject.ClientID), titleOf(cfg, other)))
				return
			}
		}
	}
	editToken := func() {
		if subject.Kind == profile.AccessSubjectToken {
			add(profile.FixEditToken, subject.TokenName, fmt.Sprintf("Edit token %s", subject.TokenName))
		}
	}

	switch step {
	case profile.StepCredential:
		switch subject.Kind {
		case profile.AccessSubjectClient:
			add(profile.FixReconnectClient, subject.ClientID, fmt.Sprintf("Reconnect %s", clientDisplay(subject.ClientID)))
		case profile.AccessSubjectToken:
			editToken()
		}
	case profile.StepProfile:
		switch {
		case ev.res.BindingGuarded:
			add(profile.FixChangeSetting, "require_mcp_auth", "Turn on require_mcp_auth")
			add(profile.FixChangeSetting, "anonymous_profile", "Set anonymous_profile to a profile not wider than the bound clients")
		case subject.Kind == profile.AccessSubjectClient:
			if cfg != nil && len(cfg.Profiles) > 0 && subject.CredentialState != profile.CredentialStateNone {
				target := cfg.Profiles[0].Name
				addMove(subject.ClientID, target, fmt.Sprintf("Move %s to %s", clientDisplay(subject.ClientID), titleOf(cfg, target)))
			} else {
				add(profile.FixChangeSetting, "anonymous_profile", "Set anonymous_profile")
			}
		case subject.Kind == profile.AccessSubjectToken:
			editToken()
		case subject.Kind == profile.AccessSubjectAnonymous:
			add(profile.FixChangeSetting, "anonymous_profile", "Set anonymous_profile")
		}
	case profile.StepServerInScope:
		if v.Reason == profile.AccessReasonServerNotInProfile && profName != "" {
			add(profile.FixAddServerToProfile, profName, fmt.Sprintf("Add %s to %s", server, profLabel))
			moveClient()
		} else {
			editToken()
		}
	case profile.StepToolRule, profile.StepTierCap:
		if profName == "" {
			break
		}
		if v.Reason == profile.AccessReasonUnannotatedHidden {
			add(profile.FixClassifyInProfile, profName, fmt.Sprintf("Classify %s in %s", toolID, profLabel))
		}
		add(profile.FixAllowInProfile, profName, fmt.Sprintf("Allow %s in %s", toolID, profLabel))
		moveClient()
	case profile.StepTokenPermission:
		editToken()
	case profile.StepServerState:
		detail := stepDetail(v, step)
		switch {
		case strings.Contains(detail, "disabled"):
			add(profile.FixEnableServer, server, fmt.Sprintf("Enable %s", server))
		case strings.Contains(detail, "quarantined"):
			add(profile.FixApproveTool, server, fmt.Sprintf("Approve server %s", server))
		}
	case profile.StepToolApproval:
		add(profile.FixApproveTool, toolID, fmt.Sprintf("Approve %s", toolID))
	}
	return fixes
}

func stepDetail(v profile.AccessVerdict, step profile.ExplainStep) string {
	for _, s := range v.Steps {
		if s.Step == step {
			return s.Detail
		}
	}
	return ""
}
