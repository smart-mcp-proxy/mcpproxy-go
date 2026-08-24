package scanner

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Service.StartScan exports tool definitions into an os.MkdirTemp directory and
// hands ownership of the cleanup to the scan callback. The engine, however, can
// reject the scan (another scan already in progress, no scanners resolved)
// BEFORE it ever invokes that callback — so on the reject path nothing ever
// removes the directory. It leaks for the life of the process, and
// quarantine_security's scan_server makes that path agent-reachable in a loop.
func TestStartScan_CleansUpTempDirWhenEngineRejectsScan(t *testing.T) {
	// Isolate os.MkdirTemp("") so the assertion counts only this test's dirs.
	tmpRoot := t.TempDir()
	t.Setenv("TMPDIR", tmpRoot)
	require.Equal(t, tmpRoot, os.TempDir(), "precondition: os.TempDir must follow TMPDIR")

	dir := t.TempDir()
	logger := zap.NewNop()
	svc := NewService(newMockStorage(), NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	svc.SetServerInfoProvider(&scanSpyProvider{info: &ServerInfo{
		Name:     "busy-srv",
		Protocol: "stdio",
		Command:  "node",
		Args:     []string{"server.js"},
	}})

	// Occupy the engine so every StartScan is rejected deterministically —
	// exactly what a second scan_server call hits while the first still runs.
	svc.engine.mu.Lock()
	svc.engine.activeScans["busy-srv"] = &ScanJob{
		ID:         "scan-busy-srv-preexisting",
		ServerName: "busy-srv",
		Status:     ScanJobStatusRunning,
		ScanPass:   ScanPassSecurityScan,
		StartedAt:  time.Now(),
	}
	svc.engine.mu.Unlock()

	before, err := os.ReadDir(tmpRoot)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		job, err := svc.StartScan(context.Background(), "busy-srv", false, nil, "")
		require.Error(t, err, "the engine must reject while a scan is in progress")
		assert.Nil(t, job)
	}

	after, err := os.ReadDir(tmpRoot)
	require.NoError(t, err)

	assert.Len(t, after, len(before),
		"a rejected scan must not leave its tool-definition temp dir behind")
}
