package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"go.uber.org/zap"
)

// profilesDirName is the subdirectory of index.bleve that holds per-profile
// indexes: <dataDir>/index.bleve/profiles/<slug>/. Per-profile indexes are kept
// under their own namespace (rather than directly under index.bleve/<slug>/) so
// they never collide with the shared index's internal entries — Bleve creates
// its own `store/` subdirectory and metadata files directly under index.bleve,
// and a sibling dir there could be mistaken for a profile during cleanup.
const profilesDirName = "profiles"

// Manager provides a unified interface for indexing operations.
//
// A root Manager (created via NewManager) owns the shared default index at
// <dataDir>/index.bleve plus a lazily-populated map of per-profile sub-Managers
// (Profiles v2, Spec 057). Each sub-Manager, obtained via ForProfile, wraps its
// own index at <dataDir>/index.bleve/profiles/<slug>/ and reuses the full Manager
// method surface. Sub-Managers do not themselves nest further (their profiles map
// is nil), so ForProfile is only meaningful on the root.
type Manager struct {
	bleveIndex *BleveIndex
	mu         sync.RWMutex
	logger     *zap.Logger

	dataDir  string              // root data dir; "" for sub-Managers
	profiles map[string]*Manager // slug -> per-profile sub-Manager (root only)
}

// profilesBaseDir returns <dataDir>/index.bleve/profiles, the parent directory
// of all per-profile indexes.
func (m *Manager) profilesBaseDir() string {
	return filepath.Join(m.dataDir, "index.bleve", profilesDirName)
}

// profileIndexPath returns the on-disk index path for a profile slug.
func (m *Manager) profileIndexPath(slug string) string {
	return filepath.Join(m.profilesBaseDir(), slug)
}

// NewManager creates a new root index manager backed by the shared default index.
func NewManager(dataDir string, logger *zap.Logger) (*Manager, error) {
	bleveIndex, err := NewBleveIndex(dataDir, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bleve index: %w", err)
	}

	return &Manager{
		bleveIndex: bleveIndex,
		logger:     logger,
		dataDir:    dataDir,
		profiles:   make(map[string]*Manager),
	}, nil
}

// Close closes the index manager and every per-profile index it opened.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for slug, sub := range m.profiles {
		if sub.bleveIndex != nil {
			if err := sub.bleveIndex.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(m.profiles, slug)
	}

	if m.bleveIndex != nil {
		if err := m.bleveIndex.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// IndexTool indexes a single tool
func (m *Manager) IndexTool(toolMeta *config.ToolMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.IndexTool(toolMeta)
}

// BatchIndexTools indexes multiple tools efficiently
func (m *Manager) BatchIndexTools(tools []*config.ToolMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.BatchIndex(tools)
}

// SearchTools searches for tools matching the query
func (m *Manager) SearchTools(query string, limit int) ([]*config.SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 20 // default limit
	}

	return m.bleveIndex.SearchTools(query, limit)
}

// Search searches for tools matching the query (alias for SearchTools)
func (m *Manager) Search(query string, limit int) ([]*config.SearchResult, error) {
	return m.SearchTools(query, limit)
}

// DeleteTool removes a tool from the index
func (m *Manager) DeleteTool(serverName, toolName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.DeleteTool(serverName, toolName)
}

// DeleteServerTools removes all tools from a specific server
func (m *Manager) DeleteServerTools(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.DeleteServerTools(serverName)
}

// GetDocumentCount returns the number of indexed documents
func (m *Manager) GetDocumentCount() (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.bleveIndex.GetDocumentCount()
}

// RebuildIndex rebuilds the entire index
func (m *Manager) RebuildIndex() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.RebuildIndex()
}

// GetStats returns indexing statistics
func (m *Manager) GetStats() (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	docCount, err := m.bleveIndex.GetDocumentCount()
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"document_count": docCount,
		"index_type":     "bleve",
		"search_backend": "BM25",
	}

	return stats, nil
}

// GetAllIndexedServerNames returns all unique server names in the index.
func (m *Manager) GetAllIndexedServerNames() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.bleveIndex.GetAllIndexedServerNames()
}

// GetToolsByServer retrieves all tools from a specific server
func (m *Manager) GetToolsByServer(serverName string) ([]*config.ToolMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.bleveIndex.GetToolsByServer(serverName)
}

// ForProfile returns the index Manager scoped to the named profile, backed by a
// physically separate index at <dataDir>/index.bleve/profiles/<slug>/. The
// per-profile index is opened (or created) lazily on first use and cached, so
// repeated calls for the same slug return the same handle. An empty slug returns
// the root (shared default) Manager. The slug must be a valid, filesystem-safe
// profile slug (see config.IsValidProfileSlug) — invalid slugs are rejected to
// prevent path traversal. Only valid on a root Manager.
func (m *Manager) ForProfile(slug string) (*Manager, error) {
	if slug == "" {
		return m, nil
	}
	if m.profiles == nil {
		return nil, fmt.Errorf("ForProfile is only valid on the root index manager")
	}
	if !config.IsValidProfileSlug(slug) {
		return nil, fmt.Errorf("invalid profile slug %q: not filesystem-safe", slug)
	}

	// Fast path: already opened.
	m.mu.RLock()
	if sub, ok := m.profiles[slug]; ok {
		m.mu.RUnlock()
		return sub, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	// Re-check under the write lock in case of a concurrent opener.
	if sub, ok := m.profiles[slug]; ok {
		return sub, nil
	}

	indexPath := m.profileIndexPath(slug)
	bleveIndex, err := newBleveIndexAt(indexPath, m.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to open profile index %q: %w", slug, err)
	}

	sub := &Manager{
		bleveIndex: bleveIndex,
		logger:     m.logger,
		// Sub-Managers carry no dataDir/profiles map: they do not nest further.
	}
	m.profiles[slug] = sub
	m.logger.Info("Opened per-profile index", zap.String("profile", slug), zap.String("path", indexPath))
	return sub, nil
}

// ClearAll removes every document from this index, leaving it empty.
func (m *Manager) ClearAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.bleveIndex.DeleteAll()
}

// RebuildProfileFromShared (re)builds the named profile's index to contain
// exactly the tools of the given servers, sourced from the shared default index.
// The profile index is cleared first, so the operation is idempotent and only
// ever touches the named profile — other profiles are left untouched (reload
// isolation). Servers with no indexed tools contribute nothing; an empty server
// list yields an empty profile index.
func (m *Manager) RebuildProfileFromShared(slug string, servers []string) error {
	pm, err := m.ForProfile(slug)
	if err != nil {
		return err
	}

	if err := pm.ClearAll(); err != nil {
		return fmt.Errorf("failed to clear profile index %q: %w", slug, err)
	}

	var tools []*config.ToolMetadata
	for _, srv := range servers {
		srvTools, err := m.GetToolsByServer(srv)
		if err != nil {
			return fmt.Errorf("failed to read shared index for server %q: %w", srv, err)
		}
		tools = append(tools, srvTools...)
	}

	if len(tools) == 0 {
		return nil
	}
	return pm.BatchIndexTools(tools)
}

// DropProfile closes the named profile's index (if open) and removes its on-disk
// directory. It is a no-op if the profile was never opened and its directory does
// not exist. The empty (default) slug cannot be dropped.
func (m *Manager) DropProfile(slug string) error {
	if slug == "" {
		return fmt.Errorf("cannot drop the shared default index")
	}
	if m.dataDir == "" {
		return fmt.Errorf("DropProfile is only valid on the root index manager")
	}
	if !config.IsValidProfileSlug(slug) {
		return fmt.Errorf("invalid profile slug %q: not filesystem-safe", slug)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if sub, ok := m.profiles[slug]; ok {
		if sub.bleveIndex != nil {
			if err := sub.bleveIndex.Close(); err != nil {
				m.logger.Warn("Failed to close profile index before drop",
					zap.String("profile", slug), zap.Error(err))
			}
		}
		delete(m.profiles, slug)
	}

	dir := m.profileIndexPath(slug)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to remove profile index dir %q: %w", dir, err)
	}
	m.logger.Info("Dropped per-profile index", zap.String("profile", slug), zap.String("path", dir))
	return nil
}

// ExistingProfileDirs returns the slugs of every per-profile index directory
// present on disk under <dataDir>/index.bleve/profiles/, regardless of whether
// it is currently open. Only valid, filesystem-safe slug directories are
// returned. Used to reclaim orphaned profile indexes left by a prior run.
func (m *Manager) ExistingProfileDirs() ([]string, error) {
	if m.dataDir == "" {
		return nil, nil
	}
	base := m.profilesBaseDir()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read index dir %q: %w", base, err)
	}

	var slugs []string
	for _, e := range entries {
		if e.IsDir() && config.IsValidProfileSlug(e.Name()) {
			slugs = append(slugs, e.Name())
		}
	}
	return slugs, nil
}

// ProfileSlugs returns the slugs of all currently open per-profile indexes.
func (m *Manager) ProfileSlugs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	slugs := make([]string, 0, len(m.profiles))
	for slug := range m.profiles {
		slugs = append(slugs, slug)
	}
	return slugs
}
