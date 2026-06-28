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

// MCP-866: `registry add-source` adds a user-supplied generic MCP registry
// (any https endpoint implementing the official modelcontextprotocol/registry
// v0.1 protocol). The added source is tagged "custom" (informational only since
// MCP-1072 — servers it yields follow the global quarantine default like any
// other). Like the keystone add op, the derivation lives server-side so every
// surface (CLI, REST, MCP) produces an identical persisted config entry.

const defaultRegistryProtocol = "modelcontextprotocol/registry"

// Stable cross-surface error codes for add-source failures.
var (
	// ErrInvalidRegistryURL means the supplied source URL was not a valid https URL.
	ErrInvalidRegistryURL = errors.New("invalid_registry_url")
	// ErrRegistriesLocked means RegistriesLocked is set and runtime additions are refused.
	ErrRegistriesLocked = errors.New("registries_locked")
	// ErrRegistryShadowsBuiltin means the requested id collides with a shipped default.
	ErrRegistryShadowsBuiltin = errors.New("registry_shadows_builtin")
	// ErrDuplicateRegistry means a custom registry with that id already exists.
	ErrDuplicateRegistry = errors.New("duplicate_registry")
)

// AddRegistrySourceRequest is the input to the add-source op. Provenance is NOT
// part of the request — it is always "custom".
type AddRegistrySourceRequest struct {
	URL      string // required https URL of the registry (base or /v0.1/servers endpoint)
	Protocol string // optional; defaults to modelcontextprotocol/registry
	ID       string // optional; derived from the host when empty
	Name     string // optional; defaults to the id
}

// AddRegistrySourceErrorCode maps an add-source error to its stable cross-surface code.
func AddRegistrySourceErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalidRegistryURL):
		return "invalid_registry_url"
	case errors.Is(err, ErrRegistriesLocked):
		return "registries_locked"
	case errors.Is(err, ErrRegistryShadowsBuiltin):
		return "registry_shadows_builtin"
	case errors.Is(err, ErrDuplicateRegistry):
		return "duplicate_registry"
	}
	return ""
}

// AddRegistrySource validates the request, derives a custom/unverified registry
// entry, and persists it into cfg.Registries via copy-on-write UpdateConfig
// (see the runtime-config-snapshot-cow rule). Returns the persisted entry.
func (s *Server) AddRegistrySource(req *AddRegistrySourceRequest) (*config.RegistryEntry, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}

	entry, err := buildRegistrySourceEntry(req.URL, req.Protocol, req.ID, req.Name)
	if err != nil {
		return nil, err
	}

	currentConfig := s.runtime.Config()
	if currentConfig == nil {
		return nil, errors.New("configuration unavailable")
	}
	if err := validateNewRegistrySource(currentConfig, entry); err != nil {
		return nil, err
	}

	// Copy-on-write: clone the config and its registries slice, append to the
	// clone, then publish atomically. runtime.Config() is a shared immutable
	// snapshot background readers may be ranging over.
	updatedConfig := *currentConfig
	updatedConfig.Registries = append(append([]config.RegistryEntry(nil), currentConfig.Registries...), entry)
	s.runtime.UpdateConfig(&updatedConfig, "")

	// Rebuild the effective catalog so the new source is immediately searchable.
	registries.SetRegistriesFromConfig(&updatedConfig)

	if err := s.SaveConfiguration(); err != nil {
		s.logger.Warn("Failed to save configuration after adding registry source",
			zap.String("registry_id", entry.ID), zap.Error(err))
	}

	s.logger.Info("Added custom registry source",
		zap.String("registry_id", entry.ID),
		zap.String("url", entry.URL),
		zap.String("provenance", entry.Provenance))

	return &entry, nil
}

// AddRegistrySourceRef is the surface-facing adapter over AddRegistrySource. On
// failure it projects the typed error onto the stable cross-surface
// contracts.RegistryAddError so REST/MCP/CLI report the same code.
func (s *Server) AddRegistrySourceRef(rawURL, protocol, id, name string) (*config.RegistryEntry, *contracts.RegistryAddError, error) {
	entry, err := s.AddRegistrySource(&AddRegistrySourceRequest{URL: rawURL, Protocol: protocol, ID: id, Name: name})
	if err != nil {
		return nil, &contracts.RegistryAddError{Code: AddRegistrySourceErrorCode(err), Message: err.Error()}, err
	}
	return entry, nil, nil
}

// buildRegistrySourceEntry is the pure derivation core: a raw URL + optional
// overrides → a validated custom/unverified config.RegistryEntry. No network,
// no storage — fully unit-testable.
func buildRegistrySourceEntry(rawURL, protocol, id, name string) (config.RegistryEntry, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return config.RegistryEntry{}, fmt.Errorf("%w: %q (must be an https URL)", ErrInvalidRegistryURL, rawURL)
	}
	// SSRF fail-fast (CWE-918): refuse a literal-IP source pointed at an
	// internal/non-routable range up front. Hostname sources are guarded
	// authoritatively at fetch/dial time (registries.registryDialControl). No
	// DNS here, so this core stays pure and offline.
	if err := registries.ValidateRegistrySourceURL(rawURL); err != nil {
		return config.RegistryEntry{}, fmt.Errorf("%w: %v", ErrInvalidRegistryURL, err)
	}

	if protocol == "" {
		protocol = defaultRegistryProtocol
	}
	if id == "" {
		id = deriveRegistryID(u.Host)
	}
	if name == "" {
		name = id
	}

	return config.RegistryEntry{
		ID:          id,
		Name:        name,
		Description: "User-added registry (custom)",
		URL:         rawURL,
		ServersURL:  deriveServersURL(rawURL),
		Protocol:    protocol,
		Provenance:  config.RegistryProvenanceCustom,
	}, nil
}

// validateNewRegistrySource enforces the add-source guardrails against the
// current config: locked registries, shadowing a shipped default, and
// duplicate custom ids.
func validateNewRegistrySource(cfg *config.Config, entry config.RegistryEntry) error {
	if cfg != nil && cfg.RegistriesLocked {
		return fmt.Errorf("%w: registry additions are disabled by policy", ErrRegistriesLocked)
	}
	for _, d := range config.DefaultRegistries() {
		if strings.EqualFold(d.ID, entry.ID) {
			return fmt.Errorf("%w: %q is a built-in registry and cannot be replaced", ErrRegistryShadowsBuiltin, entry.ID)
		}
	}
	if cfg != nil {
		for i := range cfg.Registries {
			if strings.EqualFold(cfg.Registries[i].ID, entry.ID) {
				return fmt.Errorf("%w: a registry with id %q already exists", ErrDuplicateRegistry, entry.ID)
			}
		}
	}
	return nil
}

// deriveRegistryID slugifies a host into a stable registry id, dropping a
// leading "www." and a trailing port, and replacing non-alphanumerics with "-".
func deriveRegistryID(host string) string {
	host = strings.ToLower(host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	host = strings.TrimPrefix(host, "www.")
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// deriveServersURL points a base registry URL at its v0.1 servers collection.
// A URL that already targets a servers collection is used verbatim so callers
// can paste either the base URL or the full endpoint.
func deriveServersURL(rawURL string) string {
	if strings.Contains(rawURL, "/servers") {
		return rawURL
	}
	return strings.TrimRight(rawURL, "/") + "/v0.1/servers"
}
