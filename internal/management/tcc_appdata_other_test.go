//go:build !darwin

package management

import "testing"

// TestAppDataDenialWarning_NoOpOffDarwin asserts the doctor App-Data check is a
// no-op on non-macOS platforms: it never probes client configs and never emits a
// warning (Spec 075 US3, T021).
func TestAppDataDenialWarning_NoOpOffDarwin(t *testing.T) {
	warning, ok := appDataDenialWarning()
	if ok || warning != "" {
		t.Fatalf("appDataDenialWarning must be a no-op off darwin, got ok=%v warning=%q", ok, warning)
	}
}
