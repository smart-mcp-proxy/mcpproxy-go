package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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
	updated.Servers = append(append([]*config.ServerConfig(nil), current.Servers...), sc)
	return &updated
}
