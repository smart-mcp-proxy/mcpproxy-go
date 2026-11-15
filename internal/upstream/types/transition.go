package types

import "time"

// Spec 124 D5: an ordered record of state changes.
//
// The state-change callback cannot tell an observer WHEN a change happened or
// in which ORDER two changes happened: ConnectionInfo carries no transition
// time, and the callback is dispatched synchronously on some paths and in a
// new goroutine on the error paths, so a loss can be delivered after the
// recovery that followed it. An activity log that must place "connection
// lost" between the calls before and after it needs both.
//
// So each mutation records a Transition while the state lock is held, with
// the time taken at that moment, and a single drain delivers them one at a
// time in the order they were recorded. The existing callback and its
// dispatch are not changed.

// Transition is one state change, captured when it happened.
type Transition struct {
	From ConnectionState
	To   ConnectionState
	// At is the time of the change, taken under the state lock.
	At time.Time
	// Err is the connection's last error at that moment (nil on success).
	Err error
	// TerminalCode is the diagnostics code of a terminal failure, when set.
	TerminalCode string
}

// TransitionObserver receives transitions in the order they happened, one at
// a time. It is called without the state lock and must not block for long:
// the next transition of the same connection waits behind it.
type TransitionObserver func(Transition)

// maxQueuedTransitions bounds the queue of undelivered transitions. A healthy
// observer keeps it near empty; the bound only matters if the observer stalls.
const maxQueuedTransitions = 256

// SetTransitionObserver installs the observer. Transitions recorded before an
// observer is installed are not kept.
func (sm *StateManager) SetTransitionObserver(observer TransitionObserver) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.transitionObserver = observer
	if observer == nil {
		sm.transitions = nil
	}
}

// captureLocked records the change from oldState to the current state. Caller
// holds sm.mu.
func (sm *StateManager) captureLocked(oldState ConnectionState) {
	if sm.transitionObserver == nil {
		return
	}
	if len(sm.transitions) >= maxQueuedTransitions {
		// The observer has stalled. Drop the OLDEST undelivered transition,
		// never the newest: the latest state (a final Ready) must arrive.
		copy(sm.transitions, sm.transitions[1:])
		sm.transitions = sm.transitions[:len(sm.transitions)-1]
	}
	sm.transitions = append(sm.transitions, Transition{
		From:         oldState,
		To:           sm.currentState,
		At:           time.Now(),
		Err:          sm.lastError,
		TerminalCode: sm.terminalCode,
	})
	if !sm.draining {
		sm.draining = true
		go sm.drainTransitions()
	}
}

// drainTransitions delivers queued transitions in order. At most one drain
// runs per state manager, which is what keeps the delivery ordered; it exits
// when the queue is empty, so an idle connection holds no goroutine.
func (sm *StateManager) drainTransitions() {
	for {
		sm.mu.Lock()
		if len(sm.transitions) == 0 || sm.transitionObserver == nil {
			sm.transitions = nil
			sm.draining = false
			sm.mu.Unlock()
			return
		}
		t := sm.transitions[0]
		sm.transitions = sm.transitions[1:]
		observer := sm.transitionObserver
		sm.mu.Unlock()

		observer(t)
	}
}
