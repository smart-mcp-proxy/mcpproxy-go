package telemetry

import "testing"

// Pins the contract docs/features/telemetry.md relies on: a stored
// first_real_tool_call_ever=true is loaded unchanged and survives a later
// Save that carries false, so an install activated on an older build keeps
// reporting true after upgrading. Version filtering alone therefore cannot
// separate classified-success activation from earlier transport-success flags.
func TestActivationStore_RealToolCallFlagSurvivesUpgradeReload(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	var s bboltActivationStore
	if err := s.Save(db, ActivationState{FirstRealToolCallEver: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !st.FirstRealToolCallEver {
		t.Fatal("stored flag not loaded")
	}
	// A fresh process that made no call saves a zero state.
	if err := s.Save(db, ActivationState{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err = s.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !st.FirstRealToolCallEver {
		t.Fatal("first_real_tool_call_ever flipped true->false across restart")
	}
}
