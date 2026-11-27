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

// MarkSuspended makes cmd start with its main thread suspended, in its own
// console process group: a CTRL_BREAK that asks the core to shut down must not
// reach the children, which would fail the calls still running on them. It
// keeps any SysProcAttr fields the caller already set. Call it before cmd.Start.
func MarkSuspended(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
}

// RequestExit asks the process group led by pid, a process started through
// MarkSuspended, to exit. It is the closest Windows has to SIGTERM: CTRL_BREAK
// ends a program that does not handle it. It fails when the caller does not
// share a console with the group.
func RequestExit(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// HoldPID keeps Windows from giving pid to another process until release: an
// open handle to a process reserves its PID even after the process is reaped.
// Call it while the process is still unreaped. held is false when the process
// could not be opened, so its PID may already be another process's.
func HoldPID(pid int) (release func(), held bool) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)) //nolint:gosec // pid is a live PID from exec.Cmd.Process.Pid
	if err != nil {
		return func() {}, false
	}
	return func() { _ = windows.CloseHandle(h) }, true
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
