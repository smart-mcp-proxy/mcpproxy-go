//go:build windows

package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Two accounts in one logon session (Run as different user) must not turn
// each other's tray away, so the mutex name carries the user's SID.
func TestSingleInstanceMutexNameIsPerUser(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	name, err := singleInstanceMutexName("mcpproxy-tray")
	if err != nil {
		t.Fatalf("singleInstanceMutexName: %v", err)
	}
	if want := `Local\mcpproxy-tray-` + user.User.Sid.String(); name != want {
		t.Fatalf("mutex name = %q, want %q", name, want)
	}
	if !strings.HasPrefix(name, `Local\`) {
		t.Fatalf("mutex name %q must stay per logon session", name)
	}
}
