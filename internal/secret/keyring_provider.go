package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
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

// Package-level function variables allow tests to inject a fake/hanging
// keyring without reaching into the real OS keychain.
var (
	keyringGetFn = keyring.Get
	keyringSetFn = keyring.Set
	keyringDelFn = keyring.Delete
)

// KeyringProvider resolves secrets from OS keyring (Keychain, Secret Service, WinCred)
type KeyringProvider struct {
	serviceName string
}

// NewKeyringProvider creates a new keyring provider
func NewKeyringProvider() *KeyringProvider {
	return &KeyringProvider{
		serviceName: ServiceName,
	}
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

// Store saves a secret to the OS keyring and updates the registry
func (p *KeyringProvider) Store(_ context.Context, ref Ref, value string) error {
	if !p.CanResolve(ref.Type) {
		return fmt.Errorf("keyring provider cannot store secret type: %s", ref.Type)
	}

	err := runWithTimeout(func() error {
		return keyringSetFn(p.serviceName, ref.Name, value)
	})
	if err != nil {
		return fmt.Errorf("failed to store secret %s in keyring: %w", ref.Name, err)
	}

	// Add to registry so it appears in list
	if err := p.addToRegistry(ref.Name); err != nil {
		return fmt.Errorf("failed to update secret registry: %w", err)
	}

	return nil
}

// Delete removes a secret from the OS keyring and updates the registry
func (p *KeyringProvider) Delete(_ context.Context, ref Ref) error {
	if !p.CanResolve(ref.Type) {
		return fmt.Errorf("keyring provider cannot delete secret type: %s", ref.Type)
	}

	err := runWithTimeout(func() error {
		return keyringDelFn(p.serviceName, ref.Name)
	})
	if err != nil {
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

// IsAvailable checks if the keyring is available on the current system.
//
// This is deliberately conservative and NEVER calls keyring.Set as a probe:
// on macOS, Set triggers the "Keychain Not Found" system modal when the
// user's default keychain is missing/locked/corrupted, whose default button
// is the destructive "Reset To Defaults" action. That used to block the
// server process on every scanner-configure call.
//
// Instead, we:
//  1. Skip the probe entirely in obvious headless/CI environments.
//  2. Do a single read-only Get for a non-existent key and treat
//     ErrNotFound as "available, no secret yet". Any other error (including
//     ErrUnsupportedPlatform and backend communication failures) is treated
//     as "unavailable".
//  3. Run the Get inside a goroutine with a 2s hard timeout so a hung
//     keychain backend cannot stall the caller.
func (p *KeyringProvider) IsAvailable() bool {
	// Heuristic fast-path: if we're clearly running headless (CI, no
	// X display on Linux, etc.) don't even try the keychain — it will
	// either fail or prompt.
	if isHeadlessEnvironment() {
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
		return r.ok
	case <-time.After(keyringProbeTimeout):
		// The keychain is wedged. Bail out — the caller should fall back
		// to in-memory / config-only secret storage.
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
