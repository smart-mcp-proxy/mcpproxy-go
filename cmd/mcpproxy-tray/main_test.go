//go:build darwin

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
)

func TestShellQuote(t *testing.T) {
	tcases := map[string]string{
		"":         "''",
		"simple":   "'simple'",
		"with spa": "'with spa'",
		"a'b":      "'a'\\''b'",
	}

	for input, expected := range tcases {
		if got := shellQuote(input); got != expected {
			t.Fatalf("shellQuote(%q) = %q, expected %q", input, got, expected)
		}
	}
}

func TestBuildShellExecCommand(t *testing.T) {
	cmd := buildShellExecCommand("/usr/local/bin/mcpproxy", []string{"serve", "--listen", "127.0.0.1:8080"})
	expected := "exec '/usr/local/bin/mcpproxy' 'serve' '--listen' '127.0.0.1:8080'"
	if cmd != expected {
		t.Fatalf("buildShellExecCommand produced %q, expected %q", cmd, expected)
	}
}

func TestTrayListenFromArgs(t *testing.T) {
	tcases := []struct {
		args     []string
		expected string
	}{
		{nil, ""},
		{[]string{"serve"}, ""},
		{[]string{"serve", "--listen", "0.0.0.0:8181"}, "0.0.0.0:8181"},
		{[]string{"serve", "--listen=0.0.0.0:8181"}, "0.0.0.0:8181"},
		{[]string{"-l", ":9090"}, ":9090"},
		{[]string{"-l=:9090"}, ":9090"},
		{[]string{"--listen"}, ""},                                    // dangling flag without a value
		{[]string{"-l"}, ""},                                          // dangling short flag without a value
		{[]string{"--listen", "--config", "path"}, ""},                // flag value must not be another flag
		{[]string{"--listen=", "--listen", ":8181"}, ":8181"},         // empty value skipped, scanning continues
		{[]string{"--listen", "--config", "--listen=:9090"}, ":9090"}, // malformed value skipped, later valid one wins
	}

	for _, tc := range tcases {
		if got := trayListenFromArgs(tc.args); got != tc.expected {
			t.Fatalf("trayListenFromArgs(%v) = %q, expected %q", tc.args, got, tc.expected)
		}
	}
}

func TestBuildCoreArgs_ForwardsCLIListenOverSocketEndpoint(t *testing.T) {
	t.Setenv("MCPPROXY_TRAY_CONFIG_PATH", "")
	t.Setenv("MCPPROXY_TRAY_LISTEN", "")
	t.Setenv("MCPPROXY_TRAY_EXTRA_ARGS", "")

	original := trayCLIListen
	defer func() { trayCLIListen = original }()

	// Regression: `mcpproxy-tray serve --listen 0.0.0.0:8181` used to drop the
	// listen flag whenever the tray talked to the core over the unix socket,
	// so the core fell back to the config default (127.0.0.1:8080).
	trayCLIListen = "0.0.0.0:8181"
	args := buildCoreArgs("unix:///tmp/mcpproxy.sock")
	expected := []string{"serve", "--listen", "0.0.0.0:8181"}
	if strings.Join(args, " ") != strings.Join(expected, " ") {
		t.Fatalf("buildCoreArgs with CLI listen = %v, expected %v", args, expected)
	}

	// Without a CLI listen flag, socket endpoints still get no --listen.
	trayCLIListen = ""
	args = buildCoreArgs("unix:///tmp/mcpproxy.sock")
	expected = []string{"serve"}
	if strings.Join(args, " ") != strings.Join(expected, " ") {
		t.Fatalf("buildCoreArgs without CLI listen = %v, expected %v", args, expected)
	}
}

// TestNormalizeListen is the security-adjacent invariant: normalizeListen must
// never WIDEN a bind. It used to strip the loopback host ("127.0.0.1:8181" ->
// ":8181"), which handed the core an all-interfaces bind for an address the
// user had explicitly pinned to loopback — and that value is forwarded verbatim
// as the core's --listen from both the MCPPROXY_TRAY_LISTEN and the CLI path.
func TestNormalizeListen(t *testing.T) {
	tcases := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"loopback host is preserved, not stripped", "127.0.0.1:8181", "127.0.0.1:8181"},
		{"loopback host with surrounding space", "  127.0.0.1:8181  ", "127.0.0.1:8181"},
		{"localhost is preserved", "localhost:8181", "localhost:8181"},
		{"explicit LAN host is preserved", "192.168.1.10:8181", "192.168.1.10:8181"},
		{"IPv6 loopback is preserved", "[::1]:8181", "[::1]:8181"},
		{"bare port defaults to loopback, never all interfaces", "8181", "127.0.0.1:8181"},
		{"explicit all-interfaces is honored", "0.0.0.0:8181", "0.0.0.0:8181"},
		{"explicit wildcard port form is honored", ":8181", ":8181"},
		{"IPv6 wildcard is honored", "[::]:8181", "[::]:8181"},
		{"unparseable value passes through for the core to reject", "not a listen addr", "not a listen addr"},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeListen(tc.input); got != tc.expected {
				t.Fatalf("normalizeListen(%q) = %q, expected %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestCoreURLFromListen(t *testing.T) {
	tcases := []struct {
		name     string
		protocol string
		listen   string
		expected string
	}{
		{"empty", "http", "", ""},
		{"loopback", "http", "127.0.0.1:8181", "http://127.0.0.1:8181"},
		{"bare port", "http", "8181", "http://127.0.0.1:8181"},
		{"wildcard dials loopback", "http", "0.0.0.0:8181", "http://127.0.0.1:8181"},
		{"bare wildcard dials loopback", "http", ":8181", "http://127.0.0.1:8181"},
		{"IPv6 wildcard dials loopback", "http", "[::]:8181", "http://127.0.0.1:8181"},
		{"explicit host is dialed as given", "https", "192.168.1.10:8181", "https://192.168.1.10:8181"},
		{"IPv6 host keeps brackets", "http", "[::1]:8181", "http://[::1]:8181"},
		// A zone must be percent-escaped in a URL, otherwise url.Parse rejects
		// the result with "invalid URL escape" and every later parse of the
		// core URL fails.
		{"IPv6 zone is percent-escaped", "http", "[fe80::1%en0]:8181", "http://[fe80::1%25en0]:8181"},
		{"garbage yields no URL", "http", "not a listen addr", ""},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			if got := coreURLFromListen(tc.protocol, tc.listen); got != tc.expected {
				t.Fatalf("coreURLFromListen(%q, %q) = %q, expected %q", tc.protocol, tc.listen, got, tc.expected)
			}
		})
	}
}

// TestCoreURLFromListen_AlwaysParseable is the invariant behind the escaping:
// whatever coreURLFromListen returns must survive url.Parse, because the tray
// re-parses the core URL for health checks, listenArgFromURL and the
// port-conflict handler.
func TestCoreURLFromListen_AlwaysParseable(t *testing.T) {
	listens := []string{
		"8181", "127.0.0.1:8181", "localhost:8181", "192.168.1.10:8181",
		":8181", "0.0.0.0:8181", "[::]:8181", "[::1]:8181", "[fe80::1%en0]:8181",
	}

	for _, listen := range listens {
		got := coreURLFromListen("http", listen)
		if got == "" {
			t.Fatalf("coreURLFromListen(http, %q) returned no URL", listen)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("url.Parse(coreURLFromListen(http, %q) = %q): %v", listen, got, err)
		}
		if u.Port() != "8181" {
			t.Fatalf("coreURLFromListen(http, %q) = %q, port = %q, expected 8181", listen, got, u.Port())
		}
	}
}

// TestResolveCoreTCPURL covers the no-socket fallback: the core is launched with
// the pinned --listen, so the tray must probe that same address instead of the
// 127.0.0.1:8080 default.
func TestResolveCoreTCPURL(t *testing.T) {
	t.Run("CLI listen wins over env and default", func(t *testing.T) {
		t.Setenv("MCPPROXY_TLS_ENABLED", "")
		t.Setenv("MCPPROXY_TRAY_LISTEN", "127.0.0.1:9000")
		t.Setenv("MCPPROXY_TRAY_PORT", "9100")

		if got := resolveCoreTCPURL("0.0.0.0:8181"); got != "http://127.0.0.1:8181" {
			t.Fatalf("resolveCoreTCPURL with CLI listen = %q, expected http://127.0.0.1:8181", got)
		}
	})

	t.Run("env listen keeps its host", func(t *testing.T) {
		t.Setenv("MCPPROXY_TLS_ENABLED", "")
		t.Setenv("MCPPROXY_TRAY_LISTEN", "127.0.0.1:9000")
		t.Setenv("MCPPROXY_TRAY_PORT", "")

		if got := resolveCoreTCPURL(""); got != "http://127.0.0.1:9000" {
			t.Fatalf("resolveCoreTCPURL with env listen = %q, expected http://127.0.0.1:9000", got)
		}
	})

	t.Run("port env still honored", func(t *testing.T) {
		t.Setenv("MCPPROXY_TLS_ENABLED", "")
		t.Setenv("MCPPROXY_TRAY_LISTEN", "")
		t.Setenv("MCPPROXY_TRAY_PORT", "9100")

		if got := resolveCoreTCPURL(""); got != "http://127.0.0.1:9100" {
			t.Fatalf("resolveCoreTCPURL with port env = %q, expected http://127.0.0.1:9100", got)
		}
	})

	t.Run("TLS switches the scheme", func(t *testing.T) {
		t.Setenv("MCPPROXY_TLS_ENABLED", "true")
		t.Setenv("MCPPROXY_TRAY_LISTEN", "")
		t.Setenv("MCPPROXY_TRAY_PORT", "")

		if got := resolveCoreTCPURL("127.0.0.1:8181"); got != "https://127.0.0.1:8181" {
			t.Fatalf("resolveCoreTCPURL with TLS = %q, expected https://127.0.0.1:8181", got)
		}
		if got := resolveCoreTCPURL(""); got != "https://127.0.0.1:8080" {
			t.Fatalf("resolveCoreTCPURL default with TLS = %q, expected https://127.0.0.1:8080", got)
		}
	})

	t.Run("nothing pinned falls back to the default", func(t *testing.T) {
		t.Setenv("MCPPROXY_TLS_ENABLED", "")
		t.Setenv("MCPPROXY_TRAY_LISTEN", "")
		t.Setenv("MCPPROXY_TRAY_PORT", "")

		if got := resolveCoreTCPURL(""); got != defaultCoreURL {
			t.Fatalf("resolveCoreTCPURL fallback = %q, expected %q", got, defaultCoreURL)
		}
	})
}

// TestBuildCoreArgs_LoopbackListenIsNotWidened pins the end-to-end consequence
// of the normalizeListen fix: a loopback-pinned tray must not hand the core an
// all-interfaces --listen.
func TestBuildCoreArgs_LoopbackListenIsNotWidened(t *testing.T) {
	t.Setenv("MCPPROXY_TRAY_CONFIG_PATH", "")
	t.Setenv("MCPPROXY_TRAY_EXTRA_ARGS", "")

	original := trayCLIListen
	defer func() { trayCLIListen = original }()

	// CLI path.
	t.Setenv("MCPPROXY_TRAY_LISTEN", "")
	trayCLIListen = "127.0.0.1:8181"
	args := buildCoreArgs("unix:///tmp/mcpproxy.sock")
	expected := []string{"serve", "--listen", "127.0.0.1:8181"}
	if strings.Join(args, " ") != strings.Join(expected, " ") {
		t.Fatalf("buildCoreArgs (CLI loopback listen) = %v, expected %v", args, expected)
	}

	// Env path (TCP core URL, no listen derivable from a hostless URL).
	trayCLIListen = ""
	t.Setenv("MCPPROXY_TRAY_LISTEN", "127.0.0.1:8181")
	args = buildCoreArgs("http://")
	if strings.Join(args, " ") != strings.Join(expected, " ") {
		t.Fatalf("buildCoreArgs (env loopback listen) = %v, expected %v", args, expected)
	}
}

// TestPinnedCoreListen documents which listen sources are treated as a user
// contract that must not be silently moved on a port conflict.
func TestPinnedCoreListen(t *testing.T) {
	original := trayCLIListen
	defer func() { trayCLIListen = original }()

	trayCLIListen = "127.0.0.1:8181"
	if got := pinnedCoreListen(); got != "127.0.0.1:8181" {
		t.Fatalf("pinnedCoreListen() with CLI listen = %q, expected 127.0.0.1:8181", got)
	}

	trayCLIListen = "8181"
	if got := pinnedCoreListen(); got != "127.0.0.1:8181" {
		t.Fatalf("pinnedCoreListen() normalizes bare port = %q, expected 127.0.0.1:8181", got)
	}

	// The env listen flows through coreURL, so the auto-bump can still move it.
	trayCLIListen = ""
	t.Setenv("MCPPROXY_TRAY_LISTEN", "127.0.0.1:8181")
	if got := pinnedCoreListen(); got != "" {
		t.Fatalf("pinnedCoreListen() with only env listen = %q, expected \"\"", got)
	}
}

func TestNewTrayLogConfig_DarwinUsesConsoleAndRotationDefaults(t *testing.T) {
	cfg := newTrayLogConfig(platformDarwin, "/tmp/tray-logs")

	if cfg.Level != logs.LogLevelInfo {
		t.Fatalf("Level = %q, expected %q", cfg.Level, logs.LogLevelInfo)
	}
	if !cfg.EnableFile {
		t.Fatal("EnableFile = false, expected true")
	}
	if !cfg.EnableConsole {
		t.Fatal("EnableConsole = false, expected true on darwin")
	}
	if cfg.Filename != "tray.log" {
		t.Fatalf("Filename = %q, expected tray.log", cfg.Filename)
	}
	if cfg.LogDir != "/tmp/tray-logs" {
		t.Fatalf("LogDir = %q, expected /tmp/tray-logs", cfg.LogDir)
	}
	if !cfg.JSONFormat {
		t.Fatal("JSONFormat = false, expected true")
	}
	if cfg.MaxSize != 10 || cfg.MaxBackups != 5 || cfg.MaxAge != 30 || !cfg.Compress {
		t.Fatalf("rotation defaults = size:%d backups:%d age:%d compress:%t, expected 10/5/30/true", cfg.MaxSize, cfg.MaxBackups, cfg.MaxAge, cfg.Compress)
	}
}

func TestNewTrayLogConfig_WindowsDisablesConsole(t *testing.T) {
	cfg := newTrayLogConfig(platformWindows, `C:\logs`)

	if cfg.EnableConsole {
		t.Fatal("EnableConsole = true, expected false on windows")
	}
	if !cfg.EnableFile {
		t.Fatal("EnableFile = false, expected true")
	}
	if cfg.Filename != "tray.log" {
		t.Fatalf("Filename = %q, expected tray.log", cfg.Filename)
	}
	if cfg.LogDir != `C:\logs` {
		t.Fatalf("LogDir = %q, expected C:\\logs", cfg.LogDir)
	}
}

func TestSetupLogging_WritesTrayLogToRotatingFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	logger, err := setupLogging()
	if err != nil {
		t.Fatalf("setupLogging(): %v", err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	logger.Info("rotation test message")
	_ = logger.Sync()

	logPath := filepath.Join(tempHome, "Library", "Logs", "mcpproxy", "tray.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", logPath, err)
	}
	if !strings.Contains(string(content), "\"message\":\"rotation test message\"") {
		t.Fatalf("tray.log did not contain expected JSON message: %s", string(content))
	}
}

// TestShouldTerminateCore is the ownership invariant behind GH #410: the tray
// may only kill a core IT launched.
//
// Before this, coreOwnershipExternalManaged — "a core was already running, so I
// attached to it" — still fell through to shutdownExternalCoreFallback() and
// ensureCoreTermination(), which look the PID up from /status and even
// `pgrep -f "mcpproxy serve"` for stragglers. So starting a core under
// launchd/brew and then opening the tray meant that quitting the tray killed
// the user's core. Only MCPPROXY_TRAY_SKIP_CORE opted out of that.
func TestShouldTerminateCore(t *testing.T) {
	tests := []struct {
		name      string
		ownership coreOwnershipMode
		want      bool
	}{
		{"tray launched it, so tray cleans it up", coreOwnershipTrayManaged, true},
		{"attached to a core we found running", coreOwnershipExternalManaged, false},
		{"explicitly told not to manage the core", coreOwnershipExternalUnmanaged, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTerminateCore(tt.ownership); got != tt.want {
				t.Errorf("shouldTerminateCore(%v) = %v, want %v", tt.ownership, got, tt.want)
			}
		})
	}
}
