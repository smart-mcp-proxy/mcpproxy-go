package sandbox

import "testing"

func TestWantsLandlock(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want bool
	}{
		{"empty", Spec{}, false},
		{"ro only", Spec{ReadOnlyPaths: []string{"/usr"}}, true},
		{"rw only", Spec{ReadWritePaths: []string{"/tmp/x"}}, true},
		{"rlimits only", Spec{Rlimits: []Rlimit{{Resource: 0, Cur: 1, Max: 1}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.wantsLandlock(); got != c.want {
				t.Errorf("wantsLandlock() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestApplyEmptySpecIsSafe verifies Apply with no filesystem allowlist and no
// rlimits is a harmless no-op on every platform (it must NOT restrict the test
// process, which would break the rest of the suite).
func TestApplyEmptySpecIsSafe(t *testing.T) {
	rep, err := Apply(Spec{})
	if err != nil {
		t.Fatalf("Apply(empty) returned error: %v", err)
	}
	if rep.LandlockABI != 0 {
		t.Errorf("expected LandlockABI=0 for no-fs spec, got %d", rep.LandlockABI)
	}
	if rep.RlimitsSet != 0 {
		t.Errorf("expected RlimitsSet=0, got %d", rep.RlimitsSet)
	}
	if rep.LandlockNote == "" {
		t.Error("expected a non-empty note explaining no confinement was applied")
	}
}
