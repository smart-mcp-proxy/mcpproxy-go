//go:build windows

package winjob

import (
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Starting a child suspended closes the window between process creation and
// Job assignment (Spec 116 PR4 follow-up, T093b): a CREATE_SUSPENDED process
// runs no user code, so it cannot fork a descendant that escapes the Job. The
// caller assigns the suspended process to its Job, then calls Resume.

// MarkSuspended makes cmd start with its main thread suspended. It keeps any
// SysProcAttr fields the caller already set. Call it before cmd.Start.
func MarkSuspended(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// IsSuspended reports whether cmd was marked to start suspended.
func IsSuspended(cmd *exec.Cmd) bool {
	return cmd != nil && cmd.SysProcAttr != nil &&
		cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED != 0
}

var (
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	procNtResumeProc = ntdll.NewProc("NtResumeProcess")
)

// processSuspendResume is PROCESS_SUSPEND_RESUME, which x/sys/windows does
// not export.
const processSuspendResume = 0x0800

// Resume lets a process started with CREATE_SUSPENDED run, using
// NtResumeProcess (it resumes the main thread, the only one that exists).
func Resume(pid int) error {
	proc, err := windows.OpenProcess(processSuspendResume, false, uint32(pid)) //nolint:gosec // pid is a live PID from exec.Cmd.Process.Pid
	if err != nil {
		return fmt.Errorf("OpenProcess(%d) for resume: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()

	if err := procNtResumeProc.Find(); err != nil {
		return fmt.Errorf("NtResumeProcess unavailable: %w", err)
	}
	status, _, _ := procNtResumeProc.Call(uintptr(proc))
	if status != 0 { // STATUS_SUCCESS
		return fmt.Errorf("NtResumeProcess(%d): NTSTATUS 0x%x", pid, status)
	}
	return nil
}

// AssignAndResume adds a suspended process to the job and only then lets it
// run, so everything it ever spawns is a member of the job. On an error the
// process is still suspended; the caller kills it.
func (j *Job) AssignAndResume(pid int) error {
	if err := j.Assign(pid); err != nil {
		return err
	}
	return Resume(pid)
}
