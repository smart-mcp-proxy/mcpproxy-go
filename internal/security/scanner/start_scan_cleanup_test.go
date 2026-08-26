package scanner

import (
	"context"
	"os"
	"strings"
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
	// Point os.MkdirTemp("") at a scratch dir. Which variable wins is
	// platform-specific (TMPDIR on unix, TMP/TEMP on Windows), so set all three
	// and then read back os.TempDir() rather than asserting any one of them
	// took effect — and count only OUR prefix, so the assertion is still exact
	// even if the process temp dir is shared.
	tmpRoot := t.TempDir()
	t.Setenv("TMPDIR", tmpRoot)
	t.Setenv("TMP", tmpRoot)
	t.Setenv("TEMP", tmpRoot)

	countScanTempDirs := func() int {
		entries, err := os.ReadDir(os.TempDir())
		require.NoError(t, err)
		n := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "mcpproxy-scan-tools-") {
				n++
			}
		}
		return n
	}

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

	before := countScanTempDirs()

	for i := 0; i < 3; i++ {
		job, err := svc.StartScan(context.Background(), "busy-srv", false, nil, "")
		require.Error(t, err, "the engine must reject while a scan is in progress")
		assert.Nil(t, job)
	}

	assert.Equal(t, before, countScanTempDirs(),
		"a rejected scan must not leave its tool-definition temp dir behind")
}
