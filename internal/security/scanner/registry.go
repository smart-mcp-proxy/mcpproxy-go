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

// Registry manages the scanner plugin registry
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
func (r *Registry) loadBundledRegistry() {
	for _, s := range bundledScanners {
		if s.InProcess {
			s.Status = ScannerStatusInstalled
		} else {
			s.Status = ScannerStatusAvailable
		}
		r.scanners[s.ID] = s
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
func (r *Registry) List() []*ScannerPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ScannerPlugin, 0, len(r.scanners))
	for _, s := range r.scanners {
		result = append(result, s)
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
// reading ScannerPlugin fields outside the registry lock: List() hands out the
// live pointers the registry keeps, and UpdateStatus mutates Status on exactly
// those records, so an unsynchronized read of reg.Status races with any
// concurrent install/pull. Resolving the predicate here keeps the read under
// the same lock as the write.
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

// Get returns a scanner by ID
func (r *Registry) Get(id string) (*ScannerPlugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.scanners[id]
	if !ok {
		return nil, fmt.Errorf("scanner not found: %s", id)
	}
	return s, nil
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
	r.scanners[s.ID] = s

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
