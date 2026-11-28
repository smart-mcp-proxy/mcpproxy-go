//go:build darwin || windows

package main

import (
	"fmt"
	"testing"
	"time"
)

func TestAcquireSingleInstanceRefusesSecondHolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // darwin: keep the lock file off the developer's ~/.mcpproxy
	id := fmt.Sprintf("mcpproxy-tray-test-%d", time.Now().UnixNano())

	first, err := acquireSingleInstance(id)
	if err != nil || !first {
		t.Fatalf("first acquire = %v, %v; want true, nil", first, err)
	}
	second, err := acquireSingleInstance(id)
	if err != nil || second {
		t.Fatalf("second acquire = %v, %v; want false, nil", second, err)
	}
}
