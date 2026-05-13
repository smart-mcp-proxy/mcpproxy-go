package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Host-environment hint surfaced by `mcpproxy doctor` when the combination
// "snap-installed Docker" + "service runs as a system user with HOME outside
// /home" + "at least one configured upstream uses docker" is detected.
//
// On such hosts, the deb's packaged systemd hardening fights snap-confine on
// four orthogonal axes (HOME location, ProtectHome=true masking /run/user/<uid>,
// CapabilityBoundingSet too narrow, NoNewPrivileges blocking the SUID hop into
// snap-confine). Without an override drop-in, every docker-based upstream
// produces a several-paragraph stderr blob that's hard to map back to a fix.
//
// See https://github.com/smart-mcp-proxy/mcpproxy-go/issues/457 for the trace.

// snapDockerEnvInputs is the set of observations the warning decision is
// computed from. Keeping it a plain struct makes the decision testable without
// running on a real host.
type snapDockerEnvInputs struct {
	SnapDockerPresent      bool
	ServiceHomeOutsideHome bool
	HasDockerUpstream      bool
}

// ShouldWarn reports whether the doctor output should include the override
// hint. All three signals must be true — any one missing means the user is
// not affected by this specific incompatibility.
func (i snapDockerEnvInputs) ShouldWarn() bool {
	return i.SnapDockerPresent && i.ServiceHomeOutsideHome && i.HasDockerUpstream
}

// homeOutsideHome decides whether a given HOME path triggers snap-confine's
// "home directories outside of /home" rejection. Empty HOME is treated as
// "inside" so we don't false-positive on hosts where we couldn't read the
// service's environment.
func homeOutsideHome(home string) bool {
	if home == "" {
		return false
	}
	return !strings.HasPrefix(home, "/home/")
}

// hasDockerUpstream inspects the daemon's server list (the shape returned by
// GET /api/v1/servers, decoded as []map[string]interface{}) and reports
// whether any configured upstream would invoke `docker`.
//
// Three patterns count:
//
//  1. command == "docker" (direct stdio docker)
//  2. command == "/bin/bash" (or similar shell) whose args contain "docker"
//     — the kubic linkedin-direct pattern that sources an env file then execs
//     docker run.
//  3. docker_isolation == true regardless of command — the Spec-027 Docker
//     isolation flag spawns docker run on any upstream.
//
// Disabled servers still count because re-enabling them in the UI shouldn't
// surprise the user with a fresh stderr storm.
func hasDockerUpstream(servers []map[string]interface{}) bool {
	for _, s := range servers {
		if iso, ok := s["docker_isolation"].(bool); ok && iso {
			return true
		}
		cmd, _ := s["command"].(string)
		if cmd == "docker" {
			return true
		}
		// Shell wrappers — check args for `docker`.
		if cmd == "/bin/bash" || cmd == "/bin/sh" || cmd == "bash" || cmd == "sh" {
			args, _ := s["args"].([]interface{})
			for _, a := range args {
				if str, ok := a.(string); ok && strings.Contains(str, "docker") {
					return true
				}
			}
		}
	}
	return false
}

// detectSnapDockerPresent returns true if snap-installed docker is on the
// host. Cheap stat — no exec needed. Falls back to `docker version` parse
// only when the canonical /snap/bin/docker is absent (some installs symlink
// from /usr/local/bin or vary by snap version).
func detectSnapDockerPresent(ctx context.Context) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/snap/bin/docker"); err == nil {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "docker", "version", "--format", "{{.Server.Platform.Name}}").Output()
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(out), []byte("snap"))
}

// detectServiceHome returns the effective HOME of the mcpproxy systemd
// service ("" if it cannot be read, e.g. service not under systemd or we
// aren't on Linux). Uses `systemctl show` because it's the only way to
// observe the unit's resolved env without running anything as the service
// user. Non-root reads are fine — show is a read-only introspection call.
func detectServiceHome(ctx context.Context) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "systemctl", "show", "mcpproxy.service", "--property=Environment").Output()
	if err != nil {
		return ""
	}
	// Output format: "Environment=HEADLESS=1 HOME=/var/lib/mcpproxy ..."
	line := strings.TrimSpace(strings.TrimPrefix(string(out), "Environment="))
	for _, kv := range strings.Fields(line) {
		if rest, found := strings.CutPrefix(kv, "HOME="); found {
			return rest
		}
	}
	return ""
}

// displayEnvironmentHint prints the host-environment hint in pretty doctor
// output. No-op when hint is empty so the caller doesn't need to gate it.
func displayEnvironmentHint(hint string) {
	if hint == "" {
		return
	}
	fmt.Println("🌐 Environment")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println(hint)
	fmt.Println()
}

// snapDockerHint returns the canonical override snippet, ready for the user
// to copy-paste verbatim. Asserted token-by-token in tests so wording can
// drift but the operationally-critical lines can't.
func snapDockerHint() string {
	return `⚠️  Snap-docker incompatibility detected

This host has Docker installed via snap, and the mcpproxy service is running
under a system user whose HOME sits outside /home/. One or more of your
upstream MCP servers shells out to "docker run", which will fail under the
deb's default hardening with stderr like:

    snap-confine ... required permitted capability cap_dac_override not found
    cannot create XDG_RUNTIME_DIR folder "/run/user/<uid>/snap.docker"

Fix (one-time host setup):

    sudo loginctl enable-linger mcpproxy
    sudo snap set system homedirs=/var/lib
    sudo systemctl restart snapd
    sudo snap restart docker

Then drop /etc/systemd/system/mcpproxy.service.d/snap-docker.conf with:

    [Service]
    NoNewPrivileges=false
    RestrictSUIDSGID=false
    LockPersonality=false
    ProtectHome=false
    CapabilityBoundingSet=~
    AmbientCapabilities=
    RuntimeDirectory=mcpproxy
    RuntimeDirectoryMode=0700

…and: sudo systemctl daemon-reload && sudo systemctl restart mcpproxy

Why each line is needed: see docs/getting-started/installation.md
(section: Migrating from a manually-installed mcpproxy) or the project's
issue tracker for the full root-cause breakdown.`
}
