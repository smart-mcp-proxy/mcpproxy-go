package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"go.uber.org/zap"
)

// Registry manages the scanner plugin registry.
//
// Concurrency contract: the *ScannerPlugin records in `scanners` are owned by
// the registry and may only be read or written while holding `mu`. Get and List
// therefore return defensive copies, and every mutation of a stored record goes
// through a locked method on this type (UpdateStatus, SetConfiguredEnv,
// SetRuntimeConfig). Callers must never write to a plugin they got back from
// Get/List and expect the registry to see it — a stored record mutated outside
// the lock races every concurrent reader, including the REST scanner-list path.
type Registry struct {
	mu       sync.RWMutex
	scanners map[string]*ScannerPlugin // keyed by ID
	dataDir  string
	logger   *zap.Logger
}

// NewRegistry creates a new scanner registry
func NewRegistry(dataDir string, logger *zap.Logger) *Registry {
	r := &Registry{
		scanners: make(map[string]*ScannerPlugin),
		dataDir:  dataDir,
		logger:   logger,
	}
	r.loadBundledRegistry()
	r.loadUserRegistry()
	return r
}

// loadBundledRegistry loads the default bundled scanner definitions.
//
// In-process scanners (no Docker image to pull) start "installed" so they are
// always available to the engine — they need no install step. Docker-backed
// scanners start "available" and only become "installed" once their image is
// pulled.
//
// bundledScanners holds package-level pointers shared by every Registry in the
// process, so each registry stores its OWN clone: writing Status straight onto
// the shared record would make two registries (two tests, a service plus a
// CLI-side registry) mutate the same memory.
func (r *Registry) loadBundledRegistry() {
	for _, s := range bundledScanners {
		entry := s.clone()
		if entry.InProcess {
			entry.Status = ScannerStatusInstalled
		} else {
			entry.Status = ScannerStatusAvailable
		}
		r.scanners[entry.ID] = entry
	}
}

// loadUserRegistry loads user-customized scanner definitions from ~/.mcpproxy/scanner-registry.json
// User entries override bundled ones by ID
func (r *Registry) loadUserRegistry() {
	path := filepath.Join(r.dataDir, "scanner-registry.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			r.logger.Warn("Failed to read user scanner registry", zap.Error(err))
		}
		return
	}

	var userScanners []*ScannerPlugin
	if err := json.Unmarshal(data, &userScanners); err != nil {
		r.logger.Warn("Failed to parse user scanner registry, using bundled defaults", zap.Error(err))
		return
	}

	for _, s := range userScanners {
		if s.ID == "" {
			continue
		}
		s.Custom = true
		if s.Status == "" {
			s.Status = ScannerStatusAvailable
		}
		r.scanners[s.ID] = s
	}
}

// List returns all known scanners (bundled + user) sorted by ID so that
// API consumers, CLI output, and the web UI all see a deterministic order.
//
// The returned plugins are defensive copies: the caller may read and mutate
// them freely without racing a concurrent install/pull.
func (r *Registry) List() []*ScannerPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ScannerPlugin, 0, len(r.scanners))
	for _, s := range r.scanners {
		result = append(result, s.clone())
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// InProcessRunnableIDs returns the IDs of in-process scanners whose status
// means the engine will actually run them (installed or configured), sorted.
//
// It exists so a caller can count the always-on in-process baseline without
// copying the whole plugin set: resolving the predicate here keeps the read
// under the same lock as the write, and returns only the ids.
func (r *Registry) InProcessRunnableIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.scanners))
	for id, s := range r.scanners {
		if s == nil || !s.InProcess {
			continue
		}
		if s.Status == ScannerStatusInstalled || s.Status == ScannerStatusConfigured {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Get returns a defensive copy of the scanner with the given ID. Mutating the
// result does NOT change registry state — use UpdateStatus/SetConfiguredEnv/
// SetRuntimeConfig to write it back under the lock.
func (r *Registry) Get(id string) (*ScannerPlugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.scanners[id]
	if !ok {
		return nil, fmt.Errorf("scanner not found: %s", id)
	}
	return s.clone(), nil
}

// Register adds a custom scanner to the registry
// It validates the manifest and saves to user registry file
func (r *Registry) Register(s *ScannerPlugin) error {
	if err := ValidateManifest(s); err != nil {
		return fmt.Errorf("invalid scanner manifest: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	s.Custom = true
	if s.Status == "" {
		s.Status = ScannerStatusAvailable
	}
	// Store our own copy — the caller keeps its pointer and may keep writing
	// to it, which must never reach the record readers see under the lock.
	r.scanners[s.ID] = s.clone()

	return r.saveUserRegistry()
}

// Unregister removes a custom scanner from the registry
// Cannot unregister bundled scanners
func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.scanners[id]
	if !ok {
		return fmt.Errorf("scanner not found: %s", id)
	}
	if !s.Custom {
		return fmt.Errorf("cannot unregister bundled scanner: %s", id)
	}

	delete(r.scanners, id)
	return r.saveUserRegistry()
}

// UpdateStatus updates the status of a scanner in the registry
func (r *Registry) UpdateStatus(id, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.scanners[id]
	if !ok {
		return fmt.Errorf("scanner not found: %s", id)
	}
	s.Status = status
	return nil
}

// SetConfiguredEnv replaces a scanner's configured env under the registry
// lock, so the engine picks up newly stored API keys without a restart. The
// map is copied — the caller may keep mutating the one it passed in.
func (r *Registry) SetConfiguredEnv(id string, env map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.scanners[id]
	if !ok {
		return fmt.Errorf("scanner not found: %s", id)
	}
	s.ConfiguredEnv = copyEnv(env)
	return nil
}

// SetRuntimeConfig replaces a scanner's configured env AND image override in a
// single locked update, so a concurrent reader never observes a half-applied
// configuration (new env with the old image, or vice versa).
func (r *Registry) SetRuntimeConfig(id string, env map[string]string, imageOverride string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.scanners[id]
	if !ok {
		return fmt.Errorf("scanner not found: %s", id)
	}
	s.ConfiguredEnv = copyEnv(env)
	s.ImageOverride = imageOverride
	return nil
}

// saveUserRegistry writes custom scanners to user registry file
func (r *Registry) saveUserRegistry() error {
	var customs []*ScannerPlugin
	for _, s := range r.scanners {
		if s.Custom {
			customs = append(customs, s)
		}
	}

	data, err := json.MarshalIndent(customs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal user registry: %w", err)
	}

	path := filepath.Join(r.dataDir, "scanner-registry.json")
	return os.WriteFile(path, data, 0644)
}

// ValidateManifest validates a scanner plugin manifest
func ValidateManifest(s *ScannerPlugin) error {
	if s.ID == "" {
		return fmt.Errorf("scanner ID is required")
	}
	if s.Name == "" {
		return fmt.Errorf("scanner name is required")
	}
	if s.DockerImage == "" {
		return fmt.Errorf("docker_image is required")
	}
	if len(s.Inputs) == 0 {
		return fmt.Errorf("at least one input type is required")
	}
	for _, input := range s.Inputs {
		switch input {
		case "source", "mcp_connection", "container_image":
			// valid
		default:
			return fmt.Errorf("invalid input type: %s (valid: source, mcp_connection, container_image)", input)
		}
	}
	if len(s.Command) == 0 {
		return fmt.Errorf("command is required")
	}
	return nil
}
