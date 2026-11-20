package telemetry

import (
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// Spec 126 FR-038 (T080): success_scope_ever moves forward only, in the order
// none -> starter_only -> non_starter, and is persisted.

func TestSuccessScope_WireValues(t *testing.T) {
	for scope, want := range map[SuccessScope]string{
		SuccessScopeNone:        "none",
		SuccessScopeStarterOnly: "starter_only",
		SuccessScopeNonStarter:  "non_starter",
	} {
		if got := scope.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", scope, got, want)
		}
	}
}

func TestSuccessScope_MovesForwardOnly(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()

	mustScope := func(want SuccessScope) {
		t.Helper()
		got, err := LoadSuccessScope(db)
		if err != nil {
			t.Fatalf("LoadSuccessScope: %v", err)
		}
		if got != want {
			t.Fatalf("scope = %s, want %s", got, want)
		}
	}

	mustScope(SuccessScopeNone)
	if err := RecordSuccessScope(db, true); err != nil {
		t.Fatal(err)
	}
	mustScope(SuccessScopeStarterOnly)
	if err := RecordSuccessScope(db, true); err != nil {
		t.Fatal(err)
	}
	mustScope(SuccessScopeStarterOnly)
	if err := RecordSuccessScope(db, false); err != nil {
		t.Fatal(err)
	}
	mustScope(SuccessScopeNonStarter)
	// Nothing moves it back.
	if err := RecordSuccessScope(db, true); err != nil {
		t.Fatal(err)
	}
	mustScope(SuccessScopeNonStarter)
}

// A non-starter success on a fresh store goes straight to non_starter.
func TestSuccessScope_NonStarterFirst(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()
	if err := RecordSuccessScope(db, false); err != nil {
		t.Fatal(err)
	}
	if err := RecordSuccessScope(db, true); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSuccessScope(db)
	if err != nil || got != SuccessScopeNonStarter {
		t.Fatalf("scope = %s, %v; want non_starter", got, err)
	}
}

// The value is persisted and survives a reopen of the store.
func TestSuccessScope_SurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scope.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordSuccessScope(db, true); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := LoadSuccessScope(db)
	if err != nil || got != SuccessScopeStarterOnly {
		t.Fatalf("after reopen: scope = %s, %v; want starter_only", got, err)
	}
}

// A stored value outside the ordinal range is an error, never a guess, and a
// later success does not overwrite it.
func TestSuccessScope_CorruptValueIsAnError(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()
	if err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(ActivationBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(activationKeySuccessScopeEver), []byte{7})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuccessScope(db); err == nil {
		t.Fatal("a corrupt scope must be reported")
	}
	if err := RecordSuccessScope(db, false); err == nil {
		t.Fatal("recording over a corrupt scope must fail rather than overwrite it")
	}
}
