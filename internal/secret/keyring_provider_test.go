package secret

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// withKeyringMocks swaps the package-level keyring function variables for
// the duration of a test and restores them afterwards.
func withKeyringMocks(t *testing.T, getFn func(service, user string) (string, error), setFn func(service, user, password string) error, delFn func(service, user string) error) {
	t.Helper()
	origGet, origSet, origDel := keyringGetFn, keyringSetFn, keyringDelFn
	if getFn != nil {
		keyringGetFn = getFn
	}
	if setFn != nil {
		keyringSetFn = setFn
	}
	if delFn != nil {
		keyringDelFn = delFn
	}
	t.Cleanup(func() {
		keyringGetFn = origGet
		keyringSetFn = origSet
		keyringDelFn = origDel
	})
}

// clearHeadlessEnv unsets environment variables that would make the provider
// skip the probe entirely.
func clearHeadlessEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("SUDO_USER", "")
	if runtime.GOOS == "linux" {
		// Make sure the Linux fast-path doesn't fire: set DISPLAY so
		// isHeadlessEnvironment() returns false.
		t.Setenv("DISPLAY", ":0")
	}
}

func TestKeyringProvider_IsAvailable_ErrNotFound(t *testing.T) {
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			return "", keyring.ErrNotFound
		},
		nil, nil,
	)
	p := NewKeyringProvider()
	if !p.IsAvailable() {
		t.Error("expected IsAvailable() to be true when Get returns ErrNotFound")
	}
}

func TestKeyringProvider_IsAvailable_UnknownError(t *testing.T) {
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			return "", errors.New("backend is unreachable")
		},
		nil, nil,
	)
	p := NewKeyringProvider()
	if p.IsAvailable() {
		t.Error("expected IsAvailable() to be false on unknown errors")
	}
}

// TestKeyringProvider_IsAvailable_DoesNotSet verifies the probe is read-only —
// it must NEVER invoke keyring.Set, which is what triggered the macOS modal.
func TestKeyringProvider_IsAvailable_DoesNotSet(t *testing.T) {
	clearHeadlessEnv(t)
	setCalled := false
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			return "", keyring.ErrNotFound
		},
		func(service, user, password string) error {
			setCalled = true
			return nil
		},
		nil,
	)
	p := NewKeyringProvider()
	_ = p.IsAvailable()
	if setCalled {
		t.Fatal("IsAvailable() must not call keyring.Set (Bug F-03)")
	}
}

// TestKeyringProvider_IsAvailable_HangingBackend verifies the 2-second
// timeout is enforced when the keyring hangs (macOS Keychain wedged).
func TestKeyringProvider_IsAvailable_HangingBackend(t *testing.T) {
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			time.Sleep(10 * time.Second) // way longer than timeout
			return "", nil
		},
		nil, nil,
	)
	p := NewKeyringProvider()

	start := time.Now()
	ok := p.IsAvailable()
	elapsed := time.Since(start)

	if ok {
		t.Error("expected IsAvailable() to be false on hang")
	}
	if elapsed > 3*time.Second {
		t.Errorf("IsAvailable() took %v, expected to bail out within 2-3s", elapsed)
	}
}

func TestKeyringProvider_IsAvailable_HeadlessEnv(t *testing.T) {
	// CI env should short-circuit to unavailable without probing.
	t.Setenv("CI", "true")
	probed := false
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			probed = true
			return "", keyring.ErrNotFound
		},
		nil, nil,
	)
	p := NewKeyringProvider()
	if p.IsAvailable() {
		t.Error("expected IsAvailable() to be false in CI env")
	}
	if probed {
		t.Error("IsAvailable() should not probe in headless env")
	}
}

// TestKeyringProvider_NoTestAvailabilityKey makes sure the removed probe key
// does not leak into the source again.
func TestKeyringProvider_NoTestAvailabilityKey(t *testing.T) {
	data, err := os.ReadFile("keyring_provider.go")
	if err != nil {
		t.Fatalf("read keyring_provider.go: %v", err)
	}
	if contains(string(data), "_mcpproxy_test_availability") {
		t.Error("_mcpproxy_test_availability key must not exist anymore (Bug F-03)")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
