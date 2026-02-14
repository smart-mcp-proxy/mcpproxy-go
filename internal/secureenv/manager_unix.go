//go:build !windows

package secureenv

// discoverWindowsPathsFromRegistry is a stub for non-Windows platforms
// On Unix/macOS, this function is never called because discoverWindowsPaths()
// is only called when runtime.GOOS == "windows"
func discoverWindowsPathsFromRegistry() []string {
	// This should never be called on non-Windows platforms
	return nil
}
