package secret

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// GH #1924: on macOS, a Store refused only because the write opt-in is absent
// used to return ErrKeyringUnavailable, whose text ("backend not usable on
// this system") describes a broken Keychain. The Keychain was never consulted.
// The refusal must say what it is — and still satisfy every caller that
// falls back on ErrKeyringUnavailable.
func TestKeyringProvider_Store_MacWriteGate_NamesTheOptIn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only write gate")
	}
	clearHeadlessEnv(t)
	t.Setenv("MCPPROXY_KEYRING_WRITE", "")
	setCalled := false
	withKeyringMocks(t,
		func(service, user string) (string, error) { return "", keyring.ErrNotFound },
		func(service, user, password string) error { setCalled = true; return nil },
		nil,
	)
	p := NewKeyringProvider()

	err := p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "gate_check"}, "value")
	if setCalled {
		t.Fatal("the gate must refuse before keyring.Set")
	}
	if !errors.Is(err, ErrKeyringWriteOptIn) {
		t.Fatalf("expected ErrKeyringWriteOptIn, got %v", err)
	}
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("the opt-in refusal must still match ErrKeyringUnavailable for fallback callers, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "MCPPROXY_KEYRING_WRITE=1") {
		t.Errorf("the refusal must name the opt-in, got %q", msg)
	}
	if strings.Contains(msg, "backend not usable") {
		t.Errorf("the refusal must not describe a broken backend, got %q", msg)
	}
}

// A backend that really is unusable keeps the plain ErrKeyringUnavailable: the
// opt-in is present, so naming it would send the user the wrong way.
func TestKeyringProvider_Store_BrokenBackend_IsNotTheOptIn(t *testing.T) {
	clearHeadlessEnv(t)
	withKeyringMocks(t,
		func(service, user string) (string, error) { return "", errSomeBackend },
		func(service, user, password string) error { return nil },
		nil,
	)
	p := NewKeyringProvider()
	p.SetWritesEnabled(true)

	err := p.Store(context.TODO(), Ref{Type: SecretTypeKeyring, Name: "gate_check"}, "value")
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("expected ErrKeyringUnavailable, got %v", err)
	}
	if errors.Is(err, ErrKeyringWriteOptIn) {
		t.Fatalf("a broken backend must not be reported as a missing opt-in, got %v", err)
	}
}

func TestKeyringProvider_WriteOptInRequired(t *testing.T) {
	clearHeadlessEnv(t)
	t.Setenv("MCPPROXY_KEYRING_WRITE", "")
	p := NewKeyringProvider()

	if got, want := p.WriteOptInRequired(), runtime.GOOS == "darwin"; got != want {
		t.Fatalf("without an opt-in: WriteOptInRequired() = %v, want %v on %s", got, want, runtime.GOOS)
	}

	t.Setenv("MCPPROXY_KEYRING_WRITE", "1")
	if p.WriteOptInRequired() {
		t.Fatal("MCPPROXY_KEYRING_WRITE=1 satisfies the opt-in")
	}

	// A headless environment has no keychain to opt in to; its own reason
	// wins (IsAvailableWithReason reports it first).
	t.Setenv("MCPPROXY_KEYRING_WRITE", "")
	t.Setenv("CI", "true")
	if p.WriteOptInRequired() {
		t.Fatal("a headless environment is not an opt-in problem")
	}
}

// The resolver exposes the same answer for whichever keyring provider is
// registered; a provider with no such gate never requires an opt-in.
func TestResolver_KeyringWriteOptInRequired(t *testing.T) {
	clearHeadlessEnv(t)
	t.Setenv("MCPPROXY_KEYRING_WRITE", "")

	r := NewResolver()
	if got, want := r.KeyringWriteOptInRequired(), runtime.GOOS == "darwin"; got != want {
		t.Fatalf("KeyringWriteOptInRequired() = %v, want %v on %s", got, want, runtime.GOOS)
	}

	r.RegisterProvider("keyring", NewEnvProvider(false))
	if r.KeyringWriteOptInRequired() {
		t.Fatal("a provider without a write gate requires no opt-in")
	}
}
