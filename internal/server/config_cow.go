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
// createServerAfterSnapshotHook is a test seam fired between reading the
// config snapshot and publishing the clone.
var createServerAfterSnapshotHook func()

func (s *Server) createServer(sc *config.ServerConfig) error {
	s.createMu.Lock()
	defer s.createMu.Unlock()

	// The existence check, storage create, snapshot read and publish all run
	// inside one runtime config-commit so a concurrent config apply cannot be
	// reverted by this add's clone (UX-01 review r1).
	return s.runtime.UpdateConfigFrom(func(cfg *config.Config) (*config.Config, error) {
		if cfg != nil {
			for _, existing := range cfg.Servers {
				if existing != nil && existing.Name == sc.Name {
					return nil, &ServerExistsError{Name: sc.Name}
				}
			}
		}
		if err := s.runtime.StorageManager().CreateUpstreamServer(sc); err != nil {
			if errors.Is(err, storage.ErrUpstreamExists) {
				return nil, &ServerExistsError{Name: sc.Name}
			}
			return nil, fmt.Errorf("failed to save server to storage: %w", err)
		}
		// cfg is the live immutable snapshot; copy-on-write, see
		// configWithAppendedServer.
		if createServerAfterSnapshotHook != nil {
			createServerAfterSnapshotHook()
		}
		return configWithAppendedServer(cfg, sc), nil
	})
}
