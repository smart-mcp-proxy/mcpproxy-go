package storage

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateUpstreamServer_RefusesExistingAndKeepsRecord(t *testing.T) {
	m := newGuardManager(t)
	orig := guardServer("s", false)
	orig.Args = []string{"180"}
	require.NoError(t, m.CreateUpstreamServer(orig))

	dup := guardServer("s", true)
	dup.Args = []string{"12"}
	err := m.CreateUpstreamServer(dup)
	require.ErrorIs(t, err, ErrUpstreamExists)

	got, err := m.GetUpstreamServer("s")
	require.NoError(t, err)
	assert.Equal(t, []string{"180"}, got.Args)
	assert.False(t, got.Quarantined, "a refused create must not re-quarantine")
	assert.True(t, got.Created.Equal(orig.Created), "Created must not be reset")
}

func TestCreateUpstreamServer_ConcurrentSameNameExactlyOneWins(t *testing.T) {
	m := newGuardManager(t)
	var wins, exists int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := m.CreateUpstreamServer(guardServer("race", true)); {
			case err == nil:
				atomic.AddInt32(&wins, 1)
			case err == ErrUpstreamExists:
				atomic.AddInt32(&exists, 1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), wins)
	assert.Equal(t, int32(39), exists)
}

func TestCreateUpstreamServer_RecordMatchesUpsert(t *testing.T) {
	m := newGuardManager(t)
	a := guardServer("a", true)
	a.Args = []string{"x"}
	a.Env = map[string]string{"K": "V"}
	b := *a
	b.Name = "b"
	require.NoError(t, m.CreateUpstreamServer(a))
	require.NoError(t, m.SaveUpstreamServer(&b))
	ga, _ := m.GetUpstreamServer("a")
	gb, _ := m.GetUpstreamServer("b")
	ga.Name, gb.Name = "", ""
	ga.Updated, gb.Updated = time.Time{}, time.Time{}
	assert.Equal(t, gb, ga)
}
