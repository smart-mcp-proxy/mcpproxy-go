package oauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// orderEmitter records the numbered notifications of RefreshOrderEmitter.
type orderEmitter struct {
	mockEventEmitter
	omu    sync.Mutex
	saved  []uint64
	failed []uint64
	failAt []time.Time
}

func (e *orderEmitter) EmitOAuthTokenSavedNumbered(_ string, seq uint64) {
	e.omu.Lock()
	defer e.omu.Unlock()
	e.saved = append(e.saved, seq)
}

func (e *orderEmitter) EmitOAuthLoginRequiredNumbered(_ string, seq uint64, at time.Time) {
	e.omu.Lock()
	defer e.omu.Unlock()
	e.failed = append(e.failed, seq)
	e.failAt = append(e.failAt, at)
}

func (e *orderEmitter) snapshot() (saved, failed []uint64) {
	e.omu.Lock()
	defer e.omu.Unlock()
	return append([]uint64(nil), e.saved...), append([]uint64(nil), e.failed...)
}

// Spec 124 D5: every schedule install and every accepted failure takes the
// next number of its server under the manager's lock, so a save, the later
// failure of that same schedule and the next save are totally ordered however
// their notifications are delivered.
func TestRefreshManager_NotificationsAreNumberedInDecisionOrder(t *testing.T) {
	store := newMockTokenStore()
	emitter := &orderEmitter{}
	manager := NewRefreshManager(store, nil, nil, zaptest.NewLogger(t))
	manager.SetRuntime(&mockRuntime{refreshErr: errors.New("failed to refresh OAuth token: server not found: srv")})
	manager.SetEventEmitter(emitter)
	require.NoError(t, manager.Start(context.Background()))
	defer manager.Stop()

	manager.OnTokenSaved("srv", time.Now().Add(time.Hour)) // 1: save
	manager.executeRefresh("srv")                          // 2: terminal failure of that schedule
	require.Eventually(t, func() bool { _, f := emitter.snapshot(); return len(f) == 1 }, 5*time.Second, 5*time.Millisecond)
	afterFailure := time.Now()
	manager.OnTokenSaved("srv", time.Now().Add(time.Hour))   // 3: a new login
	manager.OnTokenSaved("other", time.Now().Add(time.Hour)) // one counter serves every server

	saved, failed := emitter.snapshot()
	assert.Equal(t, []uint64{1, 3, 4}, saved)
	assert.Equal(t, []uint64{2}, failed, "the failure of schedule 1 is numbered after its save and before the next")
	assert.Equal(t, 1, emitter.GetFailedEvents(), "the existing oauth.refresh_failed event is still emitted once")

	// Review round 2: the failure carries the time it was accepted, which is
	// before the save that followed it, however late it is delivered.
	emitter.omu.Lock()
	failedAt := emitter.failAt[0]
	emitter.omu.Unlock()
	assert.False(t, failedAt.IsZero())
	assert.False(t, failedAt.After(afterFailure), "the failure time is taken when it is accepted")
}

// Review finding: the numbered save notification is sent after the manager's
// lock is released, so an emitter may read the manager back.
type readingEmitter struct {
	orderEmitter
	manager *RefreshManager
	read    chan struct{}
}

func (e *readingEmitter) EmitOAuthTokenSavedNumbered(serverName string, seq uint64) {
	_ = e.manager.GetSchedule(serverName) // would deadlock under the lock
	e.orderEmitter.EmitOAuthTokenSavedNumbered(serverName, seq)
	close(e.read)
}

func TestRefreshManager_SaveNotificationIsSentOutsideTheLock(t *testing.T) {
	manager := NewRefreshManager(newMockTokenStore(), nil, nil, zaptest.NewLogger(t))
	emitter := &readingEmitter{manager: manager, read: make(chan struct{})}
	manager.SetEventEmitter(emitter)
	require.NoError(t, manager.Start(context.Background()))
	defer manager.Stop()

	go manager.OnTokenSaved("srv", time.Now().Add(time.Hour))
	select {
	case <-emitter.read:
	case <-time.After(5 * time.Second):
		t.Fatal("the emitter could not read the manager: the notification was sent under the lock")
	}
}
