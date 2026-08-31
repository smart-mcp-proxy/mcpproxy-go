package config

import (
	"strings"
	"testing"
)

// withSandboxEnforceable swaps the host-capability probe for the duration of a
// test so the sandbox-mode matrix can be asserted on any platform.
func withSandboxEnforceable(t *testing.T, available bool) {
	t.Helper()
	prev := sandboxEnforceable
	sandboxEnforceable = func() bool { return available }
	t.Cleanup(func() { sandboxEnforceable = prev })
}

// TestResolveIsolation_SandboxUnenforceable is the read/spawn parity guard for
// GH #1142: `core.Client.wrapWithSandbox` returns the command UNCHANGED when
// the host cannot enforce Landlock (every non-Linux OS, and Linux kernels
// without the LSM), so the process runs unconfined. Reporting isolated:true in
// that case is a false isolation claim on exactly the surface this fix exists
// to make trustworthy.
//
// Mode stays "sandbox" — the spawn path still wraps for best-effort rlimits on
// Linux — but Isolated must tell the truth about confinement.
func TestResolveIsolation_SandboxUnenforceable(t *testing.T) {
	sandbox := IsolationModeSandbox
	server := &ServerConfig{
		Name:      "srv",
		Command:   "npx",
		Isolation: &IsolationConfig{Mode: &sandbox},
	}

	t.Run("host cannot enforce the sandbox", func(t *testing.T) {
		withSandboxEnforceable(t, false)
		got := ResolveIsolation(&DockerIsolationConfig{Enabled: true}, server)
		if got.Mode != IsolationModeSandbox {
			t.Errorf("Mode = %q, want %q (the spawn path still takes the sandbox branch)", got.Mode, IsolationModeSandbox)
		}
		if got.Isolated {
			t.Errorf("Isolated = true, want false: wrapWithSandbox runs the server unconfined on this host")
		}
		if got.Source != IsolationSourceSandboxUnavailable {
			t.Errorf("Source = %q, want %q", got.Source, IsolationSourceSandboxUnavailable)
		}
	})

	t.Run("host can enforce the sandbox", func(t *testing.T) {
		withSandboxEnforceable(t, true)
		got := ResolveIsolation(&DockerIsolationConfig{Enabled: true}, server)
		if got.Mode != IsolationModeSandbox || !got.Isolated {
			t.Errorf("want sandbox/isolated, got Mode=%q Isolated=%v", got.Mode, got.Isolated)
		}
		if got.Source != IsolationSourceServerMode {
			t.Errorf("Source = %q, want %q", got.Source, IsolationSourceServerMode)
		}
	})
}

// TestResolveIsolation_EmptyModeOverrideInherits pins the documented meaning of
// an empty per-server mode: "unset", i.e. inherit the global setting. The
// resolver used to treat a pointer-to-"" as an explicit override, producing
// mode="" isolated=true source=server-mode while the spawn path (which matches
// on "docker"/"sandbox") ran the server unconfined (GH #1142).
func TestResolveIsolation_EmptyModeOverrideInherits(t *testing.T) {
	empty := IsolationMode("")
	server := &ServerConfig{
		Name:      "srv",
		Command:   "npx",
		Isolation: &IsolationConfig{Mode: &empty},
	}

	got := ResolveIsolation(&DockerIsolationConfig{Enabled: true}, server)
	if got.Mode != IsolationModeDocker {
		t.Errorf("Mode = %q, want %q (an empty mode inherits the global setting)", got.Mode, IsolationModeDocker)
	}
	if !got.Isolated {
		t.Errorf("Isolated = false, want true (global docker isolation applies)")
	}
	if !got.Inherited {
		t.Errorf("Inherited = false, want true (an empty mode is not an override)")
	}
	if got.Source != IsolationSourceGlobal {
		t.Errorf("Source = %q, want %q", got.Source, IsolationSourceGlobal)
	}
}

// TestResolveIsolation_UnrecognizedModeIsNotIsolation covers a mode the spawn
// path does not implement (a hand-edited config, or a value that predates a
// downgrade). The launcher matches only "docker" and "sandbox", so anything
// else runs unconfined and must not be reported as isolated.
func TestResolveIsolation_UnrecognizedModeIsNotIsolation(t *testing.T) {
	bogus := IsolationMode("Docker") // wrong case: not the docker mode
	server := &ServerConfig{
		Name:      "srv",
		Command:   "npx",
		Isolation: &IsolationConfig{Mode: &bogus},
	}

	got := ResolveIsolation(&DockerIsolationConfig{Enabled: true}, server)
	if got.Isolated {
		t.Errorf("Isolated = true for mode %q, want false: the spawn path implements no such mode", bogus)
	}
	if got.Source != IsolationSourceUnsupportedMode {
		t.Errorf("Source = %q, want %q", got.Source, IsolationSourceUnsupportedMode)
	}
}

// TestValidateIsolationModeOverride is the shared gate both write surfaces
// (REST `mode_override`, MCP `isolation_json`) call so an unknown mode is
// rejected at the seam instead of being persisted and failing the NEXT daemon
// start's config validation.
func TestValidateIsolationModeOverride(t *testing.T) {
	mode := func(s string) *IsolationMode { m := IsolationMode(s); return &m }

	tests := []struct {
		name    string
		in      *IsolationMode
		wantErr bool
	}{
		{name: "nil is inherit", in: nil},
		{name: "empty is unset", in: mode("")},
		{name: "docker", in: mode("docker")},
		{name: "sandbox", in: mode("sandbox")},
		{name: "none", in: mode("none")},
		{name: "unknown", in: mode("bogus"), wantErr: true},
		{name: "wrong case", in: mode("Docker"), wantErr: true},
		{name: "trailing space", in: mode("docker "), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIsolationModeOverride(tt.in)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateIsolationModeOverride(%v) = nil, want an error", tt.in)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateIsolationModeOverride(%v) = %v, want nil", tt.in, err)
			}
			if tt.wantErr {
				// The message must name the accepted vocabulary so a typo is
				// self-diagnosing from the API response alone.
				for _, want := range []string{"docker", "sandbox", "none"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name the accepted value %q", err, want)
					}
				}
			}
		})
	}
}
