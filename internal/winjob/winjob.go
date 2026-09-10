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
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObjectExtendedLimitInformation / JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
// mirror the WinAPI JobObjectInfoClass / limit-flag constants. Kept local
// (rather than relying on x/sys/windows, which does not export the
// Set/QueryInformationJobObject calls themselves) so this file only needs
// the raw kernel32 procedure lookups below.
const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	stillActive                       = 259 // STILL_ACTIVE, per GetExitCodeProcess docs
)

var (
	modkernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procSetInformationJobObj  = modkernel32.NewProc("SetInformationJobObject")
)

// jobobjectBasicLimitInformation mirrors JOBOBJECT_BASIC_LIMIT_INFORMATION.
// Only LimitFlags is meaningful here; the rest exists purely so the struct
// has the layout Win32 expects when we hand it a pointer.
type jobobjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// jobobjectExtendedLimitInformation mirrors JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobobjectExtendedLimitInformation struct {
	BasicLimitInformation jobobjectBasicLimitInformation
	IoInfo                [48]byte // IO_COUNTERS — unused, present for correct struct size
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// Job wraps a Windows Job Object configured to kill every member process
// (including any it spawns after joining) as soon as Close is called.
type Job struct {
	handle windows.Handle
}

// New creates a job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE set.
func New() (*Job, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}

	info := jobobjectExtendedLimitInformation{
		BasicLimitInformation: jobobjectBasicLimitInformation{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	r1, _, callErr := procSetInformationJobObj.Call(
		uintptr(handle),
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)), //nolint:gosec // required shape for the Win32 call
		unsafe.Sizeof(info),
	)
	if r1 == 0 {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("SetInformationJobObject: %w", callErr)
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
// Safe to call more than once and safe to call on a nil *Job.
func (j *Job) Close() error {
	if j == nil || j.handle == 0 {
		return nil
	}
	// Kill first (reaches every member immediately); closing the handle
	// after that is just resource cleanup, not what does the killing.
	_ = windows.TerminateJobObject(j.handle, 1)
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}

// IsProcessAlive reports whether pid still exists and has not exited.
// Used for the isProcessGroupAlive check that process_windows.go previously
// hardcoded to false.
func IsProcessAlive(pid int) bool {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(proc) }()

	var code uint32
	if err := windows.GetExitCodeProcess(proc, &code); err != nil {
		return false
	}
	return code == stillActive
}
