package secret

import (
	"context"
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

// TestKeyringProvider_Store_MacOSDefaultRefuses pins the user-observed
// F-03 follow-up bug:
//
//	"A keychain cannot be found to store scanner_mcp-scan_snyk_token."
//
// On macOS, by default, Store MUST return ErrKeyringUnavailable WITHOUT
// ever calling keyring.Set — because Set is what triggers the system
// modal, and the goroutine wrapping it cannot be cancelled once started.
// The only safe behavior is to not call Set at all unless the user has
// explicitly opted in via MCPPROXY_KEYRING_WRITE or SetWritesEnabled.
func TestKeyringProvider_Store_MacOSDefaultRefuses(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific hard gate")
	}
	t.Setenv("MCPPROXY_KEYRING_WRITE", "") // no opt-in
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

	err := p.Store(context.TODO(), Ref{
		Type: SecretTypeKeyring,
		Name: "scanner_mcp-scan_snyk_token",
	}, "fake-snyk-token")

	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("expected ErrKeyringUnavailable, got %v", err)
	}
	if setCalled {
		t.Fatal("Store() MUST NOT call keyring.Set on macOS by default (F-03 follow-up — prevents 'Keychain Not Found' modal)")
	}
}

// TestKeyringProvider_Store_MacOSOptIn verifies that users who explicitly
// opt in via SetWritesEnabled(true) can still write to the keychain.
// The three-layer guard still applies (known-unavailable short-circuit,
// first-time probe, failure cache).
func TestKeyringProvider_Store_MacOSOptIn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific opt-in")
	}
	clearHeadlessEnv(t)
	stored := make(map[string]string)
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			if v, ok := stored[user]; ok {
				return v, nil
			}
			return "", keyring.ErrNotFound
		},
		func(service, user, password string) error {
			stored[user] = password
			return nil
		},
		nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true)

	if err := p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "scanner_ok"}, "value"); err != nil {
		t.Fatalf("opt-in Store should succeed: %v", err)
	}
	if stored["scanner_ok"] != "value" {
		t.Errorf("expected value to be stored, got %q", stored["scanner_ok"])
	}
}

// TestKeyringProvider_Store_ShortCircuitsWhenKnownUnavailable covers the
// non-macOS case where IsAvailable has decided the keyring is broken.
// On macOS it is covered by the hard gate test above.
func TestKeyringProvider_Store_ShortCircuitsWhenKnownUnavailable(t *testing.T) {
	clearHeadlessEnv(t)
	setCalled := false
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			return "", errors.New("keychain broken")
		},
		func(service, user, password string) error {
			setCalled = true
			return nil
		},
		nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true) // let the three-layer guard be the test subject

	if p.IsAvailable() {
		t.Fatal("precondition: IsAvailable() should return false")
	}

	err := p.Store(context.TODO(), Ref{
		Type: SecretTypeKeyring,
		Name: "scanner_mcp-scan_snyk_token",
	}, "fake-snyk-token")

	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("expected ErrKeyringUnavailable, got %v", err)
	}
	if setCalled {
		t.Fatal("Store() must not call keyring.Set when keyring is known unavailable")
	}
}

// TestKeyringProvider_Store_ProbesOnFirstCall — with writes enabled,
// verify that the inline probe still catches a broken keychain before Set.
func TestKeyringProvider_Store_ProbesOnFirstCall(t *testing.T) {
	clearHeadlessEnv(t)
	setCalled := false
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			return "", errors.New("keychain broken")
		},
		func(service, user, password string) error {
			setCalled = true
			return nil
		},
		nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true)

	err := p.Store(context.TODO(), Ref{
		Type: SecretTypeKeyring,
		Name: "scanner_mcp-scan_snyk_token",
	}, "fake-snyk-token")

	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("expected ErrKeyringUnavailable, got %v", err)
	}
	if setCalled {
		t.Fatal("Store() must probe and short-circuit before calling keyring.Set")
	}
}

// TestKeyringProvider_Store_CachesFailure — with writes enabled, verify
// that a Set failure caches as unavailable so subsequent Store calls don't
// retry and risk popping the modal again.
func TestKeyringProvider_Store_CachesFailure(t *testing.T) {
	clearHeadlessEnv(t)
	setCalls := 0
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			return "", keyring.ErrNotFound
		},
		func(service, user, password string) error {
			setCalls++
			return errors.New("keychain locked")
		},
		nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true)

	_ = p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "scanner_k1"}, "v1")
	if setCalls != 1 {
		t.Errorf("expected 1 Set call on first Store, got %d", setCalls)
	}

	err := p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "scanner_k2"}, "v2")
	if setCalls != 1 {
		t.Errorf("expected Set to still be called only 1 time after failure cache, got %d", setCalls)
	}
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Errorf("expected second Store to return ErrKeyringUnavailable, got %v", err)
	}
}

// TestKeyringProvider_Store_AllowsValidWrites — happy path with writes
// enabled on a healthy backend.
func TestKeyringProvider_Store_AllowsValidWrites(t *testing.T) {
	clearHeadlessEnv(t)
	stored := make(map[string]string)
	withKeyringMocks(t,
		func(service, user string) (string, error) {
			if v, ok := stored[user]; ok {
				return v, nil
			}
			return "", keyring.ErrNotFound
		},
		func(service, user, password string) error {
			stored[user] = password
			return nil
		},
		nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true)
	if !p.IsAvailable() {
		t.Fatal("precondition: IsAvailable() should pass on healthy backend")
	}
	if err := p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "scanner_ok"}, "value"); err != nil {
		t.Fatalf("Store() should succeed on healthy backend: %v", err)
	}
	if stored["scanner_ok"] != "value" {
		t.Errorf("expected value to be stored, got %q", stored["scanner_ok"])
	}
}
