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
	// Folded is the number of earlier transitions folded into this one
	// because the observer had stalled and the queue was full. A folded
	// record still starts where the first one started and ends where the
	// last one ended, so the observer sees every state the connection is
	// left in; what it loses is the intermediate states, and it is told how
	// many.
	Folded int
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
		sm.foldOldestLocked()
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

// foldOldestLocked makes room in a full queue by folding two adjacent
// transitions into one, never by dropping one: the folded record goes from
// the first one's start to the second one's end and keeps the later time and
// error. It prefers a pair whose middle state is not Ready, so that a loss and
// a recovery stay two records for as long as any other pair can be folded.
// Caller holds sm.mu.
func (sm *StateManager) foldOldestLocked() {
	q := sm.transitions
	if len(q) < 2 {
		return
	}
	at := 0
	for i := 0; i+1 < len(q); i++ {
		if q[i].To != StateReady {
			at = i
			break
		}
	}
	a, b := q[at], q[at+1]
	folded := Transition{
		From: a.From, To: b.To, At: b.At, Err: b.Err, TerminalCode: b.TerminalCode,
		Folded: a.Folded + b.Folded + 1,
	}
	q[at] = folded
	copy(q[at+1:], q[at+2:])
	sm.transitions = q[:len(q)-1]
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
