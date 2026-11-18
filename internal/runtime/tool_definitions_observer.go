package runtime

import "sync"

// toolDefinitionsObserverState is embedded in Runtime.
type toolDefinitionsObserverState struct {
	toolDefsObserversMu sync.RWMutex
	toolDefsObservers   []func(serverName string)

	// tokenSavedObservers run (each on its own goroutine) after an upstream
	// OAuth token is saved, whichever path refreshed it (GH #1929 release QA:
	// the network watcher's direct refresh emits no oauth.token_refreshed).
	tokenSavedObserversMu sync.RWMutex
	tokenSavedObservers   []func(serverName string)
}

// AddToolDefinitionsChangedObserver registers fn to run after a discovery pass
// saw a server's tool inventory move after its baseline: a tool's definition
// changed (or a held one changed again), or a tool was added. Spec 118 uses it
// to arm the debounced informational rescan that brings the review screen's
// scan coverage back to "current".
//
// fn runs on the discovery goroutine while the server's tool-approval lock is
// held, so it must return quickly and must not call back into the runtime's
// approval paths (record the name and schedule the work).
func (r *Runtime) AddToolDefinitionsChangedObserver(fn func(serverName string)) {
	if fn == nil {
		return
	}
	r.toolDefsObserversMu.Lock()
	r.toolDefsObservers = append(r.toolDefsObservers, fn)
	r.toolDefsObserversMu.Unlock()
}

func (r *Runtime) notifyToolDefinitionsChanged(serverName string) {
	r.toolDefsObserversMu.RLock()
	observers := append([]func(string){}, r.toolDefsObservers...)
	r.toolDefsObserversMu.RUnlock()
	for _, fn := range observers {
		fn(serverName)
	}
}

// ToolDefinitionsObserverCount reports how many observers are registered. It
// exists so the server package can pin that its rescan subscribes at
// construction (the observer set is otherwise unexported).
func (r *Runtime) ToolDefinitionsObserverCount() int {
	r.toolDefsObserversMu.RLock()
	defer r.toolDefsObserversMu.RUnlock()
	return len(r.toolDefsObservers)
}

// AddTokenSavedObserver registers fn to run after an upstream OAuth token is
// saved: a sign-in, a proactive or reactive refresh, or the network watcher's
// direct refresh. GH #1929 release QA: the direct surface republishes a server
// that could not list its tools while its refresh was failing. fn runs on its
// own goroutine, because the save path may hold the client's locks.
func (r *Runtime) AddTokenSavedObserver(fn func(serverName string)) {
	if fn == nil {
		return
	}
	r.tokenSavedObserversMu.Lock()
	r.tokenSavedObservers = append(r.tokenSavedObservers, fn)
	r.tokenSavedObserversMu.Unlock()
}

func (r *Runtime) notifyTokenSaved(serverName string) {
	r.tokenSavedObserversMu.RLock()
	observers := append([]func(string){}, r.tokenSavedObservers...)
	r.tokenSavedObserversMu.RUnlock()
	for _, fn := range observers {
		go fn(serverName)
	}
}
