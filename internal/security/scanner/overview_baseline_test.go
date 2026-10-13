package scanner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newFreshInstallService builds a service over the REAL bundled registry and an
// empty storage — i.e. exactly what a user gets on first launch, before any
// Docker scanner has ever been installed.
func newFreshInstallService(t *testing.T) *Service {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	return NewService(newMockStorage(), NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
}

// TestGetOverview_CountsAlwaysOnBaselineOnFreshInstall pins the fix for the
// invisible "Scan All Servers" button: the overview counted only scanners
// persisted in BBolt, so a fresh install reported scanners_enabled=0 even
// though the in-process tpa-descriptions baseline is always installed and runs
// on every scan. The web UI hides its scan-trigger button on 0.
func TestGetOverview_CountsAlwaysOnBaselineOnFreshInstall(t *testing.T) {
	svc := newFreshInstallService(t)

	overview, err := svc.GetOverview(context.Background())
	require.NoError(t, err)

	assert.GreaterOrEqual(t, overview.ScannersEnabled, 1,
		"the always-on in-process baseline must count as enabled on a fresh install")
	assert.GreaterOrEqual(t, overview.ScannersInstalled, overview.ScannersEnabled,
		"installed is a superset of enabled")

	// The count is the in-process baseline, not the Docker scanners: those load
	// as "available" and must stay uncounted until their image is pulled.
	inProcessEnabled := 0
	for _, sc := range svc.registry.List() {
		if sc.InProcess && (sc.Status == ScannerStatusInstalled || sc.Status == ScannerStatusConfigured) {
			inProcessEnabled++
		}
	}
	assert.Equal(t, inProcessEnabled, overview.ScannersEnabled,
		"only the in-process baseline is enabled before any Docker scanner is installed")
}

// TestGetOverview_DoesNotDoubleCountPersistedBaseline guards the other half:
// once the baseline IS persisted (older builds saved it, and the healing path
// in syncRegistryFromStorage rewrites it), the registry pass must not count it
// a second time.
func TestGetOverview_DoesNotDoubleCountPersistedBaseline(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	store := newMockStorage()
	registry := NewRegistry(dir, logger)

	baseline, err := registry.Get(inProcessTPAScannerID)
	require.NoError(t, err)
	persisted := *baseline
	persisted.Status = ScannerStatusInstalled
	require.NoError(t, store.SaveScanner(&persisted))

	svc := NewService(store, registry, NewDockerRunner(logger), dir, logger)
	overview, err := svc.GetOverview(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, overview.ScannersEnabled, "the persisted baseline is counted exactly once")
	assert.Equal(t, 1, overview.ScannersInstalled, "the persisted baseline is counted exactly once")
}

// TestGetOverview_ConcurrentScannerStatusUpdateIsRaceFree guards the read seam
// the baseline count added: counting enabled in-process scanners must stay
// synchronized against a concurrent install/pull flipping Status under the
// registry lock. GetOverview resolves the predicate inside the registry
// (InProcessRunnableIDs) instead of reading fields out of band. Run with -race.
func TestGetOverview_ConcurrentScannerStatusUpdateIsRaceFree(t *testing.T) {
	svc := newFreshInstallService(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			status := ScannerStatusInstalled
			if i%2 == 0 {
				status = ScannerStatusAvailable
			}
			_ = svc.registry.UpdateStatus(inProcessTPAScannerID, status)
		}
	}()

	for i := 0; i < 200; i++ {
		_, err := svc.GetOverview(context.Background())
		require.NoError(t, err)
	}
	<-done
}

// TestGetOverview_ToleratesNilRegistry pins the nil-registry contract: other
// Service methods already guard for it, and the overview must not be the one
// call that panics on a service built without a registry.
func TestGetOverview_ToleratesNilRegistry(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	svc := NewService(newMockStorage(), nil, NewDockerRunner(logger), dir, logger)

	overview, err := svc.GetOverview(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, overview.ScannersEnabled)
}
