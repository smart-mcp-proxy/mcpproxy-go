package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

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
	// ErrRegistrySourceUnusable means the URL answered but serves no MCP server
	// list (GH #783): a 404, an HTML page, or JSON with nothing server-shaped in
	// it. Refusing the add here is the whole point — the alternative is a
	// registry that persists fine and then fails every search.
	ErrRegistrySourceUnusable = errors.New("registry_source_unusable")
)

// probeRegistrySource is a seam for tests; production always probes the live URL.
var probeRegistrySource = registries.ProbeRegistrySource

// registryProbeTimeout bounds the add-time probe (it may try two candidate URLs,
// each with the registry client's own retries).
const registryProbeTimeout = 30 * time.Second

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
	case errors.Is(err, ErrRegistrySourceUnusable):
		return "registry_source_unusable"
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

	// Verify the source really is a v0.1 registry before persisting it (GH #783).
	//
	// This is deliberately NOT skippable by passing a protocol. An earlier cut
	// treated an explicit protocol as a user override that bypassed the probe —
	// which would have made the whole fix inert on the surface the bug was
	// reported from: the Web UI and the macOS tray both always send
	// "modelcontextprotocol/registry" (it is their only option), so the check
	// would never have run there. There is exactly one supported protocol; naming
	// it does not make an arbitrary URL speak it.
	if err := s.resolveRegistrySourceShape(&entry); err != nil {
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

// resolveRegistrySourceShape probes the live source and rewrites the entry's
// ServersURL/Protocol with what was actually found there, so MCPProxy speaks the
// protocol the source implements instead of assuming the official one.
//
// The two failure modes are deliberately NOT symmetric:
//   - the source answered but is not a server list → refuse the add, with the
//     status/reason the user would otherwise have met later as an opaque search
//     error (this is the #783 complaint);
//   - the source could not be reached at all → keep the offline-derived entry and
//     add it anyway, so a transient network failure never blocks a valid registry.
func (s *Server) resolveRegistrySourceShape(entry *config.RegistryEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), registryProbeTimeout)
	defer cancel()

	probe, err := probeRegistrySource(ctx, entry.URL)
	if err != nil {
		if errors.Is(err, registries.ErrRegistrySourceUnreachable) {
			s.logger.Warn("Could not probe registry source; adding it with the derived defaults",
				zap.String("url", entry.URL), zap.Error(err))
			return nil
		}
		return fmt.Errorf("%w: %v", ErrRegistrySourceUnusable, err)
	}

	entry.ServersURL = probe.ServersURL
	entry.Protocol = probe.Protocol
	return nil
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

	// MCPProxy implements exactly one registry protocol. Naming a different one is
	// a mistake we should report, not silently accept and then fail to parse.
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = defaultRegistryProtocol
	}
	if protocol != defaultRegistryProtocol {
		return config.RegistryEntry{}, fmt.Errorf("%w: unsupported registry protocol %q (only %q is supported)",
			ErrInvalidRegistryURL, protocol, defaultRegistryProtocol)
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
	// A former-default id (pulse, fleur, …) is pruned on load and skipped by the
	// merge, so a source added under one would persist and then never appear in
	// the registry list. Refuse it with a clear reason instead of accepting a
	// silent no-op; any other id for the same URL works.
	if config.IsDeprecatedDefaultRegistry(strings.ToLower(entry.ID)) {
		return fmt.Errorf("%w: %q is a retired built-in registry id — add this source under a different id", ErrRegistryShadowsBuiltin, entry.ID)
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

// deriveServersURL is the OFFLINE fallback for the servers-collection URL, used
// when the add-time probe could not reach the source. Only a bare base URL (no
// path) is pointed at the official v0.1 collection; a URL that carries a path is
// used VERBATIM.
//
// GH discussion #783: this used to append "/v0.1/servers" to anything without
// "/servers" in it, so a pasted static document
// (…/app-registry/…/apps.json) was fetched as "…/apps.json/v0.1/servers" and
// 404'd. MCPProxy must never invent routes on a URL the user gave it — the
// official route only exists on registries that implement the official API, and
// ProbeRegistrySource is what establishes that.
func deriveServersURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if strings.Trim(u.Path, "/") != "" {
		return rawURL
	}
	u.Path = "/v0.1/servers"
	return u.String()
}
