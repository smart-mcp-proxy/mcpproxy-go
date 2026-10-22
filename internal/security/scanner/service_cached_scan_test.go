package scanner

import (
	"context"
	"errors"
	"fmt"
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
func (p *cachedScanProvider) GetCurrentToolContracts(string) ([]map[string]interface{}, error) {
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

// quarantinedCachedScanProvider adds the optional quarantine capability.
type quarantinedCachedScanProvider struct {
	*cachedScanProvider
	quarantined bool
}

func (p *quarantinedCachedScanProvider) IsServerQuarantined(string) bool { return p.quarantined }

func TestStartScanFromCachedContracts_QuarantinedServerGetsNoJob(t *testing.T) {
	base := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	provider := &quarantinedCachedScanProvider{cachedScanProvider: base, quarantined: true}
	svc, store, _ := newCachedScanService(t, provider)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.ErrorIs(t, err, ErrServerQuarantined)
	assert.Nil(t, job)
	jobs, _ := store.ListScanJobs("srv")
	assert.Empty(t, jobs, "no job is recorded for a quarantined server")

	provider.quarantined = false
	job, err = svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err, "control: the same server scans once it is no longer quarantined")
	waitCachedScan(t, store, job.ID)
}

// T362 storm bound (US3 judge gap): a cached scan over many tools is ONE job
// with ONE started and ONE terminal event - what feeds the single activity
// record and the bounded SSE traffic - never one per tool.
func TestStartScanFromCachedContracts_ManyToolsIsOneJobAndOneTerminalEvent(t *testing.T) {
	var tools []map[string]interface{}
	for i := 0; i < 40; i++ {
		tools = append(tools, map[string]interface{}{
			"name":        fmt.Sprintf("tool_%02d", i),
			"description": fmt.Sprintf("Does thing number %d.", i),
			"inputSchema": map[string]interface{}{"type": "object"},
		})
	}
	provider := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: tools}
	svc, store, emitter := newCachedScanService(t, provider)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)
	waitCachedScan(t, store, job.ID)
	time.Sleep(100 * time.Millisecond) // let any straggling event land

	jobs, _ := store.ListScanJobs("srv")
	assert.Len(t, jobs, 1, "one job for 40 tools")
	counts := map[string]int{}
	emitter.mu.Lock()
	for _, ev := range emitter.events {
		counts[ev.eventType]++
	}
	emitter.mu.Unlock()
	assert.Equal(t, 1, counts["scan_started"])
	assert.Equal(t, 1, counts["scan_completed"])
	assert.Zero(t, counts["scan_failed"])
	assert.LessOrEqual(t, counts["scan_progress"], 3, "progress events are per scanner, not per tool")
}

// replacingCachedScanProvider runs onRead inside every contract read, which is
// where a remove + re-add of the server can land between the read and the
// moment the scan binds to the server.
type replacingCachedScanProvider struct {
	*cachedScanProvider
	onRead func()
}

func (p *replacingCachedScanProvider) GetCurrentToolContracts(name string) ([]map[string]interface{}, error) {
	contracts, err := p.cachedScanProvider.GetCurrentToolContracts(name)
	if p.onRead != nil {
		p.onRead()
	}
	return contracts, err
}

// Sol round 1, finding 1: the scan must bind to the incarnation the contracts
// were read for. The old order read the contracts and THEN captured the
// incarnation, so a remove + re-add between the two scanned server A's stored
// contracts under replacement B's incarnation, and B's storage accepted A's
// verdict as its own.
func TestStartScanFromCachedContracts_BindsToTheIncarnationTheContractsWereReadFor(t *testing.T) {
	base := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	svc, mock, _ := newCachedScanService(t, base)
	store := &rowStorage{mockStorage: mock, rows: map[string]int64{"srv": 1}}
	svc.storage = store
	svc.SetServerUnquarantiner(&rowUnquarantiner{store: store})
	svc.SetServerInfoProvider(&replacingCachedScanProvider{
		cachedScanProvider: base,
		// The server is removed and re-added (incarnation 3) while its stored
		// contracts are being read.
		onRead: func() { store.setRow("srv", 3) },
	})

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)

	require.Error(t, err, "contracts read for incarnation 1 must not be scanned as the replacement's (incarnation 3)")
	assert.Nil(t, job)
	jobs, _ := mock.ListScanJobs("srv")
	assert.Empty(t, jobs, "no job under the replacement")
}

// Sol round 1, finding 2: the cached scan runs under the caller's lifecycle
// context and hands back a completion handle, so a stopping server can end it
// and join it before the stores close.
func TestStartScanFromCachedContracts_StoppedLifecycleStartsNothing(t *testing.T) {
	base := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	svc, store, _ := newCachedScanService(t, base)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	job, err := svc.StartScanFromCachedContracts(ctx, "srv", cachedDigest)

	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, job)
	jobs, _ := store.ListScanJobs("srv")
	assert.Empty(t, jobs, "a stopped lifecycle leaves no job record")
}

func TestStartScanFromCachedContracts_DoneClosesOnlyAfterTheCompletionCallbackReturned(t *testing.T) {
	provider := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	dir := t.TempDir()
	logger := zap.NewNop()
	store := &gateReportStorage{mockStorage: newMockStorage(), release: make(chan struct{})}
	svc := NewService(store, NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	svc.SetServerInfoProvider(provider)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)
	require.NotNil(t, job.Done(), "a cached scan job exposes its completion handle")

	select {
	case <-job.Done():
		t.Fatal("Done closed while the report save (a callback write) was still in flight")
	case <-time.After(150 * time.Millisecond):
	}

	close(store.release)
	select {
	case <-job.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done never closed after the callbacks returned")
	}
	stored, err := store.GetScanJob(job.ID)
	require.NoError(t, err)
	assert.Equal(t, ScanJobStatusCompleted, stored.Status, "when Done closes the terminal job is already persisted")
}
