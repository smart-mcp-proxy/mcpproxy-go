//go:build teams

package teams

import (
	"errors"
	"testing"
)

func TestRegisterAndSetupAll(t *testing.T) {
	// Reset global state for test isolation
	features = nil

	called := false
	Register(Feature{
		Name: "test-feature",
		Setup: func(deps Dependencies) error {
			called = true
			return nil
		},
	})

	if err := SetupAll(Dependencies{}); err != nil {
		t.Fatalf("SetupAll failed: %v", err)
	}
	if !called {
		t.Fatal("expected feature Setup to be called")
	}
}

func TestSetupAllError(t *testing.T) {
	features = nil

	Register(Feature{
		Name: "failing-feature",
		Setup: func(deps Dependencies) error {
			return errors.New("setup failed")
		},
	})

	err := SetupAll(Dependencies{})
	if err == nil {
		t.Fatal("expected error from SetupAll")
	}
	if err.Error() != "teams feature failing-feature: setup failed" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRegisteredFeatures(t *testing.T) {
	features = nil

	Register(Feature{Name: "auth", Setup: func(deps Dependencies) error { return nil }})
	Register(Feature{Name: "workspace", Setup: func(deps Dependencies) error { return nil }})

	names := RegisteredFeatures()
	if len(names) != 2 {
		t.Fatalf("expected 2 features, got %d", len(names))
	}
	if names[0] != "auth" || names[1] != "workspace" {
		t.Fatalf("unexpected feature names: %v", names)
	}
}
