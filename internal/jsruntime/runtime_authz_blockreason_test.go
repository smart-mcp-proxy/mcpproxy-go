package jsruntime

import (
	"context"
	"testing"
)

// fakeProfileGate stands in for the host's per-call gate capture. refusal is
// the disclosed profile-refusal text; reason is the typed block reason.
type fakeProfileGate struct {
	refusal string
	reason  string
}

func (g *fakeProfileGate) ProfilePolicyRefusal() string     { return g.refusal }
func (g *fakeProfileGate) ProfilePolicyBlockReason() string { return g.reason }

// refusalOnlyGate implements only ProfilePolicyRefusal: the refusal must never
// depend on the optional block-reason method (Spec 108 FR-029, T166).
type refusalOnlyGate struct{ refusal string }

func (g *refusalOnlyGate) ProfilePolicyRefusal() string { return g.refusal }

// TestAuthzObserver_ProfileRefusalReportsBlockReason proves a profile policy
// refusal reaches the observer with the host's typed block reason, that the
// refusal itself does not depend on the optional method, and that a
// non-profile refusal carries no reason.
func TestAuthzObserver_ProfileRefusalReportsBlockReason(t *testing.T) {
	const message = "blocked by profile: s:t is above the cap"
	lookup := func(gate ToolGate) ToolGateLookup {
		return func(serverName, toolName string) (string, ToolGate) { return "write", gate }
	}

	cases := []struct {
		name       string
		opts       ExecutionOptions
		wantCode   ErrorCode
		wantReason string
	}{
		{
			name:       "profile refusal with typed reason",
			opts:       ExecutionOptions{ToolGateFunc: lookup(&fakeProfileGate{refusal: message, reason: "profile_tier"})},
			wantCode:   ErrorCodeAccessDenied,
			wantReason: "profile_tier",
		},
		{
			name:       "refusal-only gate still refuses with empty reason",
			opts:       ExecutionOptions{ToolGateFunc: lookup(&refusalOnlyGate{refusal: message})},
			wantCode:   ErrorCodeAccessDenied,
			wantReason: "",
		},
		{
			name:       "non-profile refusal carries no reason",
			opts:       ExecutionOptions{AllowedServers: []string{"other"}},
			wantCode:   ErrorCodeServerNotAllowed,
			wantReason: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller := newMockToolCaller()
			observer := &recordingAuthzObserver{}
			tc.opts.AuthzObserver = observer
			result := Execute(context.Background(), caller, `call_tool("s", "t", {})`, tc.opts)
			if !result.Ok {
				t.Fatalf("execution failed: %+v", result.Error)
			}
			if len(caller.calls) != 0 {
				t.Fatalf("a refused call must never dispatch upstream, got %d", len(caller.calls))
			}
			reports := observer.snapshot()
			if len(reports) != 1 {
				t.Fatalf("exactly one authz report expected, got %d", len(reports))
			}
			if reports[0].Code != tc.wantCode {
				t.Fatalf("Code = %q, want %q", reports[0].Code, tc.wantCode)
			}
			if reports[0].BlockReason != tc.wantReason {
				t.Fatalf("BlockReason = %q, want %q", reports[0].BlockReason, tc.wantReason)
			}
		})
	}
}
