package telemetry

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// isLoginItemParent returns true when the parent process is the OS
// login-item launcher for the current platform. See research.md R3.
//
//   - macOS: parent name == "launchd". We do not additionally check
//     LSBackgroundOnly — in the tray build we always run with an accessory
//     activation policy, so PPID=launchd is a sufficiently strong signal.
//   - Windows: parent name == "explorer.exe". (Registry Run-key apps inherit
//     explorer.exe as PPID.)
//   - Linux / anything else: always false (no tray autostart integration).
//
// Errors are swallowed — a failed lookup maps to false rather than crashing
// the telemetry path.
func isLoginItemParent() bool {
	name, ok := parentProcessName()
	if !ok {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	switch runtime.GOOS {
	case "darwin":
		return name == "launchd"
	case "windows":
		return name == "explorer.exe" || name == "explorer"
	default:
		return false
	}
}

// parentProcessName returns the parent process's executable name for the
// current platform. Implemented via `ps` on POSIX; returns (name, true) on
// success. On Windows we currently fall back to empty — the tray autostart
// pathway there is a follow-up (see tasks.md US3 deferred scope).
func parentProcessName() (string, bool) {
	ppid := os.Getppid()
	if ppid <= 0 {
		return "", false
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		// `ps -o comm= -p <ppid>` is portable across macOS/Linux BSD/GNU ps.
		out, err := exec.Command("ps", "-o", "comm=", "-p", intToString(ppid)).Output()
		if err != nil {
			return "", false
		}
		s := strings.TrimSpace(string(out))
		// macOS may return an absolute path like "/sbin/launchd"; take basename.
		if i := strings.LastIndex(s, "/"); i >= 0 {
			s = s[i+1:]
		}
		return s, true
	default:
		return "", false
	}
}

// intToString is a tiny allocation-free int-to-decimal formatter used for
// the ps argument. Avoiding fmt lets the module stay out of the hot path's
// reflect table.
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
