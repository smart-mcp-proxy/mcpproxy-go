package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
)

// Codex round 2 (PR E), docker finding 1. `a/b` and hidden `a-b` both
// generate mcpproxy-a-b-<suffix>; on a suffix collision Docker refuses the
// run with its own message naming the FOREIGN container's name and full id:
//
//	docker: Error response from daemon: Conflict. The container name
//	"/mcpproxy-a-b-wxyz" is already in use by container "<64 hex>". …
//
// That text reaches a/b's per-server log as child output — the docker CLI's
// stderr on the stdio isolation path (monitorStderr) and the launcher-pumped
// stderr of a user-supplied `docker run` upstream (loggerWriter). Neither
// producer may write it as the record MESSAGE (D8 rule 1), both must stamp
// it `child_output=true` (logs.ChildOutputField), and the attributed reader
// tail_log uses must withhold a child-output record that mentions a
// container unless container_owner matches. The pre-spawn "Docker isolation
// configured" record names a container that does not exist yet, so it must
// not carry container_owner for that unverified name.

const (
	collisionRequester   = "a/b" // the server whose log is read
	collisionHiddenOwner = "a-b" // owns the container Docker names in its refusal
	collisionForeignID   = "f0e1d2c3b4a5968778695a4b3c2d1e0ff0e1d2c3b4a5968778695a4b3c2d1e0f"
	collisionForeignName = "mcpproxy-a-b-wxyz"
)

// requireCollisionPremise pins what makes the fixture a collision: the two
// raw names sanitise to the same container-name stem, and the foreign name
// is exactly that stem with a generated suffix — so nothing in a/b's own
// records can tell the colliding container from its own.
func requireCollisionPremise(t *testing.T) {
	t.Helper()
	require.Equal(t, sanitizeServerNameForContainer(collisionRequester), sanitizeServerNameForContainer(collisionHiddenOwner),
		"fixture premise: a/b and a-b must generate the same container-name stem")
	require.Equal(t, "mcpproxy-"+sanitizeServerNameForContainer(collisionHiddenOwner)+"-wxyz", collisionForeignName,
		"fixture premise: the foreign name is a-b's canonical container name")
}

// dockerCollisionStderr is the docker CLI's stderr for a name conflict,
// verbatim shape.
var dockerCollisionStderr = `docker: Error response from daemon: Conflict. The container name "/` + collisionForeignName +
	`" is already in use by container "` + collisionForeignID + `". You have to remove (or rename) that container to be able to reuse that name.`

// newRealPerServerLogger returns the REAL per-server file writer for name
// (what client.go installs as upstreamLogger) over a fresh log directory,
// plus the LogConfig the readers take.
func newRealPerServerLogger(t *testing.T, name string) (*zap.Logger, *config.LogConfig) {
	t.Helper()
	logDir, err := os.MkdirTemp("", "mcpproxy-collision-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(logDir) })
	cfg := logs.DefaultLogConfig()
	cfg.LogDir = logDir
	cfg.EnableFile = true
	cfg.EnableConsole = false
	cfg.Compress = false
	require.NoError(t, os.WriteFile(filepath.Join(logDir, logs.ServerLogFilename(name)), nil, 0o600))
	logger, closer, err := logs.NewUpstreamServerLogger(cfg, name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	return logger, cfg
}

// assertCollisionWithheldFromScopedReader is the shared oracle: the scoped
// reader (what tail_log serves an a/b-scoped token) never returns the
// foreign id or name, the administrator whole-file reader keeps them
// (SC-005), and an ordinary record of a/b's stays served.
func assertCollisionWithheldFromScopedReader(t *testing.T, cfg *config.LogConfig, name string) {
	t.Helper()
	scoped, err := logs.ReadUpstreamServerLogTailAttributed(cfg, name, 50)
	require.NoError(t, err)
	body := strings.Join(scoped, "\n")
	assert.NotContains(t, body, collisionForeignID, "foreign container id served to %s's scoped reader:\n%s", name, body)
	assert.NotContains(t, body, collisionForeignName, "foreign container name served to %s's scoped reader:\n%s", name, body)
	assert.NotContains(t, body, "already in use by container", "Docker's collision text served to %s's scoped reader:\n%s", name, body)
	assert.Contains(t, body, "ordinary-record-of-a-b", "a/b's own ordinary record must stay served")

	whole, err := logs.ReadUpstreamServerLogTail(cfg, name, 50)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(whole, "\n"), collisionForeignID, "administrators keep Docker's output (SC-005)")
}

// Launcher path, end to end: a user-supplied `docker run --name …` upstream
// for server a/b spawned through connectWithLauncher against a fake docker
// whose `run` answers with the daemon's conflict message. The child's stderr
// is pumped through loggerWriter into the REAL per-server file; the scoped
// reader must not disclose the foreign container.
func TestDockerRunCollision_LauncherStderr_NeverAttributedToRequester(t *testing.T) {
	requireCollisionPremise(t)
	fd := installFakeDocker(t, nil)
	fd.failRunWith(t, dockerCollisionStderr)
	forceDockerDaemonEnvGOOS(t, osDarwin)

	const name = collisionRequester
	upstreamLogger, logCfg := newRealPerServerLogger(t, name)
	mainCore, mainLogs := observer.New(zap.DebugLevel)
	c := &Client{
		config: &config.ServerConfig{
			Name:                name,
			Protocol:            "http",
			URL:                 "http://127.0.0.1:1/mcp",
			Command:             "docker",
			Args:                []string{"run", "-i", "--rm", "--name", collisionForeignName, "mcp/example"},
			LauncherWaitTimeout: config.Duration(200 * time.Millisecond),
		},
		logger:           zap.New(mainCore),
		upstreamLogger:   upstreamLogger,
		isolationManager: NewIsolationManager(config.DefaultDockerIsolationConfig()),
		envManager:       secureenv.NewManager(nil),
	}
	upstreamLogger.Info("ordinary-record-of-a-b")

	c.mu.Lock()
	err := c.connectWithLauncher(context.Background())
	c.mu.Unlock()
	require.Error(t, err, "the fake docker run fails, so the launcher cannot reach the URL")
	_ = upstreamLogger.Sync()

	invocations := strings.Join(fd.invocations(t), "\n")
	require.Contains(t, invocations, "run ", "fixture premise: docker run was invoked:\n%s", invocations)
	whole, err := logs.ReadUpstreamServerLogTail(logCfg, name, 50)
	require.NoError(t, err)
	require.Contains(t, strings.Join(whole, "\n"), collisionForeignID,
		"fixture premise: the docker CLI's stderr reached the per-server log:\n%s", strings.Join(whole, "\n"))

	assertCollisionWithheldFromScopedReader(t, logCfg, name)
	// Docker's text is a field value on every launcher record, never the message.
	for _, entry := range mainLogs.All() {
		assert.NotContains(t, entry.Message, collisionForeignID, "child text written as a record MESSAGE: %q", entry.Message)
	}
}

// Stdio isolation path: the docker CLI is the stdio child and its stderr is
// pumped by monitorStderr into the real per-server file.
func TestDockerRunCollision_StdioStderr_NeverAttributedToRequester(t *testing.T) {
	requireCollisionPremise(t)
	const name = collisionRequester
	upstreamLogger, logCfg := newRealPerServerLogger(t, name)
	c := &Client{
		config:         &config.ServerConfig{Name: name},
		logger:         zap.NewNop(),
		upstreamLogger: upstreamLogger,
	}
	upstreamLogger.Info("ordinary-record-of-a-b")

	c.monitorStderr(context.Background(), strings.NewReader(dockerCollisionStderr+"\nlistening on 127.0.0.1:9331\n"))
	_ = upstreamLogger.Sync()

	assertCollisionWithheldFromScopedReader(t, logCfg, name)
	scoped, err := logs.ReadUpstreamServerLogTailAttributed(logCfg, name, 50)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(scoped, "\n"), "listening on 127.0.0.1:9331", "ordinary child stderr stays served")
}

// Producer shape, launcher: the child's line is the `message` FIELD of a
// constant-message record stamped child_output=true.
func TestLoggerWriter_ChildLineIsStampedFieldValue(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	w := newLoggerWriter(zap.New(core), nil)
	_, err := w.Write([]byte("[launcher stderr] " + dockerCollisionStderr + "\n"))
	require.NoError(t, err)

	entries := observed.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "launcher", entries[0].Message, "the record message must be a constant, never child text")
	assert.Equal(t, "[launcher stderr] "+dockerCollisionStderr, entries[0].ContextMap()["message"])
	assert.Equal(t, true, entries[0].ContextMap()["child_output"], "child output must be stamped for the attributed reader")
}

// Producer shape, stdio: the per-server stderr record is stamped
// child_output=true (the message-as-field shape predates this round).
func TestMonitorStderr_StampsChildOutput(t *testing.T) {
	perServerCore, perServerLogs := observer.New(zap.DebugLevel)
	c := &Client{
		config:         &config.ServerConfig{Name: "a/b"},
		logger:         zap.NewNop(),
		upstreamLogger: zap.New(perServerCore),
	}
	c.monitorStderr(context.Background(), strings.NewReader(dockerCollisionStderr+"\n"))

	entries := perServerLogs.FilterMessage("stderr").All()
	require.Len(t, entries, 1)
	assert.Equal(t, dockerCollisionStderr, entries[0].ContextMap()["message"])
	assert.Equal(t, true, entries[0].ContextMap()["child_output"])
}

// Pre-spawn record: "Docker isolation configured" names the GENERATED
// container name before Docker has created or inspected anything, so it
// must not assert ownership of that name — no container_owner. (A record
// naming a container without container_owner is withheld from scoped
// callers by the reader's subject-evidence rule; administrators see it.)
func TestSetupDockerIsolation_ConfiguredRecordCarriesNoOwnerForUnverifiedName(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c, _, upLogs := newOwnershipClient("a/b", nil)
	c.setupDockerIsolation(c.config.Command, c.config.Args)

	configured := upLogs.FilterMessage("Docker isolation configured").All()
	require.Len(t, configured, 1)
	fields := configured[0].ContextMap()
	assert.NotEmpty(t, fields["container_name"], "the generated name is still recorded")
	_, hasOwner := fields["container_owner"]
	assert.False(t, hasOwner, "container_owner asserted for a container that does not exist yet: %v", fields)
}
