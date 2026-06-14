package main

import (
	"path/filepath"
	"testing"
)

// TestResolveServeLogDir documents the precedence that keeps non-default-data-dir
// runs (Go tests, e2e scripts, FE/QA harnesses) from polluting the shared prod
// log at ~/Library/Logs/mcpproxy/main.log — the root cause of the phantom
// "core restarts every 10s" reports in MCP-2250.
func TestResolveServeLogDir(t *testing.T) {
	const defaultDataDir = "/home/u/.mcpproxy"

	cases := []struct {
		name        string
		explicit    string
		configLog   string
		dataDir     string
		defaultData string
		want        string
	}{
		{
			name:        "explicit --log-dir always wins",
			explicit:    "/custom/logs",
			configLog:   "/cfg/logs",
			dataDir:     "/tmp/test-123",
			defaultData: defaultDataDir,
			want:        "/custom/logs",
		},
		{
			name:        "config Logging.LogDir wins when no flag",
			explicit:    "",
			configLog:   "/cfg/logs",
			dataDir:     "/tmp/test-123",
			defaultData: defaultDataDir,
			want:        "/cfg/logs",
		},
		{
			name:        "default data dir keeps OS-standard log dir (empty => GetLogDir)",
			explicit:    "",
			configLog:   "",
			dataDir:     defaultDataDir,
			defaultData: defaultDataDir,
			want:        "",
		},
		{
			name:        "non-default data dir co-locates logs under <data-dir>/logs",
			explicit:    "",
			configLog:   "",
			dataDir:     "/tmp/mcpproxy-test-Foo",
			defaultData: defaultDataDir,
			want:        filepath.Join("/tmp/mcpproxy-test-Foo", "logs"),
		},
		{
			name:        "relative non-default data dir (harness ./test-data) co-locates",
			explicit:    "",
			configLog:   "",
			dataDir:     "./test-data",
			defaultData: defaultDataDir,
			want:        filepath.Join("test-data", "logs"),
		},
		{
			name:        "absolute spelling of default data dir is treated as default",
			explicit:    "",
			configLog:   "",
			dataDir:     "/home/u/../u/.mcpproxy",
			defaultData: defaultDataDir,
			want:        "",
		},
		{
			name:        "empty data dir is a no-op (OS-standard)",
			explicit:    "",
			configLog:   "",
			dataDir:     "",
			defaultData: defaultDataDir,
			want:        "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveServeLogDir(tc.explicit, tc.configLog, tc.dataDir, tc.defaultData)
			// Compare cleaned, since the helper may return a joined relative path.
			if filepath.Clean(got) != filepath.Clean(tc.want) {
				t.Fatalf("resolveServeLogDir(%q,%q,%q,%q) = %q, want %q",
					tc.explicit, tc.configLog, tc.dataDir, tc.defaultData, got, tc.want)
			}
		})
	}
}
