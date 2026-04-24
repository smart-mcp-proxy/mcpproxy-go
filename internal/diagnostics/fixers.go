package diagnostics

import (
	"context"
	"errors"
	"sync"
)

// FixerFunc is the signature every registered fixer implements.
type FixerFunc func(ctx context.Context, req FixRequest) (FixResult, error)

var (
	fixersMu sync.RWMutex
	fixers   = map[string]FixerFunc{}
)

// Register installs a fixer keyed by its FixStep.FixerKey. Subsequent calls
// with the same key overwrite the previous registration (useful for tests).
func Register(key string, f FixerFunc) {
	fixersMu.Lock()
	defer fixersMu.Unlock()
	fixers[key] = f
}

// ErrUnknownFixer is returned when InvokeFixer cannot find a registered fixer.
var ErrUnknownFixer = errors.New("diagnostics: unknown fixer key")

// InvokeFixer runs the registered fixer with the given key. Returns
// ErrUnknownFixer if no fixer is registered. Respects req.Mode — the fixer is
// responsible for not mutating state when req.Mode == ModeDryRun.
func InvokeFixer(ctx context.Context, key string, req FixRequest) (FixResult, error) {
	fixersMu.RLock()
	f, ok := fixers[key]
	fixersMu.RUnlock()
	if !ok {
		return FixResult{Outcome: OutcomeBlocked, FailureMsg: "unknown fixer key"}, ErrUnknownFixer
	}
	return f(ctx, req)
}

// FixerKeys returns a sorted slice of all registered fixer keys (for tests
// and `doctor list-codes`).
func FixerKeys() []string {
	fixersMu.RLock()
	defer fixersMu.RUnlock()
	out := make([]string, 0, len(fixers))
	for k := range fixers {
		out = append(out, k)
	}
	return out
}
