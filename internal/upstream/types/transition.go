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
	// FoldedLost, FoldedRestored and FoldedLogin count the changes that folding
	// hid inside this record: a move out of Ready, a move into Ready and a
	// move into PendingAuth that From and To no longer show. Folding avoids
	// hiding one while any other pair can be folded, and counts it when it
	// cannot, so an outage is never erased by a stalled observer.
	FoldedLost     int
	FoldedRestored int
	FoldedLogin    int
	// FoldedLoginLast says that, of the hidden recoveries and login
	// requirements, a login requirement came last: no recovery followed it,
	// so it is still open after everything the record hid.
	FoldedLoginLast bool
}

// lastLoginOrRecovery reports which came last among the record's hidden
// recoveries and login requirements and its own edge: 'L', 'R' or 0.
func (t Transition) lastLoginOrRecovery() byte {
	_, restored, login := t.edges()
	switch {
	case login > 0:
		return 'L'
	case restored > 0:
		return 'R'
	case t.FoldedLogin > 0 && (t.FoldedLoginLast || t.FoldedRestored == 0):
		return 'L'
	case t.FoldedRestored > 0:
		return 'R'
	}
	return 0
}

// edges reports which of the three recorded kinds of change the record shows
// by its own From and To.
func (t Transition) edges() (lost, restored, login int) {
	if t.From == StateReady && t.To != StateReady {
		lost = 1
	}
	if t.From != StateReady && t.To == StateReady {
		restored = 1
	}
	if t.From != StatePendingAuth && t.To == StatePendingAuth {
		login = 1
	}
	return lost, restored, login
}

// foldPair returns a and b as one record and whether folding hid a change.
func foldPair(a, b Transition) (Transition, bool) {
	f := Transition{
		From: a.From, To: b.To, At: b.At, Err: b.Err, TerminalCode: b.TerminalCode,
		Folded: a.Folded + b.Folded + 1,
	}
	al, ar, ag := a.edges()
	bl, br, bg := b.edges()
	fl, fr, fg := f.edges()
	hl, hr, hg := al+bl-fl, ar+br-fr, ag+bg-fg
	f.FoldedLost = a.FoldedLost + b.FoldedLost + hl
	f.FoldedRestored = a.FoldedRestored + b.FoldedRestored + hr
	f.FoldedLogin = a.FoldedLogin + b.FoldedLogin + hg
	last := b.lastLoginOrRecovery()
	if last == 0 {
		last = a.lastLoginOrRecovery()
	}
	f.FoldedLoginLast = last == 'L' && f.FoldedLogin > 0
	return f, hl+hr+hg > 0
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
// error. It takes the oldest pair whose folding hides no loss, recovery or
// login requirement; when every pair hides one, it folds the oldest pair and
// the record counts what it hid. Caller holds sm.mu.
func (sm *StateManager) foldOldestLocked() {
	q := sm.transitions
	if len(q) < 2 {
		return
	}
	at := 0
	folded, _ := foldPair(q[0], q[1])
	for i := 0; i+1 < len(q); i++ {
		if f, hides := foldPair(q[i], q[i+1]); !hides {
			at, folded = i, f
			break
		}
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
