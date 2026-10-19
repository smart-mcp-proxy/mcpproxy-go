package server

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A bare MCPProxyServer (no *Server, no runtime) exists only in this package's
// tests. It has no config commit, so its upstream_servers writes go straight
// to storage and the manager, as every write did before Spec 116. Production
// proxies always belong to a *Server and use the commit (mcp_upstream_writer.go).
func init() {
	bareUpstreamWriterFactory = func(p *MCPProxyServer) upstreamWriter {
		return bareStorageWriter{p: p}
	}
}

type bareStorageWriter struct{ p *MCPProxyServer }

func (w bareStorageWriter) find(name string) (*config.ServerConfig, error) {
	sc, err := w.p.storage.GetUpstreamServer(name)
	if err != nil || sc == nil {
		return nil, errUpstreamNotFound
	}
	return sc, nil
}

func (w bareStorageWriter) createServer(sc *config.ServerConfig) error {
	if err := w.p.storage.CreateUpstreamServer(sc); err != nil {
		if errors.Is(err, storage.ErrUpstreamExists) {
			return &ServerExistsError{Name: sc.Name}
		}
		return err
	}
	return nil
}

func (w bareStorageWriter) updateServer(name string, fn func(cur *config.ServerConfig) error) (int64, error) {
	cur, err := w.find(name)
	if err != nil {
		return 0, err
	}
	if err := fn(cur); err != nil {
		return 0, err
	}
	if err := w.p.storage.UpdateUpstream(name, cur); err != nil {
		return 0, err
	}
	w.p.upstreamManager.RemoveServer(name)
	if cur.Enabled {
		_ = w.p.upstreamManager.AddServer(name, cur)
	}
	return 0, nil
}

func (w bareStorageWriter) removeServer(name string) (storage.PurgeReport, error) {
	if _, err := w.find(name); err != nil {
		return storage.PurgeReport{}, err
	}
	if err := w.p.storage.RemoveUpstream(name); err != nil {
		return storage.PurgeReport{}, err
	}
	w.p.upstreamManager.RemoveServer(name)
	if w.p.index != nil {
		_ = w.p.index.DeleteServerTools(name)
	}
	return storage.PurgeReport{Server: name}, nil
}

func (w bareStorageWriter) quarantineServer(name string) error {
	cur, err := w.find(name)
	if err != nil {
		return err
	}
	cur.Quarantined = true
	return w.p.storage.UpdateUpstream(name, cur)
}
