package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	// ServiceName for keyring entries
	ServiceName       = "mcpproxy"
	SecretTypeKeyring = "keyring"
	RegistryKey       = "_mcpproxy_secret_registry"

	// keyringProbeKey is used for the read-only availability probe. A Get for
	// a non-existent key returns ErrNotFound on all platforms without showing
	// any UI prompts (unlike Set, which historically popped the "Keychain Not
	// Found" modal on macOS when the default keychain was missing).
	keyringProbeKey = "_mcpproxy_probe"

	// keyringProbeTimeout is the hard deadline for the availability probe.
	// If the underlying keychain hangs (e.g. waiting on a user prompt), we
	// bail out and report unavailable rather than blocking the caller.
	keyringProbeTimeout = 2 * time.Second

	// keyringOpTimeout is the hard deadline for real keyring operations
	// (Get/Set/Delete) invoked via the public KeyringProvider API. If the
	// OS keychain backend is wedged (e.g. waiting on a modal prompt that
	// nobody is going to click), we bail out with ErrKeyringTimeout rather
	// than blocking the server process and timing out the HTTP client.
	keyringOpTimeout = 3 * time.Second
)

// ErrKeyringTimeout is returned when a keyring operation exceeds
// keyringOpTimeout. Callers SHOULD treat this identically to "keyring
// unavailable" and fall back to an alternative secret store (in-memory,
// config-file, etc.) rather than propagating the error.
var ErrKeyringTimeout = errors.New("keyring operation timed out (backend unresponsive — likely waiting on a user prompt)")

// ErrKeyringUnavailable is returned by write operations (Store/Delete) when
// a previous IsAvailable() call determined the keyring backend is not
// usable on this system. We refuse to call keyring.Set in that state
// because on macOS it pops a system modal ("Keychain Not Found") whose
// default action is destructive. Callers MUST fall back to an alternative
// secret store.
var ErrKeyringUnavailable = errors.New("keyring unavailable (backend not usable on this system — refusing to write to avoid OS modal prompts)")

// Package-level function variables allow tests to inject a fake/hanging
// keyring without reaching into the real OS keychain.
var (
	keyringGetFn = keyring.Get
	keyringSetFn = keyring.Set
	keyringDelFn = keyring.Delete
)

// availabilityState captures the cached result of an IsAvailable() probe.
// Values: 0 = unknown (probe not yet run), 1 = available, 2 = unavailable.
const (
	availUnknown int32 = 0
	availYes     int32 = 1
	availNo      int32 = 2
)

// KeyringProvider resolves secrets from OS keyring (Keychain, Secret Service, WinCred)
type KeyringProvider struct {
	serviceName string

	// availability is the cached result of the most recent IsAvailable()
	// probe. Read/written atomically. When availNo, all write operations
	// short-circuit to ErrKeyringUnavailable so we NEVER call keyring.Set
	// on a broken/unusable keychain — which is what pops the macOS modal.
	availability int32

	// writeOverride allows tests (and future programmatic opt-in) to force
	// the macOS write gate without going through env vars. 0 = respect
	// env, 1 = force enabled, 2 = force disabled. Read/written atomically.
	writeOverride int32
}

// NewKeyringProvider creates a new keyring provider
func NewKeyringProvider() *KeyringProvider {
	return &KeyringProvider{
		serviceName: ServiceName,
	}
}

// markUnavailable records that the keyring is not usable. After this call,
// all Store/Delete operations will return ErrKeyringUnavailable without
// touching the OS keyring backend.
func (p *KeyringProvider) markUnavailable() {
	atomic.StoreInt32(&p.availability, availNo)
}

// markAvailable records that the keyring has been successfully probed and
// is safe to write to.
func (p *KeyringProvider) markAvailable() {
	atomic.StoreInt32(&p.availability, availYes)
}

// isKnownUnavailable returns true if a previous probe determined the
// keyring is not usable. Returns false for both "known available" AND
// "not yet probed" so callers can optimistically attempt a first-time
// write before the cache is populated.
func (p *KeyringProvider) isKnownUnavailable() bool {
	return atomic.LoadInt32(&p.availability) == availNo
}

// CanResolve returns true if this provider can handle the given secret type
func (p *KeyringProvider) CanResolve(secretType string) bool {
	return secretType == SecretTypeKeyring
}

// runWithTimeout runs fn in a goroutine and returns its result, or
// ErrKeyringTimeout if the goroutine doesn't finish before keyringOpTimeout.
// The goroutine is allowed to keep running in the background (it cannot be
// cancelled because the underlying keyring package has no Context API), but
// its result is discarded.
func runWithTimeout(fn func() error) error {
	ch := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- fmt.Errorf("keyring backend panicked: %v", r)
			}
		}()
		ch <- fn()
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(keyringOpTimeout):
		return ErrKeyringTimeout
	}
}

// Resolve retrieves the secret value from the OS keyring
func (p *KeyringProvider) Resolve(_ context.Context, ref Ref) (string, error) {
	if !p.CanResolve(ref.Type) {
		return "", fmt.Errorf("keyring provider cannot resolve secret type: %s", ref.Type)
	}

	var secret string
	err := runWithTimeout(func() error {
		var gerr error
		secret, gerr = keyringGetFn(p.serviceName, ref.Name)
		return gerr
	})
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s from keyring: %w", ref.Name, err)
	}

	return secret, nil
}

// Store saves a secret to the OS keyring and updates the registry.
//
// By default on macOS this method does NOT actually call keyring.Set —
// it returns ErrKeyringUnavailable immediately. Rationale: on macOS
// keyring.Set wraps Security.framework under the hood; if the user's
// default keychain is missing/locked/in an unusual state, Security.framework
// pops a system modal ("Keychain Not Found" with a destructive default
// button). The underlying call is blocking and the goroutine we'd wrap it
// in cannot be cancelled once started — it keeps running until Security.
// framework finally responds, which may involve the user clicking buttons.
// No wrapper / timeout / probe can prevent the modal from appearing, so
// the only safe option is to not call keyring.Set at all.
//
// Users who want keyring-backed storage on macOS can opt in by setting the
// environment variable MCPPROXY_KEYRING_WRITE=1 or calling
// EnableKeyringWrites(true) on the provider. When opted in, Store runs a
// three-layer guard (known-unavailable cache, first-time probe, failure
// cache) plus a 3s runWithTimeout wrapper — but note that the wrapper only
// protects the CALLING goroutine from blocking; it cannot prevent the
// modal from appearing on the user's screen once keyring.Set is called.
//
// On non-macOS platforms (Linux Secret Service, Windows Credential
// Manager), Store goes through the three-layer guard without requiring
// the opt-in, because those backends don't have the same modal issue.
//
// Callers MUST treat ErrKeyringUnavailable as "fall back to in-config /
// in-memory storage" and surface a clear log message.
func (p *KeyringProvider) Store(_ context.Context, ref Ref, value string) error {
	if !p.CanResolve(ref.Type) {
		return fmt.Errorf("keyring provider cannot store secret type: %s", ref.Type)
	}

	// macOS-specific hard gate: do not call keyring.Set unless the caller
	// has explicitly opted in. The opt-in can be global via the
	// MCPPROXY_KEYRING_WRITE env var (value "1", "true", or "yes"), or
	// per-provider via SetWritesEnabled(true).
	if runtime.GOOS == "darwin" && !p.writesEnabled() {
		p.markUnavailable()
		return ErrKeyringUnavailable
	}

	// Layer 1: if we already know the keyring is broken, bail out without
	// touching it.
	if p.isKnownUnavailable() {
		return ErrKeyringUnavailable
	}

	// Layer 2: if we haven't probed yet, do it now — BEFORE calling Set.
	// IsAvailable uses a read-only Get probe which is safe on all platforms.
	if atomic.LoadInt32(&p.availability) == availUnknown {
		if !p.IsAvailable() {
			return ErrKeyringUnavailable
		}
	}

	err := runWithTimeout(func() error {
		return keyringSetFn(p.serviceName, ref.Name, value)
	})
	if err != nil {
		// Layer 3: cache the failure so subsequent writes don't retry and
		// risk popping another modal.
		p.markUnavailable()
		return fmt.Errorf("failed to store secret %s in keyring: %w", ref.Name, err)
	}

	// Add to registry so it appears in list
	if err := p.addToRegistry(ref.Name); err != nil {
		return fmt.Errorf("failed to update secret registry: %w", err)
	}

	return nil
}

// writesEnabled reports whether this provider is allowed to call
// keyring.Set on the current platform. On non-macOS systems writes are
// always enabled. On macOS writes require an explicit opt-in via the
// MCPPROXY_KEYRING_WRITE env var or a test-only override.
func (p *KeyringProvider) writesEnabled() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	if p.writeOverride != 0 {
		return p.writeOverride == 1
	}
	v := strings.ToLower(os.Getenv("MCPPROXY_KEYRING_WRITE"))
	return v == "1" || v == "true" || v == "yes"
}

// SetWritesEnabled is a test-only helper that forces the opt-in state
// regardless of env vars. Pass true to allow keyring.Set, false to
// disallow. It is safe to call at any time — Store checks the override
// synchronously.
func (p *KeyringProvider) SetWritesEnabled(enabled bool) {
	if enabled {
		atomic.StoreInt32(&p.writeOverride, 1)
	} else {
		atomic.StoreInt32(&p.writeOverride, 2)
	}
}

// Delete removes a secret from the OS keyring and updates the registry.
// Same three-layer guard as Store: known-unavailable short-circuit,
// first-time probe, and failure cache.
func (p *KeyringProvider) Delete(_ context.Context, ref Ref) error {
	if !p.CanResolve(ref.Type) {
		return fmt.Errorf("keyring provider cannot delete secret type: %s", ref.Type)
	}

	if p.isKnownUnavailable() {
		return ErrKeyringUnavailable
	}

	if atomic.LoadInt32(&p.availability) == availUnknown {
		if !p.IsAvailable() {
			return ErrKeyringUnavailable
		}
	}

	err := runWithTimeout(func() error {
		return keyringDelFn(p.serviceName, ref.Name)
	})
	if err != nil {
		p.markUnavailable()
		return fmt.Errorf("failed to delete secret %s from keyring: %w", ref.Name, err)
	}

	// Remove from registry
	err = p.removeFromRegistry(ref.Name)
	if err != nil {
		return fmt.Errorf("failed to update secret registry: %w", err)
	}

	return nil
}

// List returns all secret references stored in the keyring
// Note: go-keyring doesn't provide a list function, so we'll track them differently
func (p *KeyringProvider) List(_ context.Context) ([]Ref, error) {
	// Since go-keyring doesn't provide a list function, we maintain a special
	// registry entry that tracks all our secret names
	registryKey := RegistryKey

	var registry string
	if err := runWithTimeout(func() error {
		var gerr error
		registry, gerr = keyringGetFn(p.serviceName, registryKey)
		return gerr
	}); err != nil {
		// Registry doesn't exist yet or keyring hung - return empty list
		return []Ref{}, nil
	}

	var refs []Ref
	if registry != "" {
		names := strings.Split(registry, "\n")
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				refs = append(refs, Ref{
					Type:     "keyring",
					Name:     name,
					Original: fmt.Sprintf("${keyring:%s}", name),
				})
			}
		}
	}

	return refs, nil
}

// IsAvailable checks if the keyring is available on the current system,
// and caches the result. Subsequent calls to Store/Delete will short-circuit
// to ErrKeyringUnavailable WITHOUT calling keyring.Set if the probe decided
// the keyring is not usable — this is the key property that prevents the
// macOS "Keychain Not Found" modal from ever being shown to the user.
//
// This is deliberately conservative and NEVER calls keyring.Set as a probe:
// on macOS, Set triggers the "Keychain Not Found" system modal when the
// user's default keychain is missing/locked/corrupted, whose default button
// is the destructive "Reset To Defaults" action.
//
// Instead, we:
//  1. Skip the probe entirely in obvious headless/CI environments.
//  2. Do a single read-only Get for a non-existent key and treat
//     ErrNotFound as "available, no secret yet". Any other error (including
//     ErrUnsupportedPlatform and backend communication failures) is treated
//     as "unavailable".
//  3. Run the Get inside a goroutine with a 2s hard timeout so a hung
//     keychain backend cannot stall the caller.
//  4. Cache the result via markAvailable/markUnavailable so Store/Delete
//     can short-circuit on the fast path.
func (p *KeyringProvider) IsAvailable() bool {
	// Heuristic fast-path: if we're clearly running headless (CI, no
	// X display on Linux, etc.) don't even try the keychain — it will
	// either fail or prompt.
	if isHeadlessEnvironment() {
		p.markUnavailable()
		return false
	}

	type result struct {
		ok bool
	}
	ch := make(chan result, 1)

	// Capture the current Get implementation locally. This prevents a race
	// with tests that swap keyringGetFn back after the timeout elapses — the
	// goroutine holds its own reference and can keep running harmlessly.
	getFn := keyringGetFn
	serviceName := p.serviceName

	go func() {
		// A read-only Get for a nonexistent key is the safest probe we
		// can perform: on macOS it returns errSecItemNotFound without
		// prompting, on Linux it returns ErrNotFound from Secret Service,
		// and on Windows it returns ErrNotFound from wincred.
		_, err := getFn(serviceName, keyringProbeKey)
		switch {
		case err == nil:
			// Extremely unlikely (we never store this key), but it's fine.
			ch <- result{ok: true}
		case errors.Is(err, keyring.ErrNotFound):
			ch <- result{ok: true}
		default:
			ch <- result{ok: false}
		}
	}()

	select {
	case r := <-ch:
		if r.ok {
			p.markAvailable()
		} else {
			p.markUnavailable()
		}
		return r.ok
	case <-time.After(keyringProbeTimeout):
		// The keychain is wedged. Bail out — the caller should fall back
		// to in-memory / config-only secret storage.
		p.markUnavailable()
		return false
	}
}

// isHeadlessEnvironment returns true when we can confidently say no
// interactive keyring is available. We only use this as a FAST path to
// skip probing; returning false does not mean the keyring IS available.
func isHeadlessEnvironment() bool {
	// CI environments universally set CI=true
	if v := strings.ToLower(os.Getenv("CI")); v == "true" || v == "1" || v == "yes" {
		return true
	}
	switch runtime.GOOS {
	case "linux":
		// No X display AND no Wayland → no Secret Service prompt UI
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return true
		}
	case "darwin":
		// Running under sudo — the keychain being probed is likely root's
		// default keychain, which doesn't exist and will pop the modal.
		if os.Getenv("SUDO_USER") != "" {
			return true
		}
	}
	return false
}

// addToRegistry adds a secret name to our internal registry
func (p *KeyringProvider) addToRegistry(secretName string) error {
	registryKey := RegistryKey

	// Get current registry (under timeout — if this hangs we degrade gracefully)
	var registry string
	_ = runWithTimeout(func() error {
		var gerr error
		registry, gerr = keyringGetFn(p.serviceName, registryKey)
		return gerr
	})
	// If Get returned an error (including timeout), start with an empty registry.

	// Check if secret is already in registry
	names := strings.Split(registry, "\n")
	for _, name := range names {
		if strings.TrimSpace(name) == secretName {
			return nil // Already exists
		}
	}

	// Add to registry
	if registry != "" {
		registry += "\n"
	}
	registry += secretName

	return runWithTimeout(func() error {
		return keyringSetFn(p.serviceName, registryKey, registry)
	})
}

// removeFromRegistry removes a secret name from our internal registry
func (p *KeyringProvider) removeFromRegistry(secretName string) error {
	registryKey := RegistryKey

	var registry string
	getErr := runWithTimeout(func() error {
		var gerr error
		registry, gerr = keyringGetFn(p.serviceName, registryKey)
		return gerr
	})
	if getErr != nil {
		return nil // Registry doesn't exist or keyring hung - nothing to remove
	}

	// Remove from registry
	names := strings.Split(registry, "\n")
	var newNames []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && name != secretName {
			newNames = append(newNames, name)
		}
	}

	newRegistry := strings.Join(newNames, "\n")
	return runWithTimeout(func() error {
		return keyringSetFn(p.serviceName, registryKey, newRegistry)
	})
}

// StoreWithRegistry stores a secret and updates the registry
func (p *KeyringProvider) StoreWithRegistry(ctx context.Context, ref Ref, value string) error {
	// Store the secret
	if err := p.Store(ctx, ref, value); err != nil {
		return err
	}

	// Add to registry
	return p.addToRegistry(ref.Name)
}

// DeleteWithRegistry deletes a secret and updates the registry
func (p *KeyringProvider) DeleteWithRegistry(ctx context.Context, ref Ref) error {
	// Delete the secret
	if err := p.Delete(ctx, ref); err != nil {
		return err
	}

	// Remove from registry
	return p.removeFromRegistry(ref.Name)
}
