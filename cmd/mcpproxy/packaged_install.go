package main

// Packaged Linux installs (deb/rpm, GH discussion #1678).
//
// The package runs `mcpproxy serve --config=/etc/mcpproxy/mcp_config.json
// --data-dir=/var/lib/mcpproxy` as the `mcpproxy` user. A plain CLI invocation
// uses the invoking user's defaults (~/.mcpproxy), so before this file the CLI
// silently created ~/.mcpproxy/mcp_config.json and reported an empty instance
// that has nothing to do with the running service. This file:
//
//   - detects the packaged layout (Linux only) so the loaders refuse to invent
//     a per-user config and point at the service instead;
//   - implements the global --system flag, shorthand for the service's
//     --config/--data-dir that also reaches the daemon over HTTP when its
//     owner-only socket refuses the caller;
//   - builds the "different instance" warning doctor and status print.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// packagedInstallPaths is where the deb/rpm package puts the service (see
// packaging/linux/nfpm.yaml and mcpproxy.service). A struct, not constants, so
// tests point it at a temp tree and never touch the real /etc.
type packagedInstallPaths struct {
	GOOS       string
	ConfigPath string
	DataDir    string
	UnitPaths  []string
}

var packagedPaths = packagedInstallPaths{
	GOOS:       runtime.GOOS,
	ConfigPath: "/etc/mcpproxy/mcp_config.json",
	DataDir:    "/var/lib/mcpproxy",
	UnitPaths: []string{
		"/lib/systemd/system/mcpproxy.service",
		"/usr/lib/systemd/system/mcpproxy.service",
		"/etc/systemd/system/mcpproxy.service",
	},
}

// systemMode is the global --system flag.
var systemMode bool

// systemServiceActive reports whether mcpproxy.service is running. A variable
// so tests can stub systemd.
var systemServiceActive = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "mcpproxy.service").Run() == nil
}

// pathPresent reports whether path is present. A permission error counts as
// present: /etc/mcpproxy is 2770 root:mcpproxy, so a user outside the group
// cannot stat the config, yet the package is plainly installed.
func pathPresent(path string) bool {
	_, err := os.Stat(path)
	return err == nil || errors.Is(err, fs.ErrPermission)
}

// detected reports whether this host has the packaged service: Linux, the
// service config, and the systemd unit the package ships. Requiring both
// markers keeps a hand-made /etc/mcpproxy from changing CLI behaviour.
func (p packagedInstallPaths) detected() bool {
	if p.GOOS != "linux" || !pathPresent(p.ConfigPath) {
		return false
	}
	for _, u := range p.UnitPaths {
		if pathPresent(u) {
			return true
		}
	}
	return false
}

// packagedInstallError is returned by the CLI loaders on a packaged install
// when the user has no config of their own: creating one would hand them an
// empty instance unrelated to the running service.
type packagedInstallError struct {
	paths packagedInstallPaths
	hint  string
}

func (e *packagedInstallError) Error() string {
	return fmt.Sprintf(`mcpproxy runs here as a system service (mcpproxy.service), which uses
%s and %s.
You have no mcpproxy config of your own, so there is nothing for this command to manage.

To manage the service, run:
    %s
(To drop sudo, join the service group once: sudo usermod -aG mcpproxy $USER, then log in again.)

To run a separate per-user instance instead, start it with: mcpproxy serve`,
		e.paths.ConfigPath, e.paths.DataDir, e.hint)
}

// checkPackagedInstall returns a *packagedInstallError when a loader is about
// to fall back to legacy discovery (no --config, no --data-dir) on a packaged
// install where the user has no config yet.
func checkPackagedInstall() error {
	// MCPP_CONFIG names a config for config.Load (viper), like --config.
	if systemMode || dataDir != "" || os.Getenv("MCPP_CONFIG") != "" ||
		!packagedPaths.detected() || legacyConfigExists() {
		return nil
	}
	return &packagedInstallError{paths: packagedPaths, hint: systemHintCommand(os.Args[1:])}
}

// systemHintCommand rewrites the current invocation as its --system form,
// dropping path flags that --system replaces.
func systemHintCommand(args []string) string {
	out := []string{"sudo", "mcpproxy", "--system"}
	kept := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--system":
			continue
		case a == "--config" || a == "-c" || a == "--data-dir" || a == "-d":
			i++ // skip the value too
			continue
		case strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "-c=") ||
			strings.HasPrefix(a, "--data-dir=") || strings.HasPrefix(a, "-d="):
			continue
		}
		if strings.ContainsAny(a, " \t'\"") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out = append(out, a)
		kept++
	}
	if kept == 0 {
		out = append(out, "status")
	}
	return strings.Join(out, " ")
}

// applySystemFlag turns --system into the service's --config/--data-dir. It
// runs from the root PersistentPreRunE, after flag parsing.
func applySystemFlag(cmd *cobra.Command) error {
	if !systemMode {
		return nil
	}
	if packagedPaths.GOOS != "linux" {
		return fmt.Errorf("--system is only available on Linux, where the deb/rpm package installs mcpproxy as a system service")
	}
	if cmd != nil {
		if cmd.Name() == "serve" || !cmd.HasParent() {
			return fmt.Errorf("--system manages the running service; start or stop it with: sudo systemctl start mcpproxy (or stop/restart)")
		}
		for _, name := range []string{"config", "data-dir"} {
			if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
				return fmt.Errorf("--system cannot be combined with --%s: it already selects %s and %s",
					name, packagedPaths.ConfigPath, packagedPaths.DataDir)
			}
		}
	}

	f, err := os.Open(packagedPaths.ConfigPath)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf(`cannot read %s: permission denied.
The service config is readable only by root and the mcpproxy group. Either:
    run with sudo:   %s
    or join the group once: sudo usermod -aG mcpproxy $USER   (then log out and back in)`,
			packagedPaths.ConfigPath, systemHintCommand(os.Args[1:]))
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("--system: service config %s not found; is the mcpproxy deb/rpm package installed?", packagedPaths.ConfigPath)
	case err != nil:
		return fmt.Errorf("--system: %w", err)
	}
	_ = f.Close()

	configFile = packagedPaths.ConfigPath
	dataDir = packagedPaths.DataDir
	return nil
}

// cliConfigPathInUse is the config file loadCLIConfig(local) reads, without
// loading it.
func cliConfigPathInUse(local string) string {
	if p := resolveCLIConfigPath(local); p != "" {
		return p
	}
	if dataDir != "" {
		return config.GetConfigPath(dataDir)
	}
	if _, err := os.Stat(config.ConfigFileName); err == nil {
		if abs, absErr := filepath.Abs(config.ConfigFileName); absErr == nil {
			return abs
		}
	}
	if p, err := defaultHomeConfigPath(); err == nil {
		return p
	}
	return ""
}

// differentInstanceWarning is printed by doctor and status when the packaged
// service is running but the command reads another config: the user is about
// to inspect an instance that is not the service (#1678). "" when there is
// nothing to warn about.
func differentInstanceWarning(local string, args []string) string {
	if systemMode || packagedPaths.GOOS != "linux" || !packagedPaths.detected() {
		return ""
	}
	inUse := cliConfigPathInUse(local)
	if samePath(inUse, packagedPaths.ConfigPath) || !systemServiceActive() {
		return ""
	}
	return fmt.Sprintf(`Warning: you are inspecting a different mcpproxy instance than the system service.
    mcpproxy.service (active) uses %s
    this command reads             %s
    To inspect the service, run:   %s
`, packagedPaths.ConfigPath, inUse, systemHintCommand(args))
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, errA := filepath.Abs(a)
	cb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && ca == cb
}
