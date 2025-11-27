package main

import (
	"testing"

	"mcpproxy-go/internal/socket"

	"github.com/stretchr/testify/assert"
)

func TestShouldUseToolsDaemon(t *testing.T) {
	// Test with non-existent directory
	result := shouldUseToolsDaemon("/tmp/nonexistent-mcpproxy-test-dir-tools-99999")
	assert.False(t, result, "shouldUseToolsDaemon should return false for non-existent directory")

	// Test with existing directory but no socket
	tmpDir := t.TempDir()
	result = shouldUseToolsDaemon(tmpDir)
	assert.False(t, result, "shouldUseToolsDaemon should return false when socket doesn't exist")
}

func TestDetectSocketPath_Tools(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := socket.DetectSocketPath(tmpDir)

	assert.NotEmpty(t, socketPath, "DetectSocketPath should return non-empty path")
	assert.Contains(t, socketPath, tmpDir, "Socket path should be within data directory")
}
