package secret

import (
	"context"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	// ServiceName for keyring entries
	ServiceName           = "mcpproxy"
	SecretTypeKeyring     = "keyring"
	RegistryKey           = "_mcpproxy_secret_registry"
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

// Resolve retrieves the secret value from the OS keyring
func (p *KeyringProvider) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	if !p.CanResolve(ref.Type) {
		return "", fmt.Errorf("keyring provider cannot resolve secret type: %s", ref.Type)
	}

	secret, err := keyring.Get(p.serviceName, ref.Name)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s from keyring: %w", ref.Name, err)
	}

	return secret, nil
}

// Store saves a secret to the OS keyring and updates the registry
func (p *KeyringProvider) Store(ctx context.Context, ref SecretRef, value string) error {
	if !p.CanResolve(ref.Type) {
		return fmt.Errorf("keyring provider cannot store secret type: %s", ref.Type)
	}

	err := keyring.Set(p.serviceName, ref.Name, value)
	if err != nil {
		return fmt.Errorf("failed to store secret %s in keyring: %w", ref.Name, err)
	}

	// Add to registry so it appears in list
	err = p.addToRegistry(ref.Name)
	if err != nil {
		return fmt.Errorf("failed to update secret registry: %w", err)
	}

	return nil
}

// Delete removes a secret from the OS keyring and updates the registry
func (p *KeyringProvider) Delete(ctx context.Context, ref SecretRef) error {
	if !p.CanResolve(ref.Type) {
		return fmt.Errorf("keyring provider cannot delete secret type: %s", ref.Type)
	}

	err := keyring.Delete(p.serviceName, ref.Name)
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
func (p *KeyringProvider) List(ctx context.Context) ([]SecretRef, error) {
	// Since go-keyring doesn't provide a list function, we maintain a special
	// registry entry that tracks all our secret names
	registryKey := RegistryKey

	registry, err := keyring.Get(p.serviceName, registryKey)
	if err != nil {
		// Registry doesn't exist yet - return empty list
		return []SecretRef{}, nil
	}

	var refs []SecretRef
	if registry != "" {
		names := strings.Split(registry, "\n")
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				refs = append(refs, SecretRef{
					Type:     "keyring",
					Name:     name,
					Original: fmt.Sprintf("${keyring:%s}", name),
				})
			}
		}
	}

	return refs, nil
}

// IsAvailable checks if the keyring is available on the current system
func (p *KeyringProvider) IsAvailable() bool {
	// Test if keyring is available by trying to access it
	testKey := "_mcpproxy_test_availability"

	// Try to set and get a test value
	err := keyring.Set(p.serviceName, testKey, "test")
	if err != nil {
		return false
	}

	_, err = keyring.Get(p.serviceName, testKey)
	if err != nil {
		return false
	}

	// Clean up test key
	_ = keyring.Delete(p.serviceName, testKey)

	return true
}

// addToRegistry adds a secret name to our internal registry
func (p *KeyringProvider) addToRegistry(secretName string) error {
	registryKey := RegistryKey

	// Get current registry
	registry, err := keyring.Get(p.serviceName, registryKey)
	if err != nil {
		// Registry doesn't exist - create it
		registry = ""
	}

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

	return keyring.Set(p.serviceName, registryKey, registry)
}

// removeFromRegistry removes a secret name from our internal registry
func (p *KeyringProvider) removeFromRegistry(secretName string) error {
	registryKey := RegistryKey

	// Get current registry
	registry, err := keyring.Get(p.serviceName, registryKey)
	if err != nil {
		return nil // Registry doesn't exist - nothing to remove
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
	return keyring.Set(p.serviceName, registryKey, newRegistry)
}

// StoreWithRegistry stores a secret and updates the registry
func (p *KeyringProvider) StoreWithRegistry(ctx context.Context, ref SecretRef, value string) error {
	// Store the secret
	if err := p.Store(ctx, ref, value); err != nil {
		return err
	}

	// Add to registry
	return p.addToRegistry(ref.Name)
}

// DeleteWithRegistry deletes a secret and updates the registry
func (p *KeyringProvider) DeleteWithRegistry(ctx context.Context, ref SecretRef) error {
	// Delete the secret
	if err := p.Delete(ctx, ref); err != nil {
		return err
	}

	// Remove from registry
	return p.removeFromRegistry(ref.Name)
}
