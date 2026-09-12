//go:build windows

// Package winjob provides Windows Job Object based process-tree cleanup.
//
// Unix has process groups: kill(-pgid, sig) reaches every descendant a
// spawned command has forked. Windows has no equivalent for an arbitrary
// process tree — Process.Kill() only ever reaches the one PID mcpproxy
// itself started (e.g. cmd.exe), never the grandchildren cmd.exe/npx/bash
// go on to spawn (node.exe, python.exe, the real MCP server). Those
// grandchildren were left running forever every time a stdio server was
// restarted, reconnected, or disconnected — see
// internal/upstream/core/process_windows.go and
// internal/upstream/launcher/launcher_windows.go, both previously TODO
// stubs that only killed the immediate child.
//
// A Job Object created with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE restores
// the process-group-kill guarantee on Windows: once the immediate child is
// assigned to the job, every process IT spawns afterwards automatically
// joins the same job (job membership is inherited on process creation), so
// terminating the job reaches the whole tree in one call — matching what
// killProcessGroup already does on Unix via kill(-pgid, ...).
package winjob

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job wraps a Windows Job Object configured to kill every member process
// (including any it spawns after joining) as soon as Close is called.
//
// Close may be reached from more than one goroutine at once — the
// launcher's Stop path and its reaper both close the job — so the handle
// is guarded by a mutex and Close is idempotent.
type Job struct {
	mu     sync.Mutex
	handle windows.Handle
}

// New creates a job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE set.
func New() (*Job, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}

	// x/sys/windows exports the struct, the info class and the limit flag,
	// so the layout (including the arch-specific trailing pad on 386/arm)
	// is the library's problem, not ours.
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), //nolint:gosec // required shape for the Win32 call
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	return &Job{handle: handle}, nil
}

// Assign adds the process with the given PID to the job. Must be called as
// soon as possible after the process starts — everything it spawns AFTER
// joining inherits job membership, but anything it spawns BEFORE joining
// (a race in principle) would escape. In practice the caller starts the
// child and assigns it within the same goroutine with no intervening work,
// so the window is negligible — the child (a shell/npx wrapper) has not
// yet had a chance to exec its own grandchild.
func (j *Job) Assign(pid int) error {
	if j == nil {
		return fmt.Errorf("winjob: nil job")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return fmt.Errorf("winjob: job already closed")
	}

	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // pid is always a live PID from exec.Cmd.Process.Pid
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()

	if err := windows.AssignProcessToJobObject(j.handle, proc); err != nil {
		return fmt.Errorf("AssignProcessToJobObject(%d): %w", pid, err)
	}
	return nil
}

// Close terminates every process still in the job and releases the handle.
// Safe to call more than once, concurrently, and on a nil *Job.
func (j *Job) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	// Kill first (reaches every member immediately); closing the handle
	// after that is just resource cleanup, not what does the killing.
	_ = windows.TerminateJobObject(j.handle, 1)
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}
