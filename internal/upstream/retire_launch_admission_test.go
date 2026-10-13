package upstream

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR4, T4.1 for the two other production launch branches (Sol
// review round 2): a Docker stdio launch (a mock docker binary) and a
// locally launched HTTP upstream (the launcher). Each instance is parked
// before its launch is admitted, a commit removes the server and returns, the
// instance is released: no `docker run` and no launcher marker. Each has an
// unretired control, so zero is the barrier and not a broken fixture.

// mockDocker writes a docker stand-in into dir: every `docker run` creates
// dir/marker-<pid> and idles; any other subcommand (the stale-container
// sweep) succeeds with no output. It is also put first on PATH and the
// daemon socket is pointed nowhere, so no real Docker is ever reached.
func mockDocker(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock docker is a shell script")
	}
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	path := filepath.Join(bin, "docker")
	script := "#!/bin/sh\n" +
		`echo "$1" >> "` + dir + `/docker-calls"` + "\n" +
		`if [ "$1" = run ]; then touch "` + dir + `/marker-$$"; sleep 30; fi` + "\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755)) //nolint:gosec // test fixture must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(dir, "no-docker.sock"))
	restore := core.SetDockerBinaryResolverForTest(func(*zap.Logger) (string, error) { return path, nil })
	t.Cleanup(restore)
	return path
}

func dockerRuns(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "docker-calls"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "run" {
			n++
		}
	}
	return n
}

func dockerConfig(name, docker string) *config.ServerConfig {
	return &config.ServerConfig{
		Name:     name,
		Command:  docker,
		Args:     []string{"run", "-i", "--rm", "retire-test-image"},
		Protocol: "stdio",
		Enabled:  true,
	}
}

// launcherConfig is a locally launched HTTP upstream whose command is the
// marker shell: it never serves its URL, so an unretired connect fails at
// wait-for-url after it launched.
func launcherConfig(t *testing.T, name, dir string) *config.ServerConfig {
	t.Helper()
	cfg := markerCommand(t, name, dir)
	cfg.Protocol = "http"
	cfg.URL = "http://127.0.0.1:1/mcp" // nothing listens on port 1
	cfg.LauncherWaitTimeout = config.Duration(500 * time.Millisecond)
	return cfg
}

// observeLaunches counts the processes core's launch sections start for
// server. Unlike a marker file it sees a child the gate kills before the
// child ran a single instruction.
func observeLaunches(t *testing.T, server string) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	restore := core.SetLaunchObserverForTest(func(s, _ string) {
		if s == server {
			n.Add(1)
		}
	})
	t.Cleanup(restore)
	return &n
}

// retireParkedLaunch registers cfg, parks its first admitted section, starts
// a connect, removes the server through a commit and releases the instance.
// Nothing is launched: no process is started (observer), none ran (markers).
func retireParkedLaunch(t *testing.T, cfg *config.ServerConfig) {
	t.Helper()
	launches := observeLaunches(t, cfg.Name)
	defer func() { assert.Zero(t, launches.Load(), "no process is started for the removed server") }()
	m := newGenTestManager(t)
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)
	t.Cleanup(func() { _ = inst.Disconnect() })

	parked, release := parkFirstAdmit(inst)
	defer release()
	errc := connectAsync(inst)
	select {
	case <-parked:
	case <-time.After(15 * time.Second):
		t.Fatal("the launch was never admitted: the fixture does not reach this branch")
	}

	res := m.AdvanceGenerations([]GenerationChange{{Name: cfg.Name, Present: false}})
	require.Len(t, res.Unregistered, 1)
	require.True(t, inst.Gate().Retired(), "the commit retires the instance before it returns")

	release()
	require.Error(t, waitErr(t, errc), "a retired instance cannot connect")
	time.Sleep(300 * time.Millisecond) // a launched child would have touched its marker by now
}

func connectUnretired(t *testing.T, cfg *config.ServerConfig) *managed.Client {
	t.Helper()
	launches := observeLaunches(t, cfg.Name)
	defer func() { assert.Positive(t, launches.Load(), "control: the launch section starts a process") }()
	m := newGenTestManager(t)
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)
	t.Cleanup(func() { _ = inst.Disconnect() })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_ = inst.Connect(ctx) // neither fixture is an MCP server
	return inst
}

func TestRetire_DockerLaunchParked_RemovalRunsNoContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	docker := mockDocker(t, dir)
	retireParkedLaunch(t, dockerConfig("docker-parked", docker))
	assert.Zero(t, dockerRuns(t, dir), "no `docker run` for the removed server")
	assert.Zero(t, markers(t, dir))
}

func TestRetire_DockerLaunchControl_RunsWhenNotRetired(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	docker := mockDocker(t, dir)
	connectUnretired(t, dockerConfig("docker-ctl", docker))
	require.Eventually(t, func() bool { return dockerRuns(t, dir) > 0 && markers(t, dir) > 0 },
		5*time.Second, 50*time.Millisecond, "control: an unretired Docker launch runs the mock `docker run`")
}

func TestRetire_LauncherParked_RemovalLaunchesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	retireParkedLaunch(t, launcherConfig(t, "launcher-parked", dir))
	assert.Zero(t, markers(t, dir), "the removed server's launcher must not run")
}

func TestRetire_LauncherControl_LaunchesWhenNotRetired(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	connectUnretired(t, launcherConfig(t, "launcher-ctl", dir))
	require.Eventually(t, func() bool { return markers(t, dir) > 0 }, 5*time.Second, 50*time.Millisecond,
		"control: an unretired launcher runs its command")
}
