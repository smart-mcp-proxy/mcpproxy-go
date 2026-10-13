package server

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
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
// "already exists". Since Spec 116 the runtime's commit raises it.
type ServerExistsError = runtime.ServerExistsError

// createServer is the single create-only door for new upstream servers. It is
// one config commit (Spec 116): the existence check runs against the live
// configuration (and any row still stored under the name) inside the commit
// lock, and the live config, storage and the file all receive the server
// before this returns. configCommitMu gives the exclusion the old createMu
// gave. On any error nothing has been written.
func (s *Server) createServer(sc *config.ServerConfig) error {
	if h := s.createAfterConfigRead; h != nil {
		h(sc.Name)
	}
	_, err := s.runtime.Commit("create_server", func(tx *runtime.CommitTx) error {
		return tx.CreateServer(sc)
	})
	if err == nil {
		return nil
	}
	var exists *ServerExistsError
	if errors.As(err, &exists) {
		return exists
	}
	if errors.Is(err, storage.ErrUpstreamExists) {
		return &ServerExistsError{Name: sc.Name}
	}
	return fmt.Errorf("failed to save server: %w", err)
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

// removeServerFromConfig removes a server through one config commit (Spec 116):
// the live config, its storage row and its whole purge set (tool and prompt
// approvals, OAuth tokens and DCR client, scan reports and jobs, integrity
// baseline, call history, tool hashes; PR2), and the file lose it together,
// and the commit purges its tools from the index and its in-memory state. A
// delete followed immediately by an add of the same name therefore always
// succeeds, starts clean (#1562), and no later save can resurrect the server
// (#1559). Unknown names fail with runtime.ErrServerNotFound. It returns what
// the purge deleted.
func (s *Server) removeServerFromConfig(name string) (storage.PurgeReport, error) {
	if s.runtime == nil {
		return storage.PurgeReport{}, fmt.Errorf("runtime not available")
	}
	res, err := s.runtime.Commit("remove_server", func(tx *runtime.CommitTx) error {
		if err := tx.RemoveServer(name); err != nil {
			return err
		}
		// A later add under this name is a NEW admission: forget the
		// informational-scan bookkeeping (claim, known mark, retry budget) so
		// the re-added server gets its baseline scan again. Done inside the
		// commit so an add of the same name cannot be forgotten by a removal
		// that was committed before it. Both removal doors (REST
		// Server.RemoveServer and the MCP commit writer) pass through here.
		s.forgetInformationalScan(name)
		return nil
	})
	if err != nil {
		return storage.PurgeReport{}, err
	}
	rep := storage.PurgeReport{Server: name}
	if res != nil {
		if r, ok := res.Purged[name]; ok {
			rep = r
		}
	}
	return rep, nil
}
