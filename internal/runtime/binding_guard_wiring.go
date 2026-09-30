package runtime

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// SetBindingGuard installs the FR-008a evaluator (the server's
// *MCPProxyServer). Until it is installed, every guarded path uses the
// ConservativeBindingGuard, which is only ever stricter.
func (r *Runtime) SetBindingGuard(g BindingGuard) {
	r.bindingGuardMu.Lock()
	r.bindingGuard = g
	r.bindingGuardMu.Unlock()
}

// BindingGuard returns the installed evaluator, or the conservative one.
func (r *Runtime) BindingGuard() BindingGuard {
	r.bindingGuardMu.RLock()
	g := r.bindingGuard
	r.bindingGuardMu.RUnlock()
	if g == nil {
		return ConservativeBindingGuard{}
	}
	return g
}

// LockBindingWrites serializes every guarded write: the guard check and the
// write it protects run under this one mutex (plan D3), so two individually
// safe concurrent writes (a config write and a binding write) can never
// combine into a bypassable state. Callers must not hold it across another
// LockBindingWrites call.
func (r *Runtime) LockBindingWrites() (unlock func()) {
	r.bindingWriteMu.Lock()
	return r.bindingWriteMu.Unlock
}

// GuardedApplyConfig applies newCfg after the FR-008a guard: the delta over
// the whole candidate state (candidate config + the client credentials that
// exist now) must be empty, else *BindingGuardError is returned and nothing
// is written. It is the single funnel behind PATCH /config, POST
// /config/apply and PATCH /config/docker-isolation.
func (r *Runtime) GuardedApplyConfig(newCfg *config.Config, cfgPath string) (*ConfigApplyResult, error) {
	unlock := r.LockBindingWrites()
	defer unlock()

	tokens, err := r.clientCredentialSnapshot()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect client bindings: %w", err)
	}
	current := GuardState{Config: r.Config(), Tokens: tokens}
	candidate := GuardState{Config: newCfg, Tokens: tokens}
	if err := CheckBindingGuard(r.BindingGuard(), current, candidate); err != nil {
		return nil, err
	}
	return r.ApplyConfig(newCfg, cfgPath)
}

// ClientsService returns the runtime's single client-credential service
// (Spec 108, FR-026).
func (r *Runtime) ClientsService() *ClientsService { return r.clientsService }

// clientCredentialSnapshot returns the stored client credential records
// (kind=client). A store error is returned, never swallowed: the guard fails
// closed.
func (r *Runtime) clientCredentialSnapshot() ([]auth.AgentToken, error) {
	sm := r.StorageManager()
	if sm == nil {
		return nil, nil
	}
	all, err := sm.ListAgentTokens()
	if err != nil {
		return nil, err
	}
	out := make([]auth.AgentToken, 0, len(all))
	for i := range all {
		if all[i].Kind == auth.KindClient {
			out = append(out, all[i])
		}
	}
	return out, nil
}
