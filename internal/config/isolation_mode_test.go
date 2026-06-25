package config

import "testing"

// TestDockerIsolationConfig_ResolvedMode_BackCompat verifies the back-compat
// mapping from the legacy global Enabled bool to the new isolation.mode enum
// (MCP-34.2): old enabled:true ⇒ mode:docker, enabled:false ⇒ mode:none, and
// an explicit Mode always wins over the legacy bool.
func TestDockerIsolationConfig_ResolvedMode_BackCompat(t *testing.T) {
	tests := []struct {
		name string
		cfg  *DockerIsolationConfig
		want IsolationMode
	}{
		{"nil config resolves to none", nil, IsolationModeNone},
		{"legacy enabled:true ⇒ docker", &DockerIsolationConfig{Enabled: true}, IsolationModeDocker},
		{"legacy enabled:false ⇒ none", &DockerIsolationConfig{Enabled: false}, IsolationModeNone},
		{"explicit mode:docker", &DockerIsolationConfig{Mode: IsolationModeDocker}, IsolationModeDocker},
		{"explicit mode:sandbox", &DockerIsolationConfig{Mode: IsolationModeSandbox}, IsolationModeSandbox},
		{"explicit mode:none", &DockerIsolationConfig{Mode: IsolationModeNone}, IsolationModeNone},
		{"explicit mode wins over legacy enabled:false", &DockerIsolationConfig{Enabled: false, Mode: IsolationModeSandbox}, IsolationModeSandbox},
		{"explicit mode:none wins over legacy enabled:true", &DockerIsolationConfig{Enabled: true, Mode: IsolationModeNone}, IsolationModeNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolvedMode(); got != tt.want {
				t.Errorf("ResolvedMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsolationMode_IsValid checks the enum validation helper.
func TestIsolationMode_IsValid(t *testing.T) {
	valid := []IsolationMode{IsolationModeDocker, IsolationModeSandbox, IsolationModeNone}
	for _, m := range valid {
		if !m.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", m)
		}
	}
	// Empty string is treated as "unset" (back-compat) and is considered valid
	// at the global level; only a non-empty unknown value is invalid.
	if !IsolationMode("").IsValid() {
		t.Errorf("IsValid(\"\") = false, want true (unset is valid)")
	}
	for _, m := range []IsolationMode{"vm", "gvisor", "DOCKER"} {
		if m.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", m)
		}
	}
}

// TestConfig_Validate_InvalidIsolationMode ensures an unknown global isolation
// mode is rejected by config validation.
func TestConfig_Validate_InvalidIsolationMode(t *testing.T) {
	c := &Config{
		DockerIsolation: &DockerIsolationConfig{Mode: IsolationMode("gvisor")},
	}
	errs := c.ValidateDetailed()
	found := false
	for _, e := range errs {
		if e.Field == "docker_isolation.mode" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected validation error for docker_isolation.mode, got %+v", errs)
	}
}

// TestConfig_Validate_InvalidPerServerIsolationMode ensures an unknown
// per-server isolation mode is rejected by config validation.
func TestConfig_Validate_InvalidPerServerIsolationMode(t *testing.T) {
	bad := IsolationMode("vm")
	c := &Config{
		Servers: []*ServerConfig{
			{
				Name:      "srv",
				Command:   "npx",
				Protocol:  "stdio",
				Isolation: &IsolationConfig{Mode: &bad},
			},
		},
	}
	errs := c.ValidateDetailed()
	found := false
	for _, e := range errs {
		if e.Field == "mcpServers[0].isolation.mode" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected validation error for per-server isolation.mode, got %+v", errs)
	}
}
