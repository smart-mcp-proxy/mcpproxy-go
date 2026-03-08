//go:build teams

package teams

import (
	"fmt"

	"github.com/go-chi/chi/v5"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Dependencies holds shared dependencies that teams features need.
// These are provided by the server during initialization and passed
// to each feature's Setup function.
type Dependencies struct {
	Router  chi.Router
	DB      *bbolt.DB
	Logger  *zap.SugaredLogger
	Config  *config.Config
	DataDir string
}

// Feature represents a teams feature module that self-registers.
type Feature struct {
	Name  string
	Setup func(deps Dependencies) error
}

var features []Feature

// Register adds a teams feature to the registry.
// Called by feature packages in their init() functions.
func Register(f Feature) {
	features = append(features, f)
}

// SetupAll initializes all registered teams features.
func SetupAll(deps Dependencies) error {
	for _, f := range features {
		if err := f.Setup(deps); err != nil {
			return fmt.Errorf("teams feature %s: %w", f.Name, err)
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
