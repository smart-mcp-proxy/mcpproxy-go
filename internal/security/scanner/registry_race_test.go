package scanner

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRegistryGetReturnsDefensiveCopy pins the contract that closes the
// registry data race: Get must NOT hand out the live *ScannerPlugin the
// registry keeps under its lock. Every caller that mutated the returned record
// (InstallScanner, ConfigureScanner, syncRegistryFromStorage, GetScannerStatus)
// was writing registry state outside the lock, racing every concurrent reader
// including the REST scanner-list path.
func TestRegistryGetReturnsDefensiveCopy(t *testing.T) {
	reg := NewRegistry(t.TempDir(), zap.NewNop())

	got, err := reg.Get(inProcessTPAScannerID)
	require.NoError(t, err)
	original := got.Status

	got.Status = "mutated-by-caller"
	got.ErrorMsg = "mutated-by-caller"
	got.ConfiguredEnv = map[string]string{"LEAK": "1"}

	again, err := reg.Get(inProcessTPAScannerID)
	require.NoError(t, err)
	assert.Equal(t, original, again.Status, "mutating a Get() result must not change registry state")
	assert.Empty(t, again.ErrorMsg)
	assert.Nil(t, again.ConfiguredEnv)
}

// TestRegistryListReturnsDefensiveCopies is the List() half of the same
// contract — the REST/CLI/web-UI scanner list all read these records.
func TestRegistryListReturnsDefensiveCopies(t *testing.T) {
	reg := NewRegistry(t.TempDir(), zap.NewNop())

	list := reg.List()
	require.NotEmpty(t, list)
	for _, s := range list {
		s.Status = "mutated-by-caller"
		s.Inputs = append(s.Inputs, "mutated")
	}

	for _, s := range reg.List() {
		assert.NotEqual(t, "mutated-by-caller", s.Status,
			"mutating a List() result must not change registry state")
		assert.NotContains(t, s.Inputs, "mutated",
			"List() must deep-copy slice fields too")
	}
}

// TestRegistryMutationIsLockedAgainstReaders is the -race stress test for the
// registry seam: readers hammer List/Get (and read every field the API
// serializes) while writers hammer the locked mutators.
func TestRegistryMutationIsLockedAgainstReaders(t *testing.T) {
	reg := NewRegistry(t.TempDir(), zap.NewNop())

	const iterations = 300
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			status := ScannerStatusInstalled
			if i%2 == 0 {
				status = ScannerStatusAvailable
			}
			_ = reg.UpdateStatus(inProcessTPAScannerID, status)
			_ = reg.SetRuntimeConfig(inProcessTPAScannerID,
				map[string]string{"KEY": status}, "ghcr.io/example/img:"+status)
			_ = reg.SetConfiguredEnv(inProcessTPAScannerID, map[string]string{"KEY": status})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			for _, s := range reg.List() {
				_ = s.Status
				_ = s.EffectiveImage()
				for k, v := range s.ConfiguredEnv {
					_, _ = k, v
				}
			}
			if s, err := reg.Get(inProcessTPAScannerID); err == nil {
				_ = s.Status
				_ = s.EffectiveImage()
			}
			_ = reg.InProcessRunnableIDs()
		}
	}()

	wg.Wait()
}

// TestInstallScannerIsRaceFreeAgainstListScanners is the end-to-end repro of
// the reported defect: InstallScanner used to write Status/InstalledAt/ErrorMsg
// straight onto the live registry record returned by Get, while ListScanners
// (GET /api/v1/security/scanners) read the very same record. Run with -race.
func TestInstallScannerIsRaceFreeAgainstListScanners(t *testing.T) {
	svc := newFreshInstallService(t)
	ctx := context.Background()

	const iterations = 200
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			// The in-process baseline needs no Docker, so this exercises the
			// synchronous install path on every iteration.
			_ = svc.InstallScanner(ctx, inProcessTPAScannerID)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			scanners, err := svc.ListScanners(ctx)
			require.NoError(t, err)
			for _, s := range scanners {
				_ = s.Status
				_ = s.EffectiveImage()
			}
			_, _ = svc.GetOverview(ctx)
		}
	}()

	wg.Wait()
}

// TestConfigureScannerIsRaceFreeAgainstRegistryReaders covers the other
// mutation path flagged in the review: ConfigureScanner wrote ConfiguredEnv and
// ImageOverride onto the live registry record outside the lock.
func TestConfigureScannerIsRaceFreeAgainstRegistryReaders(t *testing.T) {
	svc := newFreshInstallService(t)
	ctx := context.Background()

	const iterations = 200
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = svc.ConfigureScanner(ctx, inProcessTPAScannerID, map[string]string{"API_KEY": "v"}, "")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			for _, s := range svc.registry.List() {
				_ = s.EffectiveImage()
				for k, v := range s.ConfiguredEnv {
					_, _ = k, v
				}
			}
		}
	}()

	wg.Wait()
}
