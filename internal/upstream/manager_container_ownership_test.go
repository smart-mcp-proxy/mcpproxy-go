package upstream

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// Spec 105 FR-007 / research D9, codex round 3 (docker findings 1 and 2).
//
// The manager has three container paths of its own, beside the per-server
// ones in internal/upstream/core: the shutdown sweep
// (cleanupAllManagedContainers) selects every container carrying
// com.mcpproxy.managed=true, the emergency sweep (ForceCleanupAllContainers)
// every container carrying that AND this instance's id, and the
// disconnect-timeout path (forceCleanupClient) ran `docker rm -f` on a
// client's stored id. Labels are copyable and a stored id can be renamed or
// reused, so none of the three was canonical ownership. Now every selected
// container must ALSO be canonically owned by a configured server — label
// com.mcpproxy.server=<raw> AND name ^mcpproxy-<sanitised(raw)>-[a-z0-9]{4}$
// for the SAME configured server (core.ContainerOwnedByAny) — and the
// emergency path re-inspects the stored id through the core predicate.
// Foreign rows are neither mutated nor named: they are counted at Warn.

// managerFakeDocker is a sh+awk `docker` shim on PATH (the manager sweeps
// exec the bare name): `ps` answers from a TSV fixture honouring every
// `--filter label=k[=v]` (joined with `|`, which no label here contains)
// and `--format` with {{.ID}}, {{.Names}} and
// {{.Label "k"}}; `ps -q` answers nothing (every container already stopped);
// stop/kill/rm exit 0. Every invocation is appended to a log.
type managerFakeDocker struct {
	logPath string
}

type managerFakeContainer struct {
	ID     string
	Name   string
	Labels map[string]string
}

const managerFakeDockerShim = `#!/bin/sh
LOG=%s
PS=%s
printf '%%s\n' "$*" >> "$LOG"
[ "$1" = ps ] || exit 0
shift
format='{{.ID}}	{{.Names}}'
quiet=0
filters=''
while [ $# -gt 0 ]; do
  case "$1" in
    --format) format="$2"; shift 2 ;;
    -q) quiet=1; shift ;;
    --filter|-f)
      case "$2" in
        label=*) filters="$filters${2#label=}|" ;;
      esac
      shift 2 ;;
    *) shift ;;
  esac
done
[ "$quiet" = 1 ] && exit 0
awk -F'\t' -v fmt="$format" -v flt="$filters" '
function repl(s, lit, val,    i, out) {
  out = ""
  while ((i = index(s, lit)) > 0) { out = out substr(s, 1, i - 1) val; s = substr(s, i + length(lit)) }
  return out s
}
BEGIN { nflt = split(flt, fl, "|") }
{
  delete labels
  n = split($3, pairs, ",")
  for (i = 1; i <= n; i++) { eq = index(pairs[i], "="); if (eq > 0) labels[substr(pairs[i], 1, eq - 1)] = substr(pairs[i], eq + 1) }
  for (j = 1; j <= nflt; j++) {
    if (fl[j] == "") continue
    eq = index(fl[j], "=")
    if (eq == 0) { if (!(fl[j] in labels)) next; continue }
    k = substr(fl[j], 1, eq - 1); v = substr(fl[j], eq + 1)
    if (!(k in labels) || labels[k] != v) next
  }
  out = fmt
  out = repl(out, "{{.ID}}", $1)
  out = repl(out, "{{.Names}}", $2)
  while (match(out, /\{\{\.Label "[^"]*"\}\}/)) {
    key = substr(out, RSTART + 10, RLENGTH - 13)
    out = substr(out, 1, RSTART - 1) labels[key] substr(out, RSTART + RLENGTH)
  }
  print out
}' "$PS"
`

func shellQuoteForManagerShim(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func installManagerFakeDocker(t *testing.T, containers []managerFakeContainer) *managerFakeDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix shell shim")
	}
	dir := t.TempDir()
	fd := &managerFakeDocker{logPath: filepath.Join(dir, "invocations.log")}
	psPath := filepath.Join(dir, "ps.tsv")
	var tsv strings.Builder
	for _, c := range containers {
		labels := make([]string, 0, len(c.Labels))
		for k, v := range c.Labels {
			labels = append(labels, k+"="+v)
		}
		fmt.Fprintf(&tsv, "%s\t%s\t%s\n", c.ID, c.Name, strings.Join(labels, ","))
	}
	require.NoError(t, os.WriteFile(psPath, []byte(tsv.String()), 0o600))

	toolDir := filepath.Join(dir, "path")
	require.NoError(t, os.Mkdir(toolDir, 0o755))
	script := fmt.Sprintf(managerFakeDockerShim, shellQuoteForManagerShim(fd.logPath), shellQuoteForManagerShim(psPath))
	require.NoError(t, os.WriteFile(filepath.Join(toolDir, "docker"), []byte(script), 0o755))
	for _, tool := range []string{"sh", "awk", "printf"} {
		if real, err := exec.LookPath(tool); err == nil {
			require.NoError(t, os.Symlink(real, filepath.Join(toolDir, tool)))
		}
	}
	t.Setenv("PATH", toolDir)
	return fd
}

func (fd *managerFakeDocker) invocations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(fd.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// mutationsOf returns the rm/stop/kill invocations naming id.
func (fd *managerFakeDocker) mutationsOf(t *testing.T, id string) []string {
	t.Helper()
	var hits []string
	for _, line := range fd.invocations(t) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "rm", "stop", "kill":
			if fields[len(fields)-1] == id {
				hits = append(hits, line)
			}
		}
	}
	return hits
}

const (
	sweepOwnID        = "a1a1a1a1a1a1"
	sweepOwnName      = "mcpproxy-a-xk3q"
	sweepCopiedID     = "b2b2b2b2b2b2" // foreign container that copied the managed+instance labels
	sweepCopiedName   = "postgres"
	sweepAbID         = "c3c3c3c3c3c3" // a-b's canonical container; a-b is not configured
	sweepAbName       = "mcpproxy-a-b-wxyz"
	sweepCustomID     = "d4d4d4d4d4d4" // configured server's label on a user --name container
	sweepCustomName   = "custom"
	sweepFakeInstance = "instance-under-test"
)

// sweepFixture: every row carries the labels both sweeps select on.
func sweepFixture(instanceID string) []managerFakeContainer {
	shared := func(extra map[string]string) map[string]string {
		labels := map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": instanceID}
		for k, v := range extra {
			labels[k] = v
		}
		return labels
	}
	return []managerFakeContainer{
		{ID: sweepOwnID, Name: sweepOwnName, Labels: shared(map[string]string{"com.mcpproxy.server": "a"})},
		{ID: sweepCopiedID, Name: sweepCopiedName, Labels: shared(nil)},
		{ID: sweepAbID, Name: sweepAbName, Labels: shared(map[string]string{"com.mcpproxy.server": "a-b"})},
		{ID: sweepCustomID, Name: sweepCustomName, Labels: shared(map[string]string{"com.mcpproxy.server": "a"})},
	}
}

// newSweepManager builds a manager with servers `a` and `a/b` configured
// (disabled, never connected) and its main logger observed.
func newSweepManager(t *testing.T) (*Manager, *observer.ObservedLogs) {
	t.Helper()
	t.Setenv("CI", "")
	mainCore, mainLogs := observer.New(zap.DebugLevel)
	m := NewManager(zap.New(mainCore), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	for _, name := range []string{"a", "a/b"} {
		require.NoError(t, m.AddServerConfig(name, &config.ServerConfig{Name: name, Protocol: "http", URL: "http://127.0.0.1:1/mcp", Enabled: false}))
	}
	return m, mainLogs
}

func mainLogMentions(logs *observer.ObservedLogs, needle string) []string {
	var hits []string
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, needle) {
			hits = append(hits, entry.Message)
			continue
		}
		for k, v := range entry.ContextMap() {
			if strings.Contains(fmt.Sprint(v), needle) {
				hits = append(hits, entry.Message+" "+k+"="+fmt.Sprint(v))
				break
			}
		}
	}
	return hits
}

// assertSweepTouchesOnlyOwned is the shared oracle for both sweeps: a's own
// canonical container is mutated; the three foreign rows are neither
// mutated nor named (id or name) anywhere in main.log; the skipped rows are
// counted once at Warn.
func assertSweepTouchesOnlyOwned(t *testing.T, fd *managerFakeDocker, mainLogs *observer.ObservedLogs) {
	t.Helper()
	assert.NotEmpty(t, fd.mutationsOf(t, sweepOwnID), "a's own container must still be cleaned up; invocations:\n%s",
		strings.Join(fd.invocations(t), "\n"))
	for _, foreign := range []struct{ id, name string }{
		{sweepCopiedID, sweepCopiedName}, {sweepAbID, sweepAbName}, {sweepCustomID, sweepCustomName},
	} {
		assert.Empty(t, fd.mutationsOf(t, foreign.id), "foreign container %s (%s) was mutated", foreign.id, foreign.name)
		assert.Empty(t, mainLogMentions(mainLogs, foreign.id), "foreign id %s written into main.log", foreign.id)
		assert.Empty(t, mainLogMentions(mainLogs, foreign.name), "foreign name %s written into main.log", foreign.name)
	}
	skipped := mainLogs.FilterMessage("Skipping containers carrying the mcpproxy labels that no configured server canonically owns").All()
	require.Len(t, skipped, 1, "the skipped foreign rows are reported once")
	assert.Equal(t, zap.WarnLevel, skipped[0].Level)
	assert.EqualValues(t, 3, skipped[0].ContextMap()["count"])
}

func TestCleanupAllManagedContainers_TouchesOnlyCanonicallyOwned(t *testing.T) {
	fd := installManagerFakeDocker(t, sweepFixture(core.GetInstanceID()))
	m, mainLogs := newSweepManager(t)

	m.cleanupAllManagedContainers(context.Background())

	assertSweepTouchesOnlyOwned(t, fd, mainLogs)
	assert.Contains(t, fd.mutationsOf(t, sweepOwnID), "stop "+sweepOwnID)
}

func TestForceCleanupAllContainers_TouchesOnlyCanonicallyOwned(t *testing.T) {
	fd := installManagerFakeDocker(t, sweepFixture(core.GetInstanceID()))
	m, mainLogs := newSweepManager(t)

	m.ForceCleanupAllContainers()

	assertSweepTouchesOnlyOwned(t, fd, mainLogs)
	assert.Contains(t, fd.mutationsOf(t, sweepOwnID), "rm -f "+sweepOwnID)
}

// With nothing configured, the sweeps mutate nothing at all.
func TestSweeps_NoConfiguredServers_MutateNothing(t *testing.T) {
	fd := installManagerFakeDocker(t, sweepFixture(core.GetInstanceID()))
	t.Setenv("CI", "")
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })

	m.cleanupAllManagedContainers(context.Background())
	m.ForceCleanupAllContainers()

	for _, id := range []string{sweepOwnID, sweepCopiedID, sweepAbID, sweepCustomID} {
		assert.Empty(t, fd.mutationsOf(t, id), "container %s mutated with no configured owner", id)
	}
}

// fakeForceCleanupTarget stands in for a managed client on the
// disconnect-timeout path: it records whether the manager went through the
// ownership-checked removal instead of a bare `docker rm -f`.
type fakeForceCleanupTarget struct {
	name        string
	containerID string
	calls       []string
}

func (f *fakeForceCleanupTarget) GetConfig() *config.ServerConfig {
	return &config.ServerConfig{Name: f.name}
}
func (f *fakeForceCleanupTarget) GetContainerID() string { return f.containerID }
func (f *fakeForceCleanupTarget) ForceRemoveTrackedContainerIfOwned(_ context.Context, id string) (bool, error) {
	f.calls = append(f.calls, id)
	return false, nil
}

// Codex round 3, docker finding 1: forceCleanupClient must route through the
// core ownership-checked removal (core.Client.ForceRemoveTrackedContainerIfOwned,
// tested against the fake docker in internal/upstream/core) — never a bare
// `docker rm -f <stored id>`.
func TestForceCleanupClient_RoutesThroughOwnershipCheckedRemoval(t *testing.T) {
	fd := installManagerFakeDocker(t, nil)
	m, _ := newSweepManager(t)
	target := &fakeForceCleanupTarget{name: "a", containerID: "f0e1d2c3b4a5968778695a4b3c2d1e0ff0e1d2c3b4a5968778695a4b3c2d1e0f"}

	m.forceCleanupClient(target)

	assert.Equal(t, []string{target.containerID}, target.calls, "the stored id must be handed to the ownership-checked removal")
	assert.Empty(t, fd.invocations(t), "the manager itself must not exec docker on this path")

	target = &fakeForceCleanupTarget{name: "a"}
	m.forceCleanupClient(target)
	assert.Empty(t, target.calls, "no stored id, nothing to remove")
}
