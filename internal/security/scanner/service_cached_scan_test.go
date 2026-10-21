package scanner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 101 FR-019 / tasks B17, T363: the post-activation re-protection scan is
// an informational job over the contracts discovery already ingested for a
// server's APPROVED tools. It never connects to the upstream, never schedules
// Pass 2 and is excluded from the scan telemetry counters.

// cachedScanProvider is a ServerInfoProvider that counts every call a cached
// scan must not make.
type cachedScanProvider struct {
	approved []map[string]interface{}
	peers    map[string][]map[string]interface{}
	info     *ServerInfo

	isConnectedCalls     atomic.Int32
	ensureConnectedCalls atomic.Int32
	getServerToolsCalls  atomic.Int32
	allToolsCalls        atomic.Int32
}

func (p *cachedScanProvider) GetServerInfo(string) (*ServerInfo, error) { return p.info, nil }
func (p *cachedScanProvider) GetServerTools(string) ([]map[string]interface{}, error) {
	p.getServerToolsCalls.Add(1)
	return nil, errors.New("a cached scan must not export live tools")
}
func (p *cachedScanProvider) EnsureConnected(context.Context, string) error {
	p.ensureConnectedCalls.Add(1)
	return errors.New("a cached scan must not connect")
}
func (p *cachedScanProvider) IsConnected(string) bool {
	p.isConnectedCalls.Add(1)
	return false
}
func (p *cachedScanProvider) GetApprovedToolContracts(string) ([]map[string]interface{}, error) {
	return p.approved, nil
}
func (p *cachedScanProvider) GetAllServerTools() (map[string][]map[string]interface{}, error) {
	p.allToolsCalls.Add(1)
	return p.peers, nil
}

func (p *cachedScanProvider) upstreamTouches() int32 {
	return p.isConnectedCalls.Load() + p.ensureConnectedCalls.Load() + p.getServerToolsCalls.Load()
}

func newCachedScanService(t *testing.T, provider ServerInfoProvider) (*Service, *mockStorage, *mockEmitter) {
	t.Helper()
	dir := t.TempDir()
	logger := zap.NewNop()
	store := newMockStorage()
	svc := NewService(store, NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	emitter := newMockEmitter()
	svc.SetEmitter(emitter)
	svc.SetServerInfoProvider(provider)
	return svc, store, emitter
}

func waitCachedScan(t *testing.T, store *mockStorage, jobID string) []ScanFinding {
	t.Helper()
	var findings []ScanFinding
	require.Eventually(t, func() bool {
		job, err := store.GetScanJob(jobID)
		if err != nil || job.Status != ScanJobStatusCompleted {
			return false
		}
		reports, err := store.ListScanReportsByJob(jobID)
		if err != nil || len(reports) == 0 {
			return false
		}
		findings = nil
		for _, r := range reports {
			findings = append(findings, r.Findings...)
		}
		return true
	}, 10*time.Second, 20*time.Millisecond, "the cached scan must complete")
	return findings
}

const cachedDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func approvedPair() []map[string]interface{} {
	return []map[string]interface{}{
		rt06Tool("Summarizes text."),
		{
			"name":        "fetch_notes",
			"description": "Fetches notes.",
			"inputSchema": map[string]interface{}{"type": "object"},
		},
	}
}

func TestStartScanFromCachedContracts_BuildsAnInformationalJobFromApprovedContracts(t *testing.T) {
	provider := &cachedScanProvider{
		info:     &ServerInfo{Name: "srv", Protocol: "stdio", Command: "node"},
		approved: append(approvedPair(), rt06Tool(rt06Poisoned)),
		peers: map[string][]map[string]interface{}{
			"srv":   {{"name": "self", "description": "never a peer of itself"}},
			"other": {{"name": "other_tool", "description": "Another server's tool."}},
		},
	}
	svc, store, _ := newCachedScanService(t, provider)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)
	require.NotNil(t, job)

	ctx := job.ScanContext
	require.NotNil(t, ctx)
	assert.Equal(t, "tool_definitions_only", ctx.SourceMethod)
	assert.Equal(t, "stdio", ctx.ServerProtocol)
	assert.Equal(t, cachedDigest, ctx.BundleDigest, "the job is labelled with the bundle that triggered it")
	assert.Equal(t, []string{inProcessTPAScannerID}, job.Scanners, "only the in-process baseline runs")
	assert.Equal(t, ScanPassSecurityScan, job.ScanPass)
	assert.False(t, job.DryRun)

	// Accurate coverage: the digests describe exactly the contracts analysed.
	assert.Equal(t, 3, ctx.ToolsExported)
	assert.Equal(t, []string{"fetch_notes", "summarize_text"}, ctx.ToolNames)
	assert.Equal(t, ToolContractDigests(provider.approved), ctx.ToolContractDigests)
	assert.Equal(t, toolDefinitionHashes(provider.approved), ctx.ToolHashes)
	assert.False(t, ctx.ToolsExportedAt.IsZero())

	findings := waitCachedScan(t, store, job.ID)
	assert.Equal(t, "dangerous", verdictOf(findings), "the cached poisoned contract is scanned")

	assert.Zero(t, provider.upstreamTouches(), "no IsConnected, EnsureConnected or GetServerTools: the scan is cache-only")
	assert.Equal(t, int32(1), provider.allToolsCalls.Load(), "peers come from the cross-server snapshot, not a connection")
}

func TestStartScanFromCachedContracts_NeverSchedulesPass2OrCountsTelemetry(t *testing.T) {
	provider := &cachedScanProvider{
		info:     &ServerInfo{Name: "srv", Protocol: "stdio", Command: "node"},
		approved: approvedPair(),
	}
	svc, store, emitter := newCachedScanService(t, provider)
	svc.ApplySecurityConfig(&config.SecurityConfig{DeepScan: &config.DeepScanConfig{Enabled: true}})
	require.True(t, svc.DeepScanEnabled(), "precondition: with deep scan on, a normal Pass-1 completion starts Pass 2")

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)
	waitCachedScan(t, store, job.ID)

	// Pass 2 would be launched from OnScanCompleted on its own goroutine; give
	// it a window to show up as a second job.
	assert.Never(t, func() bool {
		jobs, _ := store.ListScanJobs("srv")
		return len(jobs) > 1
	}, 600*time.Millisecond, 25*time.Millisecond, "a re-protection job schedules no Pass 2")

	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	for _, ev := range emitter.events {
		assert.NotEqual(t, "scan_telemetry", ev.eventType,
			"the sweep must not move scans_completed or scans_with_findings")
	}
}

func TestCachedScanAdapter_TelemetryIsDecidedByTheAdapter(t *testing.T) {
	svc, _, _ := newCachedScanService(t, &cachedScanProvider{})
	job := &ScanJob{ScanPass: ScanPassSecurityScan}

	normal := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan}
	assert.True(t, normal.countsForTelemetry(job), "control: an ordinary Pass-1 job counts")

	reprotect := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan, informational: true}
	assert.False(t, reprotect.countsForTelemetry(job), "an informational re-protection job does not")
}

func TestStartScanFromCachedContracts_NoApprovedContractsIsUnscannedNeverClean(t *testing.T) {
	provider := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}}
	svc, store, _ := newCachedScanService(t, provider)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoCachedContracts)
	assert.Nil(t, job)
	jobs, _ := store.ListScanJobs("srv")
	assert.Empty(t, jobs, "no job is recorded, so the server stays unscanned rather than clean")
	assert.Nil(t, svc.GetScanSummary(context.Background(), "srv"))
	assert.Zero(t, provider.upstreamTouches())
}

func TestStartScanFromCachedContracts_ProviderWithoutTheCapabilityIsUnscanned(t *testing.T) {
	svc, _, _ := newCachedScanService(t, &namesProvider{tools: approvedPair()})

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)

	assert.ErrorIs(t, err, ErrNoCachedContracts, "a provider that cannot list cached contracts must not fall back to a live export")
	assert.Nil(t, job)
}

// gateReportStorage holds every report save until released, so the job stays
// active (the engine frees the slot only after its callbacks returned).
type gateReportStorage struct {
	*mockStorage
	release chan struct{}
}

func (g *gateReportStorage) SaveScanReport(report *ScanReport) error {
	<-g.release
	return g.mockStorage.SaveScanReport(report)
}

func TestStartScanFromCachedContracts_BusyServerReportsScanInProgress(t *testing.T) {
	provider := &cachedScanProvider{
		info:     &ServerInfo{Name: "srv", Protocol: "stdio"},
		approved: approvedPair(),
	}
	dir := t.TempDir()
	logger := zap.NewNop()
	store := &gateReportStorage{mockStorage: newMockStorage(), release: make(chan struct{})}
	svc := NewService(store, NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	svc.SetServerInfoProvider(provider)

	first, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)

	_, err = svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScanInProgress, "a second job while one is active is refused so the sweep can re-arm")

	close(store.release)
	waitCachedScan(t, store.mockStorage, first.ID)
}
