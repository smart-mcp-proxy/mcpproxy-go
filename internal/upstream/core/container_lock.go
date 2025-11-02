package core

import (
	"sync"
)

// ContainerLock provides per-server locking for container creation
// This prevents race conditions where multiple goroutines try to create
// containers for the same server simultaneously (e.g., during ForceReconnectAll)
type ContainerLock struct {
	locks sync.Map // serverName -> *sync.Mutex
}

// Lock acquires a lock for the specified server name
// Returns a mutex that the caller MUST unlock when done
func (cl *ContainerLock) Lock(serverName string) *sync.Mutex {
	mutex, _ := cl.locks.LoadOrStore(serverName, &sync.Mutex{})
	m := mutex.(*sync.Mutex)
	m.Lock()
	return m
}

// globalContainerLock is the global instance used by all clients
var globalContainerLock = &ContainerLock{}
