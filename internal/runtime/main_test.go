package runtime

import (
	"os"
	"testing"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestMain turns off fsync for config writes and bbolt commits. Nearly every
// test here builds a Runtime, which writes the config file, commits to
// config.db and creates a Bleve index; on the windows-latest runner those
// flushes cost tens of milliseconds each and the package took ~470s against
// go test's default 10m timeout (TestConfigCommit_CommitPathsConverge alone
// ran 92s). Skipping the flush changes nothing a test can observe in-process
// (writes stay visible to every reader); only crash durability is lost, and no
// test here simulates a crash.
//
// bbolt.DefaultOptions is what Bleve's scorch index copies for its root.bolt;
// storage opens config.db with its own options, hence the separate switch.
func TestMain(m *testing.M) {
	config.DisableWriteSyncForTesting()
	storage.DisableSyncForTesting()
	bbolt.DefaultOptions.NoSync = true
	os.Exit(m.Run())
}
