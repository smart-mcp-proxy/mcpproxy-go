package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdioServer is an enabled stdio server with an optional per-server isolation
// mode override (empty = inherit the global mode).
func stdioServer(name string, mode IsolationMode) *ServerConfig {
	s := &ServerConfig{Name: name, Command: "npx", Args: []string{"-y", name}, Protocol: "stdio", Enabled: true}
	if mode != "" {
		m := mode
		s.Isolation = &IsolationConfig{Mode: &m}
	}
	return s
}

// GH #1704: a stdio child runs as the proxy's user. Under isolation none or
// sandbox it can read the data directory and, while enable_socket is on, use
// the admin socket without a key (a sandboxed child minted an agent token over
// it). The doctor finding must name every such server and the fix.
func TestDoctorFindings_StdioSocketExposure_NamesUnisolatedServers(t *testing.T) {
	cfg := &Config{
		Listen:       "127.0.0.1:8080",
		EnableSocket: true,
		Servers: []*ServerConfig{
			stdioServer("inherits-none", ""),
			stdioServer("explicit-sandbox", IsolationModeSandbox),
			stdioServer("explicit-docker", IsolationModeDocker),
			{Name: "remote-http", URL: "https://example.com/mcp", Protocol: "http", Enabled: true},
			{Name: "disabled-stdio", Command: "uvx", Protocol: "stdio", Enabled: false},
			{Name: "runs-docker-itself", Command: "docker", Args: []string{"run", "img"}, Protocol: "stdio", Enabled: true},
		},
	}
	findings := DoctorFindings(cfg)
	require.Len(t, findings, 1, "%v", findings)
	f := findings[0]
	assert.Contains(t, f, `"inherits-none"`)
	assert.Contains(t, f, `"explicit-sandbox"`)
	assert.NotContains(t, f, "explicit-docker", "docker isolation hides the data dir and the socket")
	assert.NotContains(t, f, "remote-http", "no child process")
	assert.NotContains(t, f, "disabled-stdio", "never spawned")
	assert.NotContains(t, f, "runs-docker-itself", "the server itself runs in a container")
	assert.Contains(t, f, "enable_socket", "the finding must name the switch")
	assert.Contains(t, f, "docker", "the finding must point at docker isolation")
	assert.Equal(t, 1, strings.Count(f, "\n")+1, "one line per finding")
}

func TestDoctorFindings_StdioSocketExposure_SilentWhenSocketOff(t *testing.T) {
	cfg := &Config{
		Listen:       "127.0.0.1:8080",
		EnableSocket: false,
		Servers:      []*ServerConfig{stdioServer("inherits-none", "")},
	}
	assert.Empty(t, DoctorFindings(cfg))
}

func TestDoctorFindings_StdioSocketExposure_SilentUnderGlobalDocker(t *testing.T) {
	cfg := &Config{
		Listen:          "127.0.0.1:8080",
		EnableSocket:    true,
		DockerIsolation: &DockerIsolationConfig{Mode: IsolationModeDocker},
		Servers:         []*ServerConfig{stdioServer("inherits-docker", "")},
	}
	assert.Empty(t, DoctorFindings(cfg))
}

// A per-server legacy enabled:false opt-out downgrades an active global docker
// mode to none for that server, so it must be named.
func TestDoctorFindings_StdioSocketExposure_NamesPerServerOptOut(t *testing.T) {
	optOut := stdioServer("opted-out", "")
	optOut.Isolation = &IsolationConfig{Enabled: BoolPtr(false)}
	cfg := &Config{
		Listen:          "127.0.0.1:8080",
		EnableSocket:    true,
		DockerIsolation: &DockerIsolationConfig{Mode: IsolationModeDocker},
		Servers:         []*ServerConfig{optOut, stdioServer("inherits-docker", "")},
	}
	findings := DoctorFindings(cfg)
	require.Len(t, findings, 1, "%v", findings)
	assert.Contains(t, findings[0], `"opted-out"`)
	assert.NotContains(t, findings[0], "inherits-docker")
}

func TestDoctorFindings_StdioSocketExposure_NoStdioServers(t *testing.T) {
	cfg := &Config{
		Listen:       "127.0.0.1:8080",
		EnableSocket: true,
		Servers:      []*ServerConfig{{Name: "remote", URL: "https://example.com/mcp", Protocol: "http", Enabled: true}},
	}
	assert.Empty(t, DoctorFindings(cfg))
}
