package server

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// configWithAppendedServer returns a NEW *config.Config carrying every server
// current already had plus sc. It never writes through current.
//
// runtime.Config() hands back the PUBLISHED configuration snapshot — the very
// pointer other goroutines read concurrently and lock-free. Appending to
// current.Servers in place writes that shared struct's slice header (and, when
// append has spare capacity, the backing array), while readers are ranging over
// it: a data race under the Go memory model, and a reader can observe a length
// that is already past the element it is about to read.
//
// The set of concurrent readers is not hypothetical and keeps growing: the
// background LoadConfiguredServers / DiscoverAndIndexTools passes, the httpapi
// handlers, and — since the server edition's per-user door — an
// api.AdminServersProvider call on every authenticated request.
//
// Copy-on-write is the convention this package already follows for the config
// snapshot (see add_registry_source.go and addServerInternal); this is the one
// helper both server-append sites share so a third cannot drift back to an
// in-place append. The clone is shallow by design: only the Servers slice is
// being changed, and every *ServerConfig in it is treated as read-only by
// everything that reads a published snapshot.
//
// Returns nil when current is nil, so callers keep their existing nil guard.
func configWithAppendedServer(current *config.Config, sc *config.ServerConfig) *config.Config {
	if current == nil {
		return nil
	}
	updated := *current
	servers := append([]*config.ServerConfig(nil), current.Servers...)
	// Replace a same-name entry instead of appending a second one: two
	// entries under one name make every name-keyed reader ambiguous.
	replaced := false
	for i, existing := range servers {
		if existing != nil && existing.Name == sc.Name {
			servers[i] = sc
			replaced = true
			break
		}
	}
	if !replaced {
		servers = append(servers, sc)
	}
	updated.Servers = servers
	return &updated
}

// ServerExistsError is returned when a create-only add names a server that is
// already configured. Its text is part of the contract: the REST 409 mapping,
// cliclient, `upstream add --if-not-exists` and the registry add all match on
// "already exists".
type ServerExistsError struct{ Name string }

func (e *ServerExistsError) Error() string {
	return fmt.Sprintf("server '%s' already exists", e.Name)
}

// createServer is the single create-only door for new upstream servers. It
// refuses a name that is already present in the runtime config or in storage,
// and publishes the new server to the runtime config, all under one mutex so
// two concurrent adds of one name cannot both succeed. The storage write is
// additionally atomic (one bbolt tx), which covers other writers that do not
// take this mutex. On any error nothing has been published to the runtime
// config.
func (s *Server) createServer(sc *config.ServerConfig) error {
	s.createMu.Lock()
	defer s.createMu.Unlock()

	if cfg := s.runtime.Config(); cfg != nil {
		for _, existing := range cfg.Servers {
			if existing != nil && existing.Name == sc.Name {
				return &ServerExistsError{Name: sc.Name}
			}
		}
	}
	if err := s.runtime.StorageManager().CreateUpstreamServer(sc); err != nil {
		if errors.Is(err, storage.ErrUpstreamExists) {
			return &ServerExistsError{Name: sc.Name}
		}
		return fmt.Errorf("failed to save server to storage: %w", err)
	}
	// runtime.Config() is the live immutable snapshot; copy-on-write, see
	// configWithAppendedServer.
	if updated := configWithAppendedServer(s.runtime.Config(), sc); updated != nil {
		s.runtime.UpdateConfig(updated, "")
	}
	return nil
}

// configWithoutServer returns a NEW *config.Config that omits the named server,
// or nil when current is nil or does not list it. Copy-on-write for the same
// reason as configWithAppendedServer.
func configWithoutServer(current *config.Config, name string) *config.Config {
	if current == nil {
		return nil
	}
	found := false
	servers := make([]*config.ServerConfig, 0, len(current.Servers))
	for _, existing := range current.Servers {
		if existing != nil && existing.Name == name {
			found = true
			continue
		}
		servers = append(servers, existing)
	}
	if !found {
		return nil
	}
	updated := *current
	updated.Servers = servers
	return &updated
}

// removeServerStorage runs removeStorage (the storage deletion of a server) and,
// on success, synchronously drops the server from the runtime config snapshot,
// both under createMu. createServer treats the runtime config as an existence
// source, so without this a delete followed immediately by an add of the same
// name would be refused until the async config sync caught up (and forever when
// no config file path is available). Holding createMu across both steps makes a
// racing create see the server either fully present or fully gone.
func (s *Server) removeServerStorage(name string, removeStorage func() error) error {
	s.createMu.Lock()
	defer s.createMu.Unlock()

	if err := removeStorage(); err != nil {
		return err
	}
	if s.runtime != nil {
		if updated := configWithoutServer(s.runtime.Config(), name); updated != nil {
			s.runtime.UpdateConfig(updated, "")
		}
	}
	return nil
}
