package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestUpdateOnboardingState_ConcurrentWritesSurvive is T029's storage half:
// a concurrent POST /onboarding/mark write (Engaged=true) and several
// connect-success writes (ClientConnectedAt[id]=now) must ALL survive under
// -race, because they all go through UpdateOnboardingState's single
// read-modify-write transaction instead of the separate Get+Save pair the
// mark handler used before Spec 109-b (T035): two independent
// Get-then-Save calls are two separate bbolt transactions, so whichever
// finishes its Save last silently clobbers the other's change with its own
// stale snapshot.
func TestUpdateOnboardingState_ConcurrentWritesSurvive(t *testing.T) {
	db := newTestDB(t)

	const clients = 8
	var wg sync.WaitGroup

	// The "mark" writer: sets Engaged, exactly like handleMarkOnboardingState.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := db.UpdateOnboardingState(func(s *OnboardingState) error {
			s.Engaged = true
			return nil
		})
		if err != nil {
			t.Errorf("mark UpdateOnboardingState: %v", err)
		}
	}()

	// The "connect success" writers: each records one client's connect time,
	// exactly like the connect handler's onboarding hook.
	for i := 0; i < clients; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			clientID := fmt.Sprintf("client-%d", i)
			err := db.UpdateOnboardingState(func(s *OnboardingState) error {
				if s.ClientConnectedAt == nil {
					s.ClientConnectedAt = map[string]time.Time{}
				}
				s.ClientConnectedAt[clientID] = time.Now()
				return nil
			})
			if err != nil {
				t.Errorf("connect UpdateOnboardingState(%s): %v", clientID, err)
			}
		}()
	}

	wg.Wait()

	final, err := db.GetOnboardingState()
	if err != nil {
		t.Fatalf("GetOnboardingState: %v", err)
	}
	if !final.Engaged {
		t.Error("Engaged was dropped by a concurrent connect-success write")
	}
	if len(final.ClientConnectedAt) != clients {
		t.Errorf("ClientConnectedAt has %d entries, want %d (a concurrent write dropped one)",
			len(final.ClientConnectedAt), clients)
	}
	for i := 0; i < clients; i++ {
		clientID := fmt.Sprintf("client-%d", i)
		if _, ok := final.ClientConnectedAt[clientID]; !ok {
			t.Errorf("ClientConnectedAt missing entry for %s", clientID)
		}
	}
}

// TestUpdateOnboardingState_SeparateGetSavePairCanDropAWrite is the negative
// control proving the race T029 guards against is real: two Get+Save pairs
// run back-to-back (deterministically, not concurrently, to keep the
// assertion non-flaky) reproduce the exact clobber UpdateOnboardingState
// closes.
func TestUpdateOnboardingState_SeparateGetSavePairCanDropAWrite(t *testing.T) {
	db := newTestDB(t)

	// Both readers see the same starting snapshot...
	stateA, err := db.GetOnboardingState()
	if err != nil {
		t.Fatalf("GetOnboardingState (A): %v", err)
	}
	stateB, err := db.GetOnboardingState()
	if err != nil {
		t.Fatalf("GetOnboardingState (B): %v", err)
	}

	// ...A sets Engaged and saves...
	stateA.Engaged = true
	if err := db.SaveOnboardingState(stateA); err != nil {
		t.Fatalf("SaveOnboardingState (A): %v", err)
	}

	// ...then B, which never saw A's change, sets its own field and saves,
	// overwriting A's write with its own stale copy of Engaged=false.
	stateB.ClientConnectedAt = map[string]time.Time{"cursor": time.Now()}
	if err := db.SaveOnboardingState(stateB); err != nil {
		t.Fatalf("SaveOnboardingState (B): %v", err)
	}

	final, err := db.GetOnboardingState()
	if err != nil {
		t.Fatalf("GetOnboardingState (final): %v", err)
	}
	if final.Engaged {
		t.Fatal("expected the separate Get+Save pair to drop A's Engaged write — " +
			"if this now passes, SaveOnboardingState changed semantics and this " +
			"documentation test should be removed")
	}
}
