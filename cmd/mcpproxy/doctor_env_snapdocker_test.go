package main

import (
	"strings"
	"testing"
)

// Pure-logic unit tests for the snap-docker environment hint. The detection
// itself runs against the live host (proc, /snap/bin/docker, the daemon's
// server list) — the part exercised here is only the decision: given a set
// of observations, should `mcpproxy doctor` print the override snippet?
//
// Keeping detection inputs separate from the decision lets us add new probes
// (e.g. checking AppArmor profile state) without churning the test matrix.

func TestSnapDockerEnvInputs_ShouldWarn(t *testing.T) {
	cases := []struct {
		name string
		in   snapDockerEnvInputs
		want bool
	}{
		{
			name: "all signals present — warn",
			in:   snapDockerEnvInputs{SnapDockerPresent: true, ServiceHomeOutsideHome: true, HasDockerUpstream: true},
			want: true,
		},
		{
			name: "no snap docker on host — no warn (apt docker handles HOME fine)",
			in:   snapDockerEnvInputs{SnapDockerPresent: false, ServiceHomeOutsideHome: true, HasDockerUpstream: true},
			want: false,
		},
		{
			name: "service runs under /home — no warn (manual install pattern)",
			in:   snapDockerEnvInputs{SnapDockerPresent: true, ServiceHomeOutsideHome: false, HasDockerUpstream: true},
			want: false,
		},
		{
			name: "no docker upstreams configured — no warn (snap docker never invoked)",
			in:   snapDockerEnvInputs{SnapDockerPresent: true, ServiceHomeOutsideHome: true, HasDockerUpstream: false},
			want: false,
		},
		{
			name: "zero signals — no warn",
			in:   snapDockerEnvInputs{},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.ShouldWarn(); got != tc.want {
				t.Errorf("ShouldWarn()=%v, want %v (inputs=%+v)", got, tc.want, tc.in)
			}
		})
	}
}

func TestHomeOutsideHome(t *testing.T) {
	cases := []struct {
		name     string
		home     string
		expected bool
	}{
		{"deb's system user", "/var/lib/mcpproxy", true},
		{"alt sysuser dir", "/var/lib/something", true},
		{"empty HOME treated as inside (don't false-positive)", "", false},
		{"human user under /home", "/home/alice", false},
		{"root", "/root", true},
		{"trailing slash on /home prefix counted as inside", "/home/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := homeOutsideHome(tc.home); got != tc.expected {
				t.Errorf("homeOutsideHome(%q)=%v, want %v", tc.home, got, tc.expected)
			}
		})
	}
}

func TestHasDockerUpstreamFromServers(t *testing.T) {
	cases := []struct {
		name    string
		servers []map[string]interface{}
		want    bool
	}{
		{
			name: "direct command: docker",
			servers: []map[string]interface{}{
				{"name": "duckduckgo", "command": "docker", "enabled": true},
			},
			want: true,
		},
		{
			name: "bash wrapper that execs docker run",
			servers: []map[string]interface{}{
				{"name": "linkedin", "command": "/bin/bash", "args": []interface{}{"-c", "set -a; source /etc/x.env; exec docker run -i --rm img"}, "enabled": true},
			},
			want: true,
		},
		{
			name: "stdio server with no docker — irrelevant",
			servers: []map[string]interface{}{
				{"name": "tavily", "command": "uvx", "args": []interface{}{"tavily-mcp"}, "enabled": true},
			},
			want: false,
		},
		{
			name: "docker upstream but disabled — still relevant (will reconnect when enabled)",
			servers: []map[string]interface{}{
				{"name": "duckduckgo", "command": "docker", "enabled": false},
			},
			want: true,
		},
		{
			name: "docker_isolation true on otherwise-non-docker server (Spec docker-isolation flag)",
			servers: []map[string]interface{}{
				{"name": "x", "command": "uvx", "args": []interface{}{"y"}, "docker_isolation": true, "enabled": true},
			},
			want: true,
		},
		{
			name:    "no servers at all",
			servers: nil,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDockerUpstream(tc.servers); got != tc.want {
				t.Errorf("hasDockerUpstream()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestSnapDockerHintContent(t *testing.T) {
	hint := snapDockerHint()
	// Just sanity — make sure the rendered hint contains the critical commands
	// users need to copy-paste. The exact wording is intentionally not asserted
	// to keep wordsmith edits cheap.
	required := []string{
		"loginctl enable-linger mcpproxy",
		"snap set system homedirs=/var/lib",
		"systemctl restart snapd",
		"/etc/systemd/system/mcpproxy.service.d/snap-docker.conf",
		"ProtectHome=false",
		"NoNewPrivileges=false",
		"CapabilityBoundingSet=~",
	}
	for _, want := range required {
		if !strings.Contains(hint, want) {
			t.Errorf("snapDockerHint() missing required token %q", want)
		}
	}
}
