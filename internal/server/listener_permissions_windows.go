//go:build windows

package server

import (
	"os"

	"go.uber.org/zap"
)

// validateDataDirectoryPermissionsPlatform performs Windows-specific permission validation
func validateDataDirectoryPermissionsPlatform(dataDir string, info os.FileInfo, logger *zap.Logger) error {
	// Windows: ACL checks would go here (simplified for now)
	logger.Debug("Windows data directory validation (ACL checks not yet implemented)")
	return nil
}
