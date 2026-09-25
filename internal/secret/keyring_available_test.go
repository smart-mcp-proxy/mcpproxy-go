package secret

import (
	"errors"
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
	ok, reason := p.IsAvailableWithReason()
	if !ok {
		t.Fatal("expected available")
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
