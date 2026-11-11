//go:build windows

package secret

import (
	"fmt"
	"path/filepath"
	"strings"
)

func rejectPlatformAbsolutePath(path string) error {
	if isForbiddenDeviceNamespace(path) {
		return fmt.Errorf("secret file path %q uses a forbidden prefix", path)
	}
	if isForbiddenUNCPath(path) {
		return fmt.Errorf("secret file path %q uses a forbidden prefix", path)
	}
	vol := filepath.VolumeName(path)
	if vol != "" && len(vol) == 2 && vol[1] == ':' {
		afterVol := strings.TrimPrefix(path, vol)
		if afterVol == "" {
			return fmt.Errorf("secret file path %q must be absolute", path)
		}
		if afterVol[0] != '\\' && afterVol[0] != '/' {
			// Drive-relative path such as C:foo (no root after the colon).
			return fmt.Errorf("secret file path %q must be absolute", path)
		}
	}
	if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) {
		return fmt.Errorf("secret file path %q must be absolute", path)
	}
	return nil
}

// isForbiddenDeviceNamespace reports Win32 device-namespace spellings (\\?\, //?/,
// and mixed separator forms) that must not be used for secret file reads.
func isForbiddenDeviceNamespace(path string) bool {
	if len(path) < 4 {
		return false
	}
	pairs := [][2]byte{{'\\', '\\'}, {'/', '/'}, {'\\', '/'}, {'/', '\\'}}
	for _, p := range pairs {
		if path[0] == p[0] && path[1] == p[1] {
			if (path[2] == '?' || path[2] == '.') && (path[3] == '\\' || path[3] == '/') {
				return true
			}
		}
	}
	return false
}

// isForbiddenUNCPath reports UNC paths (\\server\share, //server/share, and
// mixed separator spellings). VolumeName returns a UNC prefix longer than a
// drive letter, or starting with two separators.
func isForbiddenUNCPath(path string) bool {
	vol := filepath.VolumeName(path)
	if vol != "" {
		if len(vol) > 2 {
			return true
		}
		if len(vol) >= 2 && vol[0] == '\\' && vol[1] == '\\' {
			return true
		}
	}
	if len(path) < 3 {
		return false
	}
	a, b := path[0], path[1]
	if (a != '\\' && a != '/') || (b != '\\' && b != '/') {
		return false
	}
	if a == b && (path[2] == '?' || path[2] == '.') {
		return false // device namespace
	}
	if a == b {
		return true // \\server or //server
	}
	// Mixed // or \/ at the start (UNC spelling).
	return true
}
