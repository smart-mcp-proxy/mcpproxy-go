package secret

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

var errSomeBackend = errors.New("some backend error")

// TestKeyringProvider_IsAvailableWithReason_Available pins that a healthy
// probe reports available with no reason (FR-065, GET /secrets/config).
func TestKeyringProvider_IsAvailableWithReason_Available(t *testing.T) {
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) { return "", keyring.ErrNotFound },
		nil, nil,
	)
	p := NewKeyringProvider()
	// This test is about the probe outcome, not the macOS write opt-in
	// (covered separately below), so force writes enabled.
	p.SetWritesEnabled(true)
	ok, reason := p.IsAvailableWithReason()
	if !ok {
		t.Fatal("expected available")
	}
	if reason != "" {
		t.Errorf("expected empty reason when available, got %q", reason)
	}
}

// TestKeyringProvider_IsAvailableWithReason_MacWriteGateNotOptedIn pins that
// on macOS, without the MCPPROXY_KEYRING_WRITE opt-in (or a tray-launched
// core setting it), IsAvailableWithReason reports UNAVAILABLE even when the
// read-only probe would succeed. This is a write-availability signal (it
// backs GET /secrets/config's keyring_available, and the CLI's pre-check in
// applySecretFlags before --secret-env/--secret-header), so it must agree
// with what Store() will actually do: Store() unconditionally refuses with
// ErrKeyringUnavailable on macOS when writesEnabled() is false, regardless
// of probe health. Before this fix, IsAvailableWithReason ran only the
// read-only probe and ignored the write gate entirely, so it reported
// "available" right before the next Store() call failed.
func TestKeyringProvider_IsAvailableWithReason_MacWriteGateNotOptedIn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only write gate")
	}
	clearHeadlessEnv(t)
	t.Setenv("MCPPROXY_KEYRING_WRITE", "")
	withKeyringMocks(t,
		func(service, user string) (string, error) { return "", keyring.ErrNotFound },
		nil, nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(false) // explicit: no opt-in, as a headless `mcpproxy serve` would see

	ok, reason := p.IsAvailableWithReason()
	if ok {
		t.Fatal("expected unavailable when macOS keyring writes are not opted in, even though the read probe would succeed")
	}
	if reason == "" {
		t.Error("expected a non-empty reason explaining the write opt-in is missing")
	}

	// The write-availability signal must match what Store() actually does.
	err := p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "gate_check"}, "value")
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("expected Store() to also refuse with ErrKeyringUnavailable, got %v", err)
	}
}

// TestKeyringProvider_IsAvailableWithReason_MacWriteGateOptedIn pins that
// opting in via SetWritesEnabled(true) (what MCPPROXY_KEYRING_WRITE=1 does
// via writesEnabled()) restores the plain probe-based result on macOS.
func TestKeyringProvider_IsAvailableWithReason_MacWriteGateOptedIn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only write gate")
	}
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) { return "", keyring.ErrNotFound },
		nil, nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true)

	ok, reason := p.IsAvailableWithReason()
	if !ok {
		t.Fatal("expected available once macOS keyring writes are opted in")
	}
	if reason != "" {
		t.Errorf("expected empty reason when available, got %q", reason)
	}
}

// TestKeyringProvider_IsAvailableWithReason_CI pins the headless CI reason so
// the disabled secret toggle can explain itself instead of going silently
// dark.
func TestKeyringProvider_IsAvailableWithReason_CI(t *testing.T) {
	t.Setenv("CI", "true")
	p := NewKeyringProvider()
	ok, reason := p.IsAvailableWithReason()
	if ok {
		t.Fatal("expected unavailable in CI")
	}
	if reason == "" {
		t.Error("expected a non-empty reason in CI")
	}
}

// TestKeyringProvider_IsAvailableWithReason_ProbeFails pins the generic probe
// failure reason.
func TestKeyringProvider_IsAvailableWithReason_ProbeFails(t *testing.T) {
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) { return "", errSomeBackend },
		nil, nil,
	)
	p := NewKeyringProvider()
	ok, reason := p.IsAvailableWithReason()
	if ok {
		t.Fatal("expected unavailable when the probe fails")
	}
	if reason == "" {
		t.Error("expected a non-empty reason when the probe fails")
	}
}

// TestResolver_KeyringAvailability pins that the resolver forwards to the
// registered keyring provider's reasoned probe.
func TestResolver_KeyringAvailability(t *testing.T) {
	t.Setenv("CI", "true")
	r := NewResolver()
	ok, reason := r.KeyringAvailability()
	if ok {
		t.Fatal("expected unavailable in CI")
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}
