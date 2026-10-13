package storage

import (
	"os"
	"testing"
)

// TestMain opens every test database with bbolt NoSync: an fsync per commit
// made this package take ~150s on the windows-latest runner. Committed data
// stays visible to every reader of the open database; only crash durability is
// lost, and no test here simulates a crash. See DisableSyncForTesting.
func TestMain(m *testing.M) {
	DisableSyncForTesting()
	os.Exit(m.Run())
}
