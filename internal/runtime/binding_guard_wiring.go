package runtime

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
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
