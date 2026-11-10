package config

import "sync"

// LoadWarningRecorder receives non-fatal messages from config loading.
type LoadWarningRecorder func(message string, key string, value string)

// LoadWarning is a deferred config load warning.
type LoadWarning struct {
	Message string
	Key     string
	Value   string
}

type pendingWarning struct {
	message, key, value string
}

var (
	loadWarnMu          sync.RWMutex
	loadWarnRecorder    LoadWarningRecorder
	pendingLoadWarnings []pendingWarning
)

// SetLoadWarningRecorder installs a callback for config load warnings. When unset,
// warnings are discarded (CLI paths may log separately).
func SetLoadWarningRecorder(fn LoadWarningRecorder) {
	loadWarnMu.Lock()
	loadWarnRecorder = fn
	loadWarnMu.Unlock()
}

func emitLoadWarning(message, key, value string) {
	loadWarnMu.Lock()
	defer loadWarnMu.Unlock()
	if loadWarnRecorder != nil {
		loadWarnRecorder(message, key, value)
		return
	}
	pendingLoadWarnings = append(pendingLoadWarnings, pendingWarning{message, key, value})
}

// DrainLoadWarnings returns warnings emitted before a recorder was installed.
func DrainLoadWarnings() []LoadWarning {
	loadWarnMu.Lock()
	defer loadWarnMu.Unlock()
	out := make([]LoadWarning, len(pendingLoadWarnings))
	for i, w := range pendingLoadWarnings {
		out[i] = LoadWarning{Message: w.message, Key: w.key, Value: w.value}
	}
	pendingLoadWarnings = nil
	return out
}
