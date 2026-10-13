//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

func setProcessGroup(_ *exec.Cmd) {}

// killProcessGroup kills the process and its descendants (taskkill /T); the
// CI lane runs on Linux, this keeps a local Windows run from leaking the test
// binary on cancellation.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
