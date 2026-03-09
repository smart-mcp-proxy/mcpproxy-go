//go:build server

package teams

import (
	"fmt"

	"github.com/go-chi/chi/v5"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Dependencies holds shared dependencies that server edition features need.
// These are provided by the server during initialization and passed
// to each feature's Setup function.
type Dependencies struct {
	Router            chi.Router
	DB                *bbolt.DB
	Logger            *zap.SugaredLogger
	Config            *config.Config
	DataDir           string
	ManagementService interface{}      // management.Service - kept as interface{} to avoid circular imports
	StorageManager    *storage.Manager // Shared storage manager for token operations
}

// Feature represents a server edition feature module that self-registers.
type Feature struct {
	Name  string
	Setup func(deps Dependencies) error
}

var features []Feature

// Register adds a server edition feature to the registry.
// Called by feature packages in their init() functions.
func Register(f Feature) {
	features = append(features, f)
}

// SetupAll initializes all registered server edition features.
func SetupAll(deps Dependencies) error {
	for _, f := range features {
		if err := f.Setup(deps); err != nil {
			return fmt.Errorf("server feature %s: %w", f.Name, err)
		}
	}
	return nil
}

// RegisteredFeatures returns the names of all registered features.
func RegisteredFeatures() []string {
	names := make([]string, len(features))
	for i, f := range features {
		names[i] = f.Name
	}
	return names
}
