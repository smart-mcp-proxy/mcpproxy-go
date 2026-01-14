package stateview

import (
	"sync"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	view := New()
	require.NotNil(t, view)

	snap := view.Snapshot()
	assert.NotNil(t, snap)
	assert.NotNil(t, snap.Servers)
	assert.Equal(t, 0, len(snap.Servers))
}

func TestUpdateServer_Create(t *testing.T) {
	view := New()

	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.State = "connected"
		status.Enabled = true
		status.Connected = true
		status.Config = &config.ServerConfig{Name: "test-server"}
	})

	status, ok := view.GetServer("test-server")
	require.True(t, ok)
	assert.Equal(t, "test-server", status.Name)
	assert.Equal(t, "connected", status.State)
	assert.True(t, status.Enabled)
	assert.True(t, status.Connected)
	assert.NotNil(t, status.Config)
}

func TestUpdateServer_Update(t *testing.T) {
	view := New()

	// Create
	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.State = "connecting"
		status.Enabled = true
	})

	// Update
	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.State = "connected"
		status.Connected = true
		now := time.Now()
		status.ConnectedAt = &now
	})

	status, ok := view.GetServer("test-server")
	require.True(t, ok)
	assert.Equal(t, "connected", status.State)
	assert.True(t, status.Connected)
	assert.NotNil(t, status.ConnectedAt)
}

func TestUpdateServer_Error(t *testing.T) {
	view := New()

	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.State = "error"
		status.LastError = "connection failed"
		now := time.Now()
		status.LastErrorTime = &now
		status.RetryCount = 3
	})

	status, ok := view.GetServer("test-server")
	require.True(t, ok)
	assert.Equal(t, "error", status.State)
	assert.Equal(t, "connection failed", status.LastError)
	assert.NotNil(t, status.LastErrorTime)
	assert.Equal(t, 3, status.RetryCount)
}

func TestRemoveServer(t *testing.T) {
	view := New()

	// Create servers
	view.UpdateServer("server1", func(status *ServerStatus) {
		status.State = "connected"
	})
	view.UpdateServer("server2", func(status *ServerStatus) {
		status.State = "connected"
	})

	assert.Equal(t, 2, view.Count())

	// Remove one
	view.RemoveServer("server1")

	assert.Equal(t, 1, view.Count())
	_, ok := view.GetServer("server1")
	assert.False(t, ok)

	status, ok := view.GetServer("server2")
	require.True(t, ok)
	assert.Equal(t, "connected", status.State)
}

func TestRemoveServer_NonExistent(t *testing.T) {
	view := New()

	// Removing non-existent server should not panic or error
	view.RemoveServer("non-existent")

	assert.Equal(t, 0, view.Count())
}

func TestGetAll(t *testing.T) {
	view := New()

	view.UpdateServer("server1", func(status *ServerStatus) {
		status.State = "connected"
	})
	view.UpdateServer("server2", func(status *ServerStatus) {
		status.State = "error"
	})
	view.UpdateServer("server3", func(status *ServerStatus) {
		status.State = "idle"
	})

	all := view.GetAll()
	assert.Equal(t, 3, len(all))
	assert.Contains(t, all, "server1")
	assert.Contains(t, all, "server2")
	assert.Contains(t, all, "server3")
}

func TestCountByState(t *testing.T) {
	view := New()

	view.UpdateServer("server1", func(status *ServerStatus) {
		status.State = "connected"
		status.Connected = true
	})
	view.UpdateServer("server2", func(status *ServerStatus) {
		status.State = "connected"
		status.Connected = true
	})
	view.UpdateServer("server3", func(status *ServerStatus) {
		status.State = "error"
	})
	view.UpdateServer("server4", func(status *ServerStatus) {
		status.State = "idle"
	})

	assert.Equal(t, 2, view.CountByState("connected"))
	assert.Equal(t, 1, view.CountByState("error"))
	assert.Equal(t, 1, view.CountByState("idle"))
	assert.Equal(t, 0, view.CountByState("unknown"))
}

func TestCountConnected(t *testing.T) {
	view := New()

	view.UpdateServer("server1", func(status *ServerStatus) {
		status.Connected = true
	})
	view.UpdateServer("server2", func(status *ServerStatus) {
		status.Connected = true
	})
	view.UpdateServer("server3", func(status *ServerStatus) {
		status.Connected = false
	})

	assert.Equal(t, 2, view.CountConnected())
}

func TestSnapshot_Immutability(t *testing.T) {
	view := New()

	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.State = "connected"
		status.Enabled = true
	})

	// Get snapshot
	snap1 := view.Snapshot()
	status1 := snap1.Servers["test-server"]

	// Update server
	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.State = "error"
	})

	// Original snapshot should be unchanged
	assert.Equal(t, "connected", status1.State)
	assert.True(t, status1.Enabled)

	// New snapshot should have the update
	snap2 := view.Snapshot()
	status2 := snap2.Servers["test-server"]
	assert.Equal(t, "error", status2.State)
}

func TestConcurrentReads(t *testing.T) {
	view := New()

	// Populate with data
	for i := 0; i < 10; i++ {
		name := "server-" + string(rune('a'+i))
		view.UpdateServer(name, func(status *ServerStatus) {
			status.State = "connected"
			status.Connected = true
		})
	}

	// Concurrent reads
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := view.Snapshot()
			assert.Equal(t, 10, len(snap.Servers))

			for j := 0; j < 10; j++ {
				name := "server-" + string(rune('a'+j))
				status, ok := view.GetServer(name)
				assert.True(t, ok)
				assert.Equal(t, "connected", status.State)
			}
		}()
	}

	wg.Wait()
}

func TestConcurrentUpdates(t *testing.T) {
	view := New()

	// Concurrent updates to different servers
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		name := "server-" + string(rune('a'+i))
		go func(serverName string) {
			defer wg.Done()
			view.UpdateServer(serverName, func(status *ServerStatus) {
				status.State = "connected"
				status.Connected = true
			})
		}(name)
	}

	wg.Wait()

	// Verify all servers were created
	assert.Equal(t, 10, view.Count())
}

func TestConcurrentReadWrite(t *testing.T) {
	view := New()

	// Initial data
	view.UpdateServer("server1", func(status *ServerStatus) {
		status.State = "connected"
	})

	var wg sync.WaitGroup

	// Concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				snap := view.Snapshot()
				_ = snap.Servers
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				view.UpdateServer("server1", func(status *ServerStatus) {
					status.RetryCount = index*100 + j
				})
			}
		}(i)
	}

	wg.Wait()

	// Should not panic or deadlock
	status, ok := view.GetServer("server1")
	assert.True(t, ok)
	assert.NotNil(t, status)
}

func TestMetadata(t *testing.T) {
	view := New()

	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.Metadata["key1"] = "value1"
		status.Metadata["key2"] = 42
	})

	status, ok := view.GetServer("test-server")
	require.True(t, ok)
	assert.Equal(t, "value1", status.Metadata["key1"])
	assert.Equal(t, 42, status.Metadata["key2"])

	// Update metadata
	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.Metadata["key3"] = true
	})

	status, ok = view.GetServer("test-server")
	require.True(t, ok)
	assert.Equal(t, 3, len(status.Metadata))
	assert.Equal(t, true, status.Metadata["key3"])
}

func TestTimestamps(t *testing.T) {
	view := New()

	connectedTime := time.Now()
	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.ConnectedAt = &connectedTime
	})

	status, ok := view.GetServer("test-server")
	require.True(t, ok)
	require.NotNil(t, status.ConnectedAt)
	assert.Equal(t, connectedTime.Unix(), status.ConnectedAt.Unix())

	// Add error time
	errorTime := time.Now()
	view.UpdateServer("test-server", func(status *ServerStatus) {
		status.LastErrorTime = &errorTime
	})

	status, ok = view.GetServer("test-server")
	require.True(t, ok)
	require.NotNil(t, status.ConnectedAt)
	require.NotNil(t, status.LastErrorTime)
	assert.Equal(t, connectedTime.Unix(), status.ConnectedAt.Unix())
	assert.Equal(t, errorTime.Unix(), status.LastErrorTime.Unix())
}
