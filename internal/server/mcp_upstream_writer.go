package server

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// upstreamWriter is how the MCP `upstream_servers` and `quarantine_security`
// handlers change the server set. In the daemon it is the runtime's config
// commit (Spec 116): every write reads the live entry inside the commit lock,
// and the live config, storage, the file and the index move together.
type upstreamWriter interface {
	createServer(sc *config.ServerConfig) error
	// updateServer runs fn on a copy of the live entry inside the commit;
	// an error from fn aborts with nothing written. It returns the server's
	// committed generation, or 0 when the commit changed nothing for it (the
	// running instance already is the right one) or the writer has none.
	updateServer(name string, fn func(cur *config.ServerConfig) error) (int64, error)
	removeServer(name string) error
	quarantineServer(name string) error
}

// errUpstreamNotFound is returned by an upstreamWriter for an unknown name.
var errUpstreamNotFound = runtime.ErrServerNotFound

// bareUpstreamWriterFactory builds a writer for an MCPProxyServer that has no
// runtime (a bare proxy built by package tests). It is nil in production,
// where every proxy belongs to a *Server.
var bareUpstreamWriterFactory func(p *MCPProxyServer) upstreamWriter

// writer returns the proxy's upstreamWriter, or nil when it has none.
func (p *MCPProxyServer) writer() upstreamWriter {
	if p.mainServer != nil && p.mainServer.runtime != nil {
		return commitUpstreamWriter{s: p.mainServer}
	}
	if bareUpstreamWriterFactory != nil {
		return bareUpstreamWriterFactory(p)
	}
	return nil
}

var errNoUpstreamWriter = errors.New("server management is not available without the runtime")

// commitUpstreamWriter is the production writer: one runtime.Commit per call.
type commitUpstreamWriter struct{ s *Server }

func (w commitUpstreamWriter) createServer(sc *config.ServerConfig) error {
	return w.s.createServer(sc)
}

func (w commitUpstreamWriter) updateServer(name string, fn func(cur *config.ServerConfig) error) (int64, error) {
	res, err := w.s.runtime.Commit("mcp_update_server", func(tx *runtime.CommitTx) error {
		return tx.UpdateServer(name, fn)
	})
	if err != nil || res == nil {
		return 0, err
	}
	if ch, ok := res.Changes[name]; ok {
		return ch.New.Gen, nil
	}
	return 0, nil
}

func (w commitUpstreamWriter) removeServer(name string) error {
	return w.s.removeServerFromConfig(name)
}

func (w commitUpstreamWriter) quarantineServer(name string) error {
	// The runtime's QuarantineServer is the commit (SetQuarantined) plus the
	// review events and the activity record: the MCP door gets exactly what
	// the REST door gets, including the index purge (B1, #1061).
	if err := w.s.runtime.QuarantineServer(name, true); err != nil {
		if errors.Is(err, runtime.ErrServerNotFound) {
			return errUpstreamNotFound
		}
		return err
	}
	return nil
}

// mcpUserError carries a message the MCP handler returns to the caller
// verbatim (validation of the request against the live entry).
type mcpUserError struct{ msg string }

func (e *mcpUserError) Error() string { return e.msg }

func newMCPUserError(format string, args ...interface{}) error {
	return &mcpUserError{msg: fmt.Sprintf(format, args...)}
}
