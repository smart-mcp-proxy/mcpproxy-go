package config

import (
	"fmt"
	"io"
	"os"
	"sync"
)

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

// loadWarningStderr is where emitLoadWarningVisible writes when no recorder is
// installed. A variable so tests can capture it.
var loadWarningStderr io.Writer = os.Stderr

// emitLoadWarningVisible is emitLoadWarning for a warning that must not be lost
// in a process that never installs a recorder (a standalone CLI save): with no
// recorder it also prints one line to stderr. The warning is still queued, so
// a core that installs its recorder later logs it too.
func emitLoadWarningVisible(message, key, value string) {
	loadWarnMu.RLock()
	noRecorder := loadWarnRecorder == nil
	loadWarnMu.RUnlock()
	if noRecorder {
		fmt.Fprintf(loadWarningStderr, "warning: %s (%s=%s)\n", message, key, value)
	}
	emitLoadWarning(message, key, value)
}
