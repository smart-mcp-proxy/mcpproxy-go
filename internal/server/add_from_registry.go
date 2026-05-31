package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// Keystone of spec 070: a single backend op that turns a registry *reference*
// (registryID + serverID + optional overrides) into a validated, quarantined
// upstream server. Every surface (REST, MCP, CLI) funnels through here so the
// registry-result → config.ServerConfig normalization lives in exactly one
// place (CN-001) and identical input yields an identical persisted config
// (CN-004). The client never sends a config blob — the server re-derives it
// (security decision D1), so a compromised/buggy client cannot smuggle in
// arbitrary command/args or disable quarantine.

// Stable error codes shared across surfaces. Surfaces translate these to their
// own envelopes (HTTP 400/404, MCP structured error, CLI message) via
// RegistryAddErrorCode so the same failure reads the same way everywhere.
var (
	// ErrNoInstallInfo means the registry entry had neither an install command
	// nor a URL, so there is nothing runnable to persist.
	ErrNoInstallInfo = errors.New("no_install_info")
	// ErrDuplicateName means an upstream server with the target name already exists.
	ErrDuplicateName = errors.New("duplicate_name")
)

// MissingRequiredInputError is returned when the registry entry declares
// required inputs that the request did not supply. It carries the missing
// names so surfaces can tell the user exactly what to provide.
type MissingRequiredInputError struct {
	Names []string
}

func (e *MissingRequiredInputError) Error() string {
	return "missing_required_input: " + strings.Join(e.Names, ", ")
}

// AddFromRegistryRequest is the reference-based input to the keystone op.
type AddFromRegistryRequest struct {
	RegistryID string            // required — must resolve via registries.FindRegistry
	ServerID   string            // required — resolved via registries.FindServerByID
	Name       string            // optional override; defaults to the entry's name/id
	Env        map[string]string // optional; satisfies declared RequiredInputs
	Enabled    *bool             // optional; defaults to true when nil
}

// RegistryAddErrorCode maps an error returned by AddServerFromRegistry (or the
// pure derivation) to its stable cross-surface code, or "" if it is not one of
// the recognized add-from-registry failures.
func RegistryAddErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, registries.ErrRegistryNotFound):
		return "registry_not_found"
	case errors.Is(err, registries.ErrServerNotFound):
		return "server_not_found"
	case errors.Is(err, ErrNoInstallInfo):
		return "no_install_info"
	case errors.Is(err, ErrDuplicateName):
		return "duplicate_name"
	}
	var missing *MissingRequiredInputError
	if errors.As(err, &missing) {
		return "missing_required_input"
	}
	return ""
}

// AddServerFromRegistry resolves the referenced registry server, re-derives a
// validated config.ServerConfig server-side, and persists it quarantined.
func (s *Server) AddServerFromRegistry(ctx context.Context, req *AddFromRegistryRequest) (*config.ServerConfig, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}

	// Shared resolution path (CN-001): same lookup for every surface. Returns
	// registries.ErrRegistryNotFound / ErrServerNotFound, which propagate as
	// stable codes. A nil guesser is fine — entries carry their own install
	// command/URL; repository guessing is a search-time enrichment.
	entry, err := registries.FindServerByID(ctx, req.RegistryID, req.ServerID, nil)
	if err != nil {
		return nil, err
	}

	// Quarantine default comes from global config — never from the request
	// (CN-002). Fall back to quarantining when config is unavailable (safe default).
	quarantineDefault := true
	if cfg := s.runtime.Config(); cfg != nil {
		quarantineDefault = cfg.DefaultQuarantineForNewServer()
	}

	serverCfg, err := buildServerConfigFromEntry(entry, req, quarantineDefault)
	if err != nil {
		return nil, err
	}

	// Persist via the shared add path (duplicate check + storage + runtime sync).
	if err := s.AddServer(ctx, serverCfg); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateName, serverCfg.Name)
		}
		return nil, err
	}

	return serverCfg, nil
}

// buildServerConfigFromEntry is the pure derivation core: registry entry +
// request overrides + the proxy's quarantine default → a validated
// config.ServerConfig. No network, no storage — fully unit-testable.
func buildServerConfigFromEntry(entry *registries.ServerEntry, req *AddFromRegistryRequest, quarantineDefault bool) (*config.ServerConfig, error) {
	if entry == nil {
		return nil, ErrNoInstallInfo
	}
	if req == nil {
		req = &AddFromRegistryRequest{}
	}

	// Refuse before persisting anything if declared inputs are unmet (lists names).
	if missing := missingRequiredInputs(entry, req.Env); len(missing) > 0 {
		return nil, &MissingRequiredInputError{Names: missing}
	}

	name := req.Name
	if name == "" {
		name = entry.Name
	}
	if name == "" {
		name = entry.ID
	}

	cfg := &config.ServerConfig{
		Name:        name,
		Quarantined: quarantineDefault, // CN-002: never overridable to false here
		Enabled:     true,
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}

	// Carry any supplied env (overrides + required-input values).
	if len(req.Env) > 0 {
		cfg.Env = make(map[string]string, len(req.Env))
		for k, v := range req.Env {
			cfg.Env[k] = v
		}
	}

	// Derive transport: prefer a stdio install command, else an http/remote URL.
	installCmd := resolveInstallCmd(entry)
	switch {
	case installCmd != "":
		command, args := parseInstallCommand(installCmd)
		if command == "" {
			return nil, ErrNoInstallInfo
		}
		cfg.Protocol = "stdio"
		cfg.Command = command
		cfg.Args = args
	case entry.URL != "":
		cfg.Protocol = "http"
		cfg.URL = entry.URL
	case entry.ConnectURL != "":
		cfg.Protocol = "http"
		cfg.URL = entry.ConnectURL
	default:
		return nil, ErrNoInstallInfo
	}

	return cfg, nil
}

// resolveInstallCmd returns the entry's install command, falling back to a
// repository-info-derived npm install command when the entry itself has none.
func resolveInstallCmd(entry *registries.ServerEntry) string {
	if entry.InstallCmd != "" {
		return entry.InstallCmd
	}
	if entry.RepositoryInfo != nil && entry.RepositoryInfo.NPM != nil && entry.RepositoryInfo.NPM.Exists {
		return entry.RepositoryInfo.NPM.InstallCmd
	}
	return ""
}

// parseInstallCommand splits an install command into command + args. Whitespace
// split matches the historical client-side behavior but now runs server-side so
// every surface derives identical command/args (CN-001/CN-004).
func parseInstallCommand(installCmd string) (command string, args []string) {
	fields := strings.Fields(installCmd)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

// missingRequiredInputs returns the names of declared/detected required inputs
// that env does not satisfy with a non-empty value.
func missingRequiredInputs(entry *registries.ServerEntry, env map[string]string) []string {
	var missing []string
	for _, in := range registries.DetectRequiredInputs(entry) {
		if v, ok := env[in.Name]; !ok || v == "" {
			missing = append(missing, in.Name)
		}
	}
	return missing
}
