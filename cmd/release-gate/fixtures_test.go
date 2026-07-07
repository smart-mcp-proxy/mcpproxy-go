package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestFixtureAlive_DetectsSelfExit guards the FR-010 retry-restore path: a
// driver-owned fixture that dies on its own must read as not-alive once reaped,
// otherwise prepareRetry never restarts it and retries keep hitting a dead port.
func TestFixtureAlive_DetectsSelfExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gate fixtures run on ubuntu-latest; liveness probe uses a POSIX shell")
	}
	logPath := filepath.Join(t.TempDir(), "fx.log")
	f, err := startFixture("selfexit", "/bin/sh", []string{"-c", "exit 0"}, 0, logPath)
	if err != nil {
		t.Fatalf("startFixture: %v", err)
	}
	select {
	case <-f.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not exit/reap within 5s")
	}
	if f.alive() {
		t.Error("alive() must be false after the process self-exits and is reaped")
	}
}

// TestFixtureAlive_TrueUntilKilled confirms alive() stays true while the
// process runs and flips to false after kill() reaps it.
func TestFixtureAlive_TrueUntilKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gate fixtures run on ubuntu-latest; liveness probe uses a POSIX shell")
	}
	logPath := filepath.Join(t.TempDir(), "fx.log")
	f, err := startFixture("longrun", "/bin/sh", []string{"-c", "sleep 30"}, 0, logPath)
	if err != nil {
		t.Fatalf("startFixture: %v", err)
	}
	if !f.alive() {
		t.Fatal("alive() must be true while the process runs")
	}
	f.kill()
	if f.alive() {
		t.Error("alive() must be false after kill()")
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("fixture log missing after run: %v", err)
	}
}
