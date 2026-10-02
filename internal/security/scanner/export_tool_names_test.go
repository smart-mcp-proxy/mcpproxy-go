package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namesProvider reports a fixed tool list (duplicates and unsorted on
// purpose). When failFirst is set the first GetServerTools call fails, so the
// Pass-1 retry export path is the one that has to record the names.
type namesProvider struct {
	info      *ServerInfo
	tools     []map[string]interface{}
	failFirst bool
	calls     int
}

func (p *namesProvider) GetServerInfo(string) (*ServerInfo, error) { return p.info, nil }
func (p *namesProvider) GetServerTools(string) ([]map[string]interface{}, error) {
	p.calls++
	if p.failFirst && p.calls == 1 {
		return nil, errors.New("tools/list not ready")
	}
	return p.tools, nil
}
func (p *namesProvider) EnsureConnected(context.Context, string) error { return nil }
func (p *namesProvider) IsConnected(string) bool                       { return true }

func TestExportToolDefinitionsReturnsSortedUniqueNames(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	svc := NewService(newMockStorage(), NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	svc.SetServerInfoProvider(&namesProvider{
		tools: []map[string]interface{}{{"name": "b"}, {"name": "a"}, {"name": "a"}, {"description": "no name"}},
	})

	count, names := svc.exportToolDefinitions("srv", t.TempDir())
	assert.Equal(t, 4, count)
	assert.Equal(t, []string{"a", "b"}, names)
}

func TestStartScanRecordsToolNamesOnScanContext(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failFirst bool
	}{
		{"first export", false},
		{"retry export", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			logger := zap.NewNop()
			store := newMockStorage()
			svc := NewService(store, NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
			svc.SetServerInfoProvider(&namesProvider{
				info:      &ServerInfo{Name: "srv-names", Protocol: "stdio", Command: "node", Args: []string{"server.js"}},
				tools:     []map[string]interface{}{{"name": "b"}, {"name": "a"}, {"name": "a"}},
				failFirst: tc.failFirst,
			})

			job, err := svc.StartScan(context.Background(), "srv-names", false, nil, "")
			require.NoError(t, err)
			waitForScanIdle(t, svc, "srv-names")

			final, err := store.GetScanJob(job.ID)
			require.NoError(t, err)
			require.NotNil(t, final)
			require.NotNil(t, final.ScanContext)
			assert.Equal(t, 3, final.ScanContext.ToolsExported)
			assert.Equal(t, []string{"a", "b"}, final.ScanContext.ToolNames)
		})
	}
}

func TestStartScanRecordsToolsExportedAt(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	store := newMockStorage()
	svc := NewService(store, NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	svc.SetServerInfoProvider(&namesProvider{
		info:  &ServerInfo{Name: "srv-at", Protocol: "stdio", Command: "node", Args: []string{"server.js"}},
		tools: []map[string]interface{}{{"name": "a"}},
	})

	before := time.Now().UTC().Add(-time.Second)
	job, err := svc.StartScan(context.Background(), "srv-at", false, nil, "")
	require.NoError(t, err)
	waitForScanIdle(t, svc, "srv-at")

	final, err := store.GetScanJob(job.ID)
	require.NoError(t, err)
	require.NotNil(t, final.ScanContext)
	assert.False(t, final.ScanContext.ToolsExportedAt.IsZero())
	assert.True(t, final.ScanContext.ToolsExportedAt.After(before))
}
