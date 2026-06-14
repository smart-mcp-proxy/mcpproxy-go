package main

import (
	"os"
	"path/filepath"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// defaultDataDirPath returns the default data directory (<home>/.mcpproxy),
// mirroring internal/config's default resolution. It is used to decide whether
// the resolved data dir is "non-default" for log co-location. On failure to
// resolve the home dir it returns config.DefaultDataDir, which will simply not
// match any absolute data dir and leave logs at the OS-standard location.
func defaultDataDirPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.DefaultDataDir
	}
	return filepath.Join(home, config.DefaultDataDir)
}

// resolveServeLogDir decides the log directory for the `serve` command.
//
// Precedence:
//  1. explicit --log-dir flag (explicitLogDir) — always wins.
//  2. a log dir already set in the loaded config (configLogDir).
//  3. for a NON-default data dir, co-locate logs under <data-dir>/logs.
//  4. otherwise "" — meaning the OS-standard location resolved by
//     internal/logs.GetLogDir (e.g. ~/Library/Logs/mcpproxy on macOS).
//
// Step 3 is the fix for MCP-2250: Go integration tests, e2e scripts, and the
// FE/QA Playwright harnesses all run `mcpproxy serve` with a custom data dir
// (temp dirs, ./test-data, /tmp/mcpproxy-*) but, because the log dir was
// derived purely from $HOME, every one of them appended to the SAME shared
// prod log at ~/Library/Logs/mcpproxy/main.log. A reader of that file saw
// dozens of fast boots interleaved and mistook it for the core restarting
// ~every 10s. Co-locating logs with a non-default data dir keeps those runs
// self-contained and leaves the real prod log clean. The default data dir
// (~/.mcpproxy) is unchanged: it still logs to the OS-standard location so the
// tray and documented paths keep working.
func resolveServeLogDir(explicitLogDir, configLogDir, dataDir, defaultDataDir string) string {
	if explicitLogDir != "" {
		return explicitLogDir
	}
	if configLogDir != "" {
		return configLogDir
	}
	if dataDir != "" && !sameDir(dataDir, defaultDataDir) {
		return filepath.Join(dataDir, "logs")
	}
	return ""
}

// sameDir reports whether two paths refer to the same directory, comparing
// their cleaned absolute forms so that relative/aliased spellings of the
// default data dir are still recognized as the default.
func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}
