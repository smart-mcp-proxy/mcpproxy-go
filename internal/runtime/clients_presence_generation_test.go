package runtime

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestRecordClientSeenAllowsFirstInitializeAfterRecentDisconnect(t *testing.T) {
	t.Setenv("MCPPROXY_TELEMETRY", "false")
	dataDir := t.TempDir()
	mgr, err := storage.NewManager(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	seen := time.Now().Add(-10 * time.Second)
	disconnected := seen.Add(5 * time.Second)
	require.NoError(t, mgr.SaveOnboardingState(&storage.OnboardingState{
		ClientLastSeen:       map[string]time.Time{"cursor": seen},
		ClientDisconnectedAt: map[string]time.Time{"cursor": disconnected},
	}))
	r := &Runtime{storageManager: mgr, logger: zap.NewNop()}
	r.RecordClientSeen("cursor")

	state, err := mgr.GetOnboardingState()
	require.NoError(t, err)
	require.True(t, state.ClientLastSeen["cursor"].After(disconnected), "new generation initialize should not be throttled")
	require.NoError(t, mgr.Close())

	reopened, err := storage.NewManager(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	persisted, err := reopened.GetOnboardingState()
	require.NoError(t, err)
	require.True(t, persisted.ClientLastSeen["cursor"].After(disconnected), "presence survives a storage reopen without telemetry")
}

func TestRecordClientSeenCapsOtherNamesWithoutEvictingKnownAliases(t *testing.T) {
	mgr, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	r := &Runtime{storageManager: mgr, logger: zap.NewNop()}
	for i := 0; i < 40; i++ {
		r.RecordClientSeen("other-client-" + strconv.Itoa(i))
	}
	r.RecordClientSeen("cursor")

	state, err := mgr.GetOnboardingState()
	require.NoError(t, err)
	require.LessOrEqual(t, len(state.ClientLastSeen), 32)
	require.Contains(t, state.ClientLastSeen, "cursor")
}

func TestRecordClientSeenCreditsBridgedAndCodexClientNames(t *testing.T) {
	mgr, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	r := &Runtime{storageManager: mgr, logger: zap.NewNop()}
	r.RecordClientSeen("claude-ai (via mcp-remote 0.15.2)")
	r.RecordClientSeen("codex-mcp-client")
	r.RecordClientSeen("\x1bclaude-ai (via mcp-remote 0.15.2)")

	state, err := mgr.GetOnboardingState()
	require.NoError(t, err)
	require.Contains(t, state.ClientLastSeen, "claude-ai")
	require.Contains(t, state.ClientLastSeen, "codex-mcp-client")
	require.NotNil(t, latestSeenForAliases(state.ClientLastSeen, []string{"claude-ai", "Claude"}))
	require.NotContains(t, state.ClientLastSeen, "v1:p:claude-ai (via mcp-remote 0.15.2)", "a bridged known client must not keep an other: key")
	require.Len(t, state.ClientLastSeen, 3, "the escape-prefixed name stays a distinct unknown client")
}
