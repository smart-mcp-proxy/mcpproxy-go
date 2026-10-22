//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A path swapped for a never-written FIFO between the lstat and the open must
// be refused without blocking: a plain open(2) of a FIFO blocks until a writer
// appears, before any post-open check can run (Sol 6.1 review, round 2).
func TestReadBoundedRefusesAFIFOSwappedInBeforeOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := beforeOpenHook
	beforeOpenHook = func(path string) {
		_ = os.Remove(path)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Errorf("mkfifo: %v", err)
		}
	}
	t.Cleanup(func() { beforeOpenHook = prev })

	done := make(chan error, 1)
	go func() { _, err := readBounded(p, 1024); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("want a not-regular refusal, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readBounded blocked on a FIFO swapped in before the open")
	}
}
