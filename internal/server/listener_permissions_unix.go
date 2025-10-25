//go:build !windows

package server

import (
	"fmt"
	"os"
	"syscall"

	"go.uber.org/zap"
)

// validateDataDirectoryPermissionsPlatform performs Unix-specific permission validation
func validateDataDirectoryPermissionsPlatform(dataDir string, info os.FileInfo, logger *zap.Logger) error {
	// Unix: Check ownership
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot get file stat for ownership check")
	}

	currentUID := uint32(os.Getuid())
	if stat.Uid != currentUID {
		return fmt.Errorf("data directory not owned by current user (uid=%d, expected=%d)", stat.Uid, currentUID)
	}

	// Unix: Check permissions are secure (0700 or stricter)
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		return fmt.Errorf(
			"data directory has insecure permissions %#o, must be 0700 or stricter\n"+
				"Security risk: Other users can access mcpproxy data and control socket\n"+
				"To fix, run: chmod 0700 %s",
			perm, dataDir,
		)
	}

	logger.Info("Data directory security validation passed",
		zap.String("path", dataDir),
		zap.String("permissions", fmt.Sprintf("%#o", perm)))

	return nil
}
