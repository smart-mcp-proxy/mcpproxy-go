package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// reconcileProbe parks the first reconcile that enters, and counts entries and
// exits so a test can wait until every reconcile that started has written
// storage (on main the reconcile runs on a goroutine ApplyConfig spawned).
type reconcileProbe struct {
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	entries  atomic.Int32
	exits    atomic.Int32
	parkOnce sync.Once
}

func installReconcileProbe(srv *Server) *reconcileProbe {
	p := &reconcileProbe{entered: make(chan struct{}), release: make(chan struct{})}
	srv.runtime.SetReconcileEntryHookForTest(func() {
		p.entries.Add(1)
		parked := false
		p.parkOnce.Do(func() { parked = true })
		if parked {
			close(p.entered)
			<-p.release
		}
	})
	srv.runtime.SetReconcileExitHookForTest(func() { p.exits.Add(1) })
	return p
}

func (p *reconcileProbe) releaseParked() { p.once.Do(func() { close(p.release) }) }

// settle waits until every reconcile that entered has exited.
func (p *reconcileProbe) settle(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return p.exits.Load() >= p.entries.Load() },
		20*time.Second, time.Millisecond, "a reconcile that started never finished")
}

func seedBravoState(t *testing.T, srv *Server) string {
	t.Helper()
	st := srv.runtime.StorageManager()
	now := time.Now().UTC()
	for _, tool := range []string{"read", "write"} {
		require.NoError(t, st.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "bravo", ToolName: tool, Status: storage.ToolApprovalStatusApproved,
			CurrentHash: "h-" + tool, ApprovedHash: "h-" + tool, ApprovedBy: "test", ApprovedAt: now,
			Disabled: tool == "write",
		}))
	}
	bravo := liveServer(srv, "bravo")
	require.NotNil(t, bravo, "bravo must be configured before its state is seeded")
	identity, err := st.RegisterServerIdentity(bravo, "")
	require.NoError(t, err)
	require.NoError(t, st.RecordToolCall(&storage.ToolCallRecord{
		ID: "call-1", ServerID: identity.ID, ServerName: "bravo", ToolName: "read", Timestamp: now,
	}))
	return identity.ID
}

func assertBravoStateIntact(t *testing.T, srv *Server, identityID string) {
	t.Helper()
	st := srv.runtime.StorageManager()
	assert.NotNil(t, liveServer(srv, "bravo"), "bravo must stay in the live config")
	assert.NotNil(t, storedServer(t, srv, "bravo"), "bravo's storage row must survive")
	records, err := st.ListToolApprovals("bravo")
	require.NoError(t, err)
	assert.Len(t, records, 2, "bravo's approval records must survive the stale reconcile")
	blocked, err := st.GetToolApproval("bravo", "write")
	if assert.NoError(t, err) {
		assert.True(t, blocked.Disabled, "the operator's tool block must survive")
	}
	calls, err := st.GetServerToolCalls(identityID, 10)
	require.NoError(t, err)
	assert.Len(t, calls, 1, "bravo's call history must survive")
}

// T1.1 (#1558, review R2.1): a reconcile that read the configuration before a
// concurrent REST add must not delete the added server, prune its approvals or
// its call history. On main the ApplyConfig reconcile runs on a goroutine with
// the config it captured; after Spec 116 it runs inside the apply commit, so
// the add waits for it on the commit lock.
func TestSpec116_T1_1_StaleReconcileKeepsConcurrentAdd(t *testing.T) {
	for _, removeAlpha := range []bool{false, true} {
		name := "apply_edits_alpha"
		if removeAlpha {
			name = "apply_removes_alpha"
		}
		t.Run(name, func(t *testing.T) {
			srv, cfgPath := newSpec116Server(t,
				&config.ServerConfig{Name: "alpha", Command: "true", Protocol: "stdio", Enabled: false},
				&config.ServerConfig{Name: "keep", Command: "true", Protocol: "stdio", Enabled: false},
			)
			probe := installReconcileProbe(srv)
			defer probe.releaseParked()

			next := cloneConfig(t, srv.runtime.Config())
			if removeAlpha {
				var kept []*config.ServerConfig
				for _, sc := range next.Servers {
					if sc.Name != "alpha" {
						kept = append(kept, sc)
					}
				}
				next.Servers = kept
			} else {
				for _, sc := range next.Servers {
					if sc.Name == "alpha" {
						sc.Args = []string{"edited"}
					}
				}
			}

			applyDone := make(chan error, 1)
			go func() {
				_, err := srv.runtime.ApplyConfig(next, cfgPath)
				applyDone <- err
			}()
			awaitSignal(t, probe.entered, "the apply reconcile to park")

			addDone := make(chan error, 1)
			go func() {
				addDone <- srv.AddServer(context.Background(), &config.ServerConfig{
					Name: "bravo", Command: "true", Protocol: "stdio", Enabled: false,
				})
			}()
			returned, addErr := awaitReturnOrBlocked(t, addDone, func() bool { return srv.runtime.CommitWaitersForTest() > 0 })
			var identityID string
			if returned {
				require.NoError(t, addErr)
				identityID = seedBravoState(t, srv)
			}
			probe.releaseParked()
			require.NoError(t, awaitErr(t, applyDone, "ApplyConfig"))
			if !returned {
				require.NoError(t, awaitErr(t, addDone, "AddServer"))
				identityID = seedBravoState(t, srv)
			}
			probe.settle(t)

			assertBravoStateIntact(t, srv, identityID)
			if removeAlpha {
				assert.Nil(t, liveServer(srv, "alpha"))
				assert.Nil(t, storedServer(t, srv, "alpha"))
				assert.Nil(t, diskServer(t, cfgPath, "alpha"))
			} else {
				if a := storedServer(t, srv, "alpha"); assert.NotNil(t, a) {
					assert.Equal(t, []string{"edited"}, a.Args)
				}
			}
			assert.NotNil(t, diskServer(t, cfgPath, "bravo"), "bravo must be on disk")
		})
	}
}
