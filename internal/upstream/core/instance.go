package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

var (
	instanceID     string
	instanceIDOnce sync.Once
)

// getInstanceID returns a unique identifier for this mcpproxy instance
// The ID is persisted across restarts and used to label Docker containers
func getInstanceID() string {
	instanceIDOnce.Do(func() {
		// Try to load from file first
		if id, err := loadInstanceID(); err == nil && id != "" {
			instanceID = id
			return
		}

		// Generate new instance ID
		instanceID = uuid.New().String()
		_ = saveInstanceID(instanceID) // Best effort save
	})
	return instanceID
}

// GetInstanceID returns the unique identifier for this mcpproxy instance (exported for use by manager)
func GetInstanceID() string {
	return getInstanceID()
}

// loadInstanceID attempts to load the instance ID from disk
func loadInstanceID() (string, error) {
	instanceFile := filepath.Join(os.TempDir(), "mcpproxy-instance-id")
	data, err := os.ReadFile(instanceFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// saveInstanceID persists the instance ID to disk
func saveInstanceID(id string) error {
	instanceFile := filepath.Join(os.TempDir(), "mcpproxy-instance-id")
	return os.WriteFile(instanceFile, []byte(id), 0644)
}

// formatContainerLabels returns Docker labels for container ownership tracking
func formatContainerLabels(serverName string) []string {
	instanceID := getInstanceID()
	return []string{
		"--label", "com.mcpproxy.managed=true",
		"--label", fmt.Sprintf("com.mcpproxy.instance=%s", instanceID),
		"--label", fmt.Sprintf("com.mcpproxy.server=%s", serverName),
		"--label", fmt.Sprintf("com.mcpproxy.created=%d", os.Getpid()),
	}
}
