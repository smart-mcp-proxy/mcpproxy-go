package preflight

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// --- fake readers -----------------------------------------------------------
//
// The fakes exist to prove the evaluator needs nothing else: four in-memory
// maps are a complete world. If a future change makes the evaluator reach for
// an upstream client, a runtime or an index writer, these fakes stop compiling
// — which is the point.

type fakeIndex struct {
	tools      map[string][]IndexedTool
	toolsErr   error
	serversErr error
	// serverOrder makes IndexedServerNames deterministic.
	serverOrder []string
}

func (f *fakeIndex) ToolsByServer(serverName string) ([]IndexedTool, error) {
	if f.toolsErr != nil {
		return nil, f.toolsErr
	}
	return f.tools[serverName], nil
}

func (f *fakeIndex) IndexedServerNames() ([]string, error) {
	if f.serversErr != nil {
		return nil, f.serversErr
	}
	if f.serverOrder != nil {
		return f.serverOrder, nil
	}
	names := make([]string, 0, len(f.tools))
	for name := range f.tools {
		names = append(names, name)
	}
	return names, nil
}

type fakeApprovals struct {
	records map[string]*ApprovalState // key "server:tool"
	err     error
}

func (f *fakeApprovals) ToolApproval(serverName, toolName string) (*ApprovalState, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records[serverName+":"+toolName], nil
}

type fakeState struct {
	states map[string]ServerRuntime
}

func (f *fakeState) ServerRuntime(serverName string) (ServerRuntime, bool) {
	rt, ok := f.states[serverName]
	return rt, ok
}

type fakePolicy struct {
	servers    map[string]ServerPolicy
	denied     map[string]bool // key "server:tool"
	quarantine bool
	serverErr  error
	deniedErr  error
}

func (f *fakePolicy) ServerPolicy(serverName string) (ServerPolicy, error) {
	if f.serverErr != nil {
		return ServerPolicy{}, f.serverErr
	}
	return f.servers[serverName], nil
}

func (f *fakePolicy) ToolConfigDenied(serverName, toolName string) (bool, error) {
	if f.deniedErr != nil {
		return false, f.deniedErr
	}
	return f.denied[serverName+":"+toolName], nil
}

func (f *fakePolicy) QuarantineEnabled() bool { return f.quarantine }

var errBoom = errors.New("boom")

// --- world builder ----------------------------------------------------------

// world is a mutable fixture: start from healthyWorld() (one enabled, connected
// server with one indexed, approved tool) and sabotage exactly the axis under
// test. Every reason cell in the tests below is one mutation away from ready,
// which is what keeps the co-occurrence cases honest.
type world struct {
	index     *fakeIndex
	approvals *fakeApprovals
	state     *fakeState
	policy    *fakePolicy
	tier      Tier
	scope     *Scope
	filters   filterSet
	pins      map[string]string
}

// filterSet is an alias-free local mirror of the annotation filters so tests
// read declaratively.
type filterSet struct {
	readOnlyOnly       bool
	excludeDestructive bool
	excludeOpenWorld   bool
}

const (
	srv  = "gh"
	tool = "sync"
	id   = "gh:sync"
)

func boolPtr(b bool) *bool { return &b }

func healthyWorld() *world {
	return &world{
		index: &fakeIndex{
			serverOrder: []string{srv},
			tools: map[string][]IndexedTool{
				srv: {{
					Name: srv + ":" + tool,
					Annotations: &config.ToolAnnotations{
						ReadOnlyHint:    boolPtr(true),
						DestructiveHint: boolPtr(false),
						OpenWorldHint:   boolPtr(false),
					},
				}},
			},
		},
		approvals: &fakeApprovals{records: map[string]*ApprovalState{
			id: {Status: ApprovalStatusApproved, CurrentHash: "abc123", HashSchemaVersion: 2},
		}},
		state:  &fakeState{states: map[string]ServerRuntime{srv: {State: RuntimeStateReady}}},
		policy: &fakePolicy{servers: map[string]ServerPolicy{srv: {Found: true, Enabled: true}}, quarantine: true, denied: map[string]bool{}},
		tier:   TierOperator,
	}
}

func (w *world) ctx() EvalContext {
	ec := EvalContext{
		Index:     w.index,
		Approvals: w.approvals,
		Policy:    w.policy,
		Tier:      w.tier,
		Scope:     w.scope,
		Pins:      w.pins,
	}
	// Assigned conditionally so a nil *fakeState becomes a nil INTERFACE, the
	// shape the glue produces when no runtime is wired.
	if w.state != nil {
		ec.State = w.state
	}
	ec.Filters.ReadOnlyOnly = w.filters.readOnlyOnly
	ec.Filters.ExcludeDestructive = w.filters.excludeDestructive
	ec.Filters.ExcludeOpenWorld = w.filters.excludeOpenWorld
	return ec
}

// server mutators
func (w *world) unconfigure() *world { delete(w.policy.servers, srv); return w }
func (w *world) quarantine() *world {
	sp := w.policy.servers[srv]
	sp.Quarantined = true
	w.policy.servers[srv] = sp
	return w
}
func (w *world) disable() *world {
	sp := w.policy.servers[srv]
	sp.Enabled = false
	w.policy.servers[srv] = sp
	return w
}
func (w *world) autoApprove() *world {
	sp := w.policy.servers[srv]
	sp.AutoApproveToolChanges = true
	w.policy.servers[srv] = sp
	return w
}
func (w *world) outOfScope() *world {
	w.scope = NewScope("readonly", []string{"other"})
	return w
}
func (w *world) runtime(state ServerRuntimeState) *world {
	w.state.states[srv] = ServerRuntime{State: state}
	return w
}

// tool mutators
func (w *world) unindex() *world      { w.index.tools[srv] = nil; return w }
func (w *world) forget() *world       { delete(w.approvals.records, id); return w }
func (w *world) denyByConfig() *world { w.policy.denied[id] = true; return w }
func (w *world) approval(mut func(*ApprovalState)) *world {
	rec := w.approvals.records[id]
	if rec == nil {
		rec = &ApprovalState{}
		w.approvals.records[id] = rec
	}
	mut(rec)
	return w
}
func (w *world) annotations(a *config.ToolAnnotations) *world {
	tools := w.index.tools[srv]
	tools[0].Annotations = a
	w.index.tools[srv] = tools
	return w
}
