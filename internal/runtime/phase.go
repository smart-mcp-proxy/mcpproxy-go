package runtime

import "sync"

// Phase represents the lifecycle state of the core runtime.
type Phase string

// Core runtime phases.
const (
	PhaseInitializing Phase = "Initializing"
	PhaseLoading      Phase = "Loading"
	PhaseReady        Phase = "Ready"
	PhaseStarting     Phase = "Starting"
	PhaseRunning      Phase = "Running"
	PhaseStopping     Phase = "Stopping"
	PhaseStopped      Phase = "Stopped"
	PhaseError        Phase = "Error"
)

// allowedTransitions defines valid state transitions for the runtime.
var allowedTransitions = map[Phase]map[Phase]struct{}{
	PhaseInitializing: {
		PhaseInitializing: {},
		PhaseLoading:      {},
		PhaseReady:        {},
		PhaseStarting:     {},
		PhaseError:        {},
	},
	PhaseLoading: {
		PhaseLoading:  {},
		PhaseReady:    {},
		PhaseStarting: {},
		PhaseError:    {},
	},
	PhaseReady: {
		PhaseReady:    {},
		PhaseStarting: {},
		PhaseStopping: {},
		PhaseRunning:  {},
		PhaseError:    {},
	},
	PhaseStarting: {
		PhaseStarting: {},
		PhaseRunning:  {},
		PhaseStopping: {},
		PhaseError:    {},
	},
	PhaseRunning: {
		PhaseRunning:  {},
		PhaseStopping: {},
		PhaseError:    {},
	},
	PhaseStopping: {
		PhaseStopping: {},
		PhaseStopped:  {},
		PhaseError:    {},
	},
	PhaseStopped: {
		PhaseStopped:  {},
		PhaseStarting: {},
		PhaseReady:    {},
		PhaseError:    {},
	},
	PhaseError: {
		PhaseError:    {},
		PhaseStarting: {},
		PhaseStopping: {},
		PhaseReady:    {},
	},
}

type phaseMachine struct {
	mu      sync.RWMutex
	current Phase
}

func newPhaseMachine(initial Phase) *phaseMachine {
	return &phaseMachine{
		current: initial,
	}
}

// Transition attempts to move the machine to the requested phase, enforcing allowed transitions.
func (pm *phaseMachine) Transition(next Phase) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.current == next {
		return true
	}

	if allowed, ok := allowedTransitions[pm.current]; ok {
		if _, ok := allowed[next]; ok {
			pm.current = next
			return true
		}
	}

	return false
}

// Current returns the currently tracked phase.
func (pm *phaseMachine) Current() Phase {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.current
}

func (pm *phaseMachine) Set(next Phase) {
	pm.mu.Lock()
	pm.current = next
	pm.mu.Unlock()
}
