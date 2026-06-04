package server

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// MCP-1072: `registry edit` updates a user-added custom registry source (name,
// url, servers-url). It is the sibling of add-source/remove-source and shares
// their guards: built-in registries are refused via the same
// registry_shadows_builtin guard, an unknown id yields registry_not_found, and a
// RegistriesLocked policy freezes the set. The derivation lives server-side so
// every surface (CLI, REST) produces an identical persisted config.

// EditRegistrySourceRequest carries partial updates to a custom registry.
// Empty string fields mean "leave unchanged".
type EditRegistrySourceRequest struct {
	ID         string // required — the id of the custom registry to edit
	Name       string // optional new display name
	URL        string // optional new base/servers https URL
	ServersURL string // optional explicit servers-collection URL
}

// EditRegistrySourceErrorCode maps an edit-source error to its stable
// cross-surface code (shared HTTP status mapping with add/remove-source).
func EditRegistrySourceErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrRegistryNotFound):
		return "registry_not_found"
	case errors.Is(err, ErrRegistriesLocked):
		return "registries_locked"
	case errors.Is(err, ErrRegistryShadowsBuiltin):
		return "registry_shadows_builtin"
	case errors.Is(err, ErrInvalidRegistryURL):
		return "invalid_registry_url"
	}
	return ""
}

// EditRegistrySource applies the requested updates to a user-added custom
// registry by id and persists the change via copy-on-write UpdateConfig (see the
// runtime-config-snapshot-cow rule). Returns the updated entry.
func (s *Server) EditRegistrySource(req *EditRegistrySourceRequest) (*config.RegistryEntry, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}

	currentConfig := s.runtime.Config()
	if currentConfig == nil {
		return nil, errors.New("configuration unavailable")
	}

	updated, remaining, err := editRegistrySourceInConfig(currentConfig, req)
	if err != nil {
		return nil, err
	}

	// Copy-on-write: clone the config and publish a fresh registries slice so
	// background readers ranging over the shared snapshot are never disturbed.
	updatedConfig := *currentConfig
	updatedConfig.Registries = remaining
	s.runtime.UpdateConfig(&updatedConfig, "")

	// Rebuild the effective catalog so the edit takes effect immediately.
	registries.SetRegistriesFromConfig(&updatedConfig)

	if err := s.SaveConfiguration(); err != nil {
		s.logger.Warn("Failed to save configuration after editing registry source",
			zap.String("registry_id", updated.ID), zap.Error(err))
	}

	s.logger.Info("Edited custom registry source",
		zap.String("registry_id", updated.ID),
		zap.String("url", updated.URL))

	return &updated, nil
}

// EditRegistrySourceRef is the surface-facing adapter over EditRegistrySource.
// On failure it projects the typed error onto the stable cross-surface
// contracts.RegistryAddError so REST/CLI report the same code.
func (s *Server) EditRegistrySourceRef(id, name, rawURL, serversURL string) (*config.RegistryEntry, *contracts.RegistryAddError, error) {
	entry, err := s.EditRegistrySource(&EditRegistrySourceRequest{ID: id, Name: name, URL: rawURL, ServersURL: serversURL})
	if err != nil {
		return nil, &contracts.RegistryAddError{Code: EditRegistrySourceErrorCode(err), Message: err.Error()}, err
	}
	return entry, nil, nil
}

// editRegistrySourceInConfig is the pure edit core: given the current config, a
// target id, and partial updates, it validates the edit (registries unlocked,
// not a built-in, the id exists as a custom entry, any new URL is https) and
// returns the updated entry plus a fresh registries slice with the entry
// replaced. No network, no storage — fully unit-testable.
func editRegistrySourceInConfig(cfg *config.Config, req *EditRegistrySourceRequest) (config.RegistryEntry, []config.RegistryEntry, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return config.RegistryEntry{}, nil, fmt.Errorf("%w: empty registry id", ErrRegistryNotFound)
	}
	if cfg != nil && cfg.RegistriesLocked {
		return config.RegistryEntry{}, nil, fmt.Errorf("%w: registry edits are disabled by policy", ErrRegistriesLocked)
	}
	// Refuse built-ins via the same shadow guard add/remove-source use — a
	// shipped default is not user-owned and cannot be edited.
	for _, d := range config.DefaultRegistries() {
		if strings.EqualFold(d.ID, id) {
			return config.RegistryEntry{}, nil, fmt.Errorf("%w: %q is a built-in registry and cannot be edited", ErrRegistryShadowsBuiltin, id)
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

	// Copy-on-write clone, then mutate the clone's entry only.
	remaining := append([]config.RegistryEntry(nil), cfg.Registries...)
	updated := remaining[idx]

	if name := strings.TrimSpace(req.Name); name != "" {
		updated.Name = name
	}
	if rawURL := strings.TrimSpace(req.URL); rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return config.RegistryEntry{}, nil, fmt.Errorf("%w: %q (must be an https URL)", ErrInvalidRegistryURL, rawURL)
		}
		updated.URL = rawURL
		// Re-derive the servers URL from the new base unless the caller also
		// supplied an explicit one below.
		if strings.TrimSpace(req.ServersURL) == "" {
			updated.ServersURL = deriveServersURL(rawURL)
		}
	}
	if serversURL := strings.TrimSpace(req.ServersURL); serversURL != "" {
		updated.ServersURL = serversURL
	}
	// A custom registry stays "custom" — the merge recomputes provenance from the
	// id anyway, but keep it consistent here too.
	updated.Provenance = config.RegistryProvenanceCustom

	remaining[idx] = updated
	return updated, remaining, nil
}
