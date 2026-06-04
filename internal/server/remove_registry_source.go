package server

import (
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// MCP-1057: `registry remove` deletes a user-added custom registry source. It is
// the inverse of add-source (see add_registry_source.go) and shares its guards:
// built-in registries are refused via the same registry_shadows_builtin guard,
// and a RegistriesLocked policy freezes the set (removals refused too). The
// derivation lives server-side so every surface (CLI, REST) produces an
// identical persisted config.

// ErrRegistryNotFound means no custom registry with the requested id exists.
var ErrRegistryNotFound = errors.New("registry_not_found")

// RemoveRegistrySourceErrorCode maps a remove-source error to its stable
// cross-surface code (shared HTTP status mapping with add-source).
func RemoveRegistrySourceErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrRegistryNotFound):
		return "registry_not_found"
	case errors.Is(err, ErrRegistriesLocked):
		return "registries_locked"
	case errors.Is(err, ErrRegistryShadowsBuiltin):
		return "registry_shadows_builtin"
	}
	return ""
}

// RemoveRegistrySource removes a user-added custom registry by id and persists
// the change via copy-on-write UpdateConfig (see the runtime-config-snapshot-cow
// rule). Returns the removed entry.
func (s *Server) RemoveRegistrySource(id string) (*config.RegistryEntry, error) {
	currentConfig := s.runtime.Config()
	if currentConfig == nil {
		return nil, errors.New("configuration unavailable")
	}

	removed, remaining, err := removeRegistrySourceFromConfig(currentConfig, id)
	if err != nil {
		return nil, err
	}

	// Copy-on-write: clone the config and publish a fresh registries slice so
	// background readers ranging over the shared snapshot are never disturbed.
	updatedConfig := *currentConfig
	updatedConfig.Registries = remaining
	s.runtime.UpdateConfig(&updatedConfig, "")

	// Rebuild the effective catalog so the removed source disappears immediately.
	registries.SetRegistriesFromConfig(&updatedConfig)

	if err := s.SaveConfiguration(); err != nil {
		s.logger.Warn("Failed to save configuration after removing registry source",
			zap.String("registry_id", removed.ID), zap.Error(err))
	}

	s.logger.Info("Removed custom registry source",
		zap.String("registry_id", removed.ID),
		zap.String("url", removed.URL))

	return &removed, nil
}

// RemoveRegistrySourceRef is the surface-facing adapter over RemoveRegistrySource.
// On failure it projects the typed error onto the stable cross-surface
// contracts.RegistryAddError so REST/CLI report the same code.
func (s *Server) RemoveRegistrySourceRef(id string) (*config.RegistryEntry, *contracts.RegistryAddError, error) {
	entry, err := s.RemoveRegistrySource(id)
	if err != nil {
		return nil, &contracts.RegistryAddError{Code: RemoveRegistrySourceErrorCode(err), Message: err.Error()}, err
	}
	return entry, nil, nil
}

// removeRegistrySourceFromConfig is the pure removal core: given the current
// config and a target id, it validates the removal (registries unlocked, not a
// built-in, the id exists as a custom entry) and returns the removed entry plus
// a fresh registries slice with the entry filtered out. No network, no storage —
// fully unit-testable.
func removeRegistrySourceFromConfig(cfg *config.Config, id string) (config.RegistryEntry, []config.RegistryEntry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return config.RegistryEntry{}, nil, fmt.Errorf("%w: empty registry id", ErrRegistryNotFound)
	}
	if cfg != nil && cfg.RegistriesLocked {
		return config.RegistryEntry{}, nil, fmt.Errorf("%w: registry removals are disabled by policy", ErrRegistriesLocked)
	}
	// Refuse built-ins via the same shadow guard add-source uses — a shipped
	// default is not user-owned and cannot be removed.
	for _, d := range config.DefaultRegistries() {
		if strings.EqualFold(d.ID, id) {
			return config.RegistryEntry{}, nil, fmt.Errorf("%w: %q is a built-in registry and cannot be removed", ErrRegistryShadowsBuiltin, id)
		}
	}
	if cfg == nil {
		return config.RegistryEntry{}, nil, fmt.Errorf("%w: no custom registry with id %q", ErrRegistryNotFound, id)
	}
	idx := -1
	for i := range cfg.Registries {
		if strings.EqualFold(cfg.Registries[i].ID, id) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return config.RegistryEntry{}, nil, fmt.Errorf("%w: no custom registry with id %q", ErrRegistryNotFound, id)
	}

	removed := cfg.Registries[idx]
	remaining := make([]config.RegistryEntry, 0, len(cfg.Registries)-1)
	remaining = append(remaining, cfg.Registries[:idx]...)
	remaining = append(remaining, cfg.Registries[idx+1:]...)
	return removed, remaining, nil
}
