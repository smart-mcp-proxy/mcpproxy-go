package config

import (
	"path/filepath"
	"runtime"
)

// testAbsPath turns a slash-separated, POSIX-style absolute path used by the
// tool_output_roots tests into an absolute path on the current OS, so the same
// cases hold on Windows ("/tmp/x" -> `C:\tmp\x`, "/" -> `C:\`).
func testAbsPath(p string) string {
	if runtime.GOOS == "windows" {
		return `C:` + filepath.FromSlash(p)
	}
	return p
}
