package upstream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
// `--filter label=k[=v]` (joined with `|`, which no label here contains),
// `--filter id=<id>` (a prefix match, as docker's is) and `--format` with {{.ID}}, {{.Names}},
// {{.Status}} and {{.Label "k"}}; without `-a` only rows marked Running are
// answered — same as real `docker ps` — regardless of `-q` (default: every
// container already stopped, so a plain `ps` with no `-a` answers nothing
// until a row sets Running: true). {{.Status}} synthesises a plausible
// `docker ps` human STATUS text from the fixture's Running bool alone ("Up 1
// second" / "Exited (0) 1 second ago") — the same "Up" prefix real Docker
// uses regardless of state (codex round 16 finding 1: ContainerRow.Running
// reads it), so this fixture needs no separate paused/restarting case.
// stop/kill/rm exit 0 unless the verb is listed in the fail file
// (failVerbs). Every invocation is
// appended to a log. A `ps.tsv.next` fixture (swapFixtureAfterNextPs)
// replaces the fixture right after the next `ps` answers, so a container
// can change between the sweep's listing and its mutation.
type managerFakeDocker struct {
	logPath  string
	psPath   string
	failPath string
}

type managerFakeContainer struct {
	ID      string
	Name    string
	Labels  map[string]string
	Running bool
}

const managerFakeDockerShim = `#!/bin/sh
LOG=%s
PS=%s
FAIL=%s
printf '%%s\n' "$*" >> "$LOG"
if [ -f "$FAIL" ]; then
  read -r failverbs < "$FAIL"
  case " $failverbs " in *" $1 "*) exit 1 ;; esac
fi
if [ "$1" = inspect ]; then
  shift
  ifmt=''
  iid=''
  while [ $# -gt 0 ]; do
    case "$1" in
      --format) ifmt="$2"; shift 2 ;;
      *) iid="$1"; shift ;;
    esac
  done
  row=$(awk -F'\t' -v id="$iid" '$1==id{print; found=1} END{exit !found}' "$PS")
  if [ -z "$row" ]; then echo "Error: No such object: $iid" >&2; exit 1; fi
  printf '%%s\n' "$row" | awk -F'\t' -v fmt="$ifmt" '
  function repl(s, lit, val,    i, out) {
    out = ""
    while ((i = index(s, lit)) > 0) { out = out substr(s, 1, i - 1) val; s = substr(s, i + length(lit)) }
    return out s
  }
  {
    running = ($4 == "1") ? "true" : "false"
    status = ($4 == "1") ? "running" : "exited"
    out = fmt
    out = repl(out, "{{.State.Running}}", running)
    out = repl(out, "{{.State.Status}}", status)
    print out
  }'
  exit 0
fi
[ "$1" = ps ] || exit 0
shift
format='{{.ID}}	{{.Names}}'
quiet=0
all=0
filters=''
idflt=''
while [ $# -gt 0 ]; do
  case "$1" in
    --format) format="$2"; shift 2 ;;
    -q) quiet=1; format='{{.ID}}'; shift ;;
    -a) all=1; shift ;;
    --filter|-f)
      case "$2" in
        label=*) filters="$filters${2#label=}|" ;;
        id=*) idflt="${2#id=}" ;;
      esac
      shift 2 ;;
    *) shift ;;
  esac
done
awk -F'\t' -v fmt="$format" -v flt="$filters" -v all="$all" -v idflt="$idflt" '
function repl(s, lit, val,    i, out) {
  out = ""
  while ((i = index(s, lit)) > 0) { out = out substr(s, 1, i - 1) val; s = substr(s, i + length(lit)) }
  return out s
}
BEGIN { nflt = split(flt, fl, "|") }
{
  if (idflt != "" && index($1, idflt) != 1) next
  if (all == 0 && $4 != "1") next
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
  out = repl(out, "{{.Status}}", ($4 == "1") ? "Up 1 second" : "Exited (0) 1 second ago")
  while (match(out, /\{\{\.Label "[^"]*"\}\}/)) {
    key = substr(out, RSTART + 10, RLENGTH - 13)
    out = substr(out, 1, RSTART - 1) labels[key] substr(out, RSTART + RLENGTH)
  }
  print out
}' "$PS"
if [ -f "$PS.next" ]; then mv "$PS.next" "$PS"; fi
`

func shellQuoteForManagerShim(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func installManagerFakeDocker(t *testing.T, containers []managerFakeContainer) *managerFakeDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix shell shim")
	}
	dir := t.TempDir()
	fd := &managerFakeDocker{
		logPath:  filepath.Join(dir, "invocations.log"),
		psPath:   filepath.Join(dir, "ps.tsv"),
		failPath: filepath.Join(dir, "fail"),
	}
	psPath := fd.psPath
	require.NoError(t, os.WriteFile(psPath, managerFakeFixtureTSV(containers), 0o600))

	toolDir := filepath.Join(dir, "path")
	require.NoError(t, os.Mkdir(toolDir, 0o755))
	script := fmt.Sprintf(managerFakeDockerShim, shellQuoteForManagerShim(fd.logPath), shellQuoteForManagerShim(psPath), shellQuoteForManagerShim(fd.failPath))
	require.NoError(t, os.WriteFile(filepath.Join(toolDir, "docker"), []byte(script), 0o755))
	for _, tool := range []string{"sh", "awk", "printf", "mv"} {
		if real, err := exec.LookPath(tool); err == nil {
			require.NoError(t, os.Symlink(real, filepath.Join(toolDir, tool)))
		}
	}
	t.Setenv("PATH", toolDir)
	return fd
}

// managerFakeFixtureTSV renders the `ps` fixture the shim reads.
func managerFakeFixtureTSV(containers []managerFakeContainer) []byte {
	var tsv strings.Builder
	for _, c := range containers {
		labels := make([]string, 0, len(c.Labels))
		for k, v := range c.Labels {
			labels = append(labels, k+"="+v)
		}
		running := "0"
		if c.Running {
			running = "1"
		}
		fmt.Fprintf(&tsv, "%s\t%s\t%s\t%s\n", c.ID, c.Name, strings.Join(labels, ","), running)
	}
	return []byte(tsv.String())
}

// swapFixtureAfterNextPs makes the shim answer the NEXT `ps` from the
// current fixture and every later one from containers — the state of the
// daemon after another client changed it between listing and mutation.
func (fd *managerFakeDocker) swapFixtureAfterNextPs(t *testing.T, containers []managerFakeContainer) {
	t.Helper()
	require.NoError(t, os.WriteFile(fd.psPath+".next", managerFakeFixtureTSV(containers), 0o600))
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

// failVerbs makes every later invocation of the listed docker verbs
// (stop, kill, rm, ...) exit 1 without output.
func (fd *managerFakeDocker) failVerbs(t *testing.T, verbs ...string) {
	t.Helper()
	require.NoError(t, os.WriteFile(fd.failPath, []byte(strings.Join(verbs, " ")+"\n"), 0o600))
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
// mutated nor named (id or name) anywhere in main.log. The rejected rows'
// labels are untrusted (they failed canonical ownership), so no owner can
// be attributed to them; per Spec 105 D8/D9 (codex round 11) a count that
// cannot be bound to a subject is not evidence, so listOwnedManagedContainers
// logs nothing about the rejected-row tally at all.
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
	assert.Empty(t, skipped, "the rejected-row tally must not be logged: no owner can be attributed to it")
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

// ownerCountsFromLog reduces a set of owner-grouped count records (each
// carrying container_owner and count) to a map, failing the test if any
// record in entries is missing either field — an aggregate with no owner
// must never be among them (D8, codex round 10 finding 2).
func ownerCountsFromLog(t *testing.T, entries []observer.LoggedEntry) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(entries))
	for _, entry := range entries {
		fields := entry.ContextMap()
		owner, _ := fields["container_owner"].(string)
		require.NotEmpty(t, owner, "record %q carries no container_owner: %v", entry.Message, fields)
		switch v := fields["count"].(type) {
		case int64:
			counts[owner] = int(v)
		case int:
			counts[owner] = v
		default:
			t.Fatalf("record %q carries no numeric count: %v", entry.Message, fields)
		}
	}
	return counts
}

// twoOwnerSweepFixture: two containers, canonically owned by two DIFFERENT
// configured servers (a and a/b — sanitised to a-b in the name, per
// dockernaming.SanitizeServerName). Used to prove a sweep's "found"/"force
// removing" count is owner-grouped rather than a single aggregate that
// cannot be bound to either subject.
func twoOwnerSweepFixture(instanceID string) []managerFakeContainer {
	shared := func(server string) map[string]string {
		return map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": instanceID, "com.mcpproxy.server": server}
	}
	return []managerFakeContainer{
		{ID: "e5e5e5e5e5e5", Name: "mcpproxy-a-yz12", Labels: shared("a")},
		{ID: "f6f6f6f6f6f6", Name: "mcpproxy-a-b-yz34", Labels: shared("a/b")},
	}
}

// Codex round 10, docker finding 2 (D8): a sweep can select containers
// belonging to more than one configured server, so a bare aggregate count
// cannot be bound to a subject. The shutdown sweep's "Found ... to cleanup"
// and the emergency sweep's "Force removing ..." records must instead be
// owner-grouped: one record per Docker-read owner, each carrying its
// container_owner and count — never a bare count with no owner.
func TestSweepCounts_AreOwnerGrouped(t *testing.T) {
	t.Run("cleanupAllManagedContainers", func(t *testing.T) {
		installManagerFakeDocker(t, twoOwnerSweepFixture(core.GetInstanceID()))
		m, mainLogs := newSweepManager(t)

		m.cleanupAllManagedContainers(context.Background())

		found := mainLogs.FilterMessage("Found mcpproxy-managed containers to cleanup").All()
		require.Len(t, found, 2, "one record per Docker-read owner, not one aggregate")
		assert.Equal(t, map[string]int{"a": 1, "a/b": 1}, ownerCountsFromLog(t, found))
	})

	t.Run("ForceCleanupAllContainers", func(t *testing.T) {
		installManagerFakeDocker(t, twoOwnerSweepFixture(core.GetInstanceID()))
		m, mainLogs := newSweepManager(t)

		m.ForceCleanupAllContainers()

		found := mainLogs.FilterMessage("Force removing managed containers").All()
		require.Len(t, found, 2, "one record per Docker-read owner, not one aggregate")
		assert.Equal(t, map[string]int{"a": 1, "a/b": 1}, ownerCountsFromLog(t, found))
	})
}

// Codex round 10, docker finding 1 (D9): HasDockerContainers drove the
// runtime/server shutdown wait and the "still running" report off the
// shared, copyable managed/instance labels alone — it must instead apply
// the same selection as the sweeps (listOwnedManagedContainers /
// core.ContainerOwnedByAny), so a foreign container that copies those
// labels, or one whose owning server was removed from config, is not
// reported as still running.
func TestHasDockerContainers_AppliesCanonicalOwnership(t *testing.T) {
	t.Run("foreign container copying the shared labels - not reported", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepCopiedID, Name: sweepCopiedName, Running: true,
				Labels: map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID()}},
		})
		m, _ := newSweepManager(t)

		assert.False(t, m.HasDockerContainers(), "a foreign container carrying only the shared labels must not count")
	})

	t.Run("orphaned container (server no longer configured) - not reported", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepAbID, Name: sweepAbName, Running: true,
				Labels: map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID(), "com.mcpproxy.server": "a-b"}},
		})
		m, _ := newSweepManager(t)

		assert.False(t, m.HasDockerContainers(), "a-b is not configured; its container must not count")
	})

	t.Run("canonically owned and running - reported", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: true,
				Labels: map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID(), "com.mcpproxy.server": "a"}},
		})
		m, _ := newSweepManager(t)

		assert.True(t, m.HasDockerContainers(), "a's own canonically owned, running container must count")
	})

	t.Run("canonically owned but stopped - not reported", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: false,
				Labels: map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID(), "com.mcpproxy.server": "a"}},
		})
		m, _ := newSweepManager(t)

		assert.False(t, m.HasDockerContainers(), "a stopped container must not read as still running")
	})

	t.Run("docker unavailable - not reported", func(t *testing.T) {
		fd := installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: true,
				Labels: map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID(), "com.mcpproxy.server": "a"}},
		})
		fd.failVerbs(t, "ps")
		m, _ := newSweepManager(t)

		assert.False(t, m.HasDockerContainers())
	})
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
// ownership-checked removal instead of a bare `docker rm -f`, answers it
// with the configured verdict (owner/owned/err), and snapshots the main.log
// records written BEFORE the verdict existed.
type fakeForceCleanupTarget struct {
	name        string
	containerID string
	calls       []string

	owner string
	owned bool
	err   error

	mainLogs        *observer.ObservedLogs
	recordsAtCall   []observer.LoggedEntry
	recordsCaptured bool
}

func (f *fakeForceCleanupTarget) GetConfig() *config.ServerConfig {
	return &config.ServerConfig{Name: f.name}
}
func (f *fakeForceCleanupTarget) GetContainerID() string { return f.containerID }
func (f *fakeForceCleanupTarget) ForceRemoveTrackedContainerIfOwned(_ context.Context, id string) (string, bool, error) {
	f.calls = append(f.calls, id)
	if f.mainLogs != nil {
		f.recordsAtCall = f.mainLogs.All()
		f.recordsCaptured = true
	}
	return f.owner, f.owned, f.err
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

// sweepFixtureOwnRunning is sweepFixture with a's own container still
// running, so the shutdown sweep's force-kill branch fires after `stop`.
func sweepFixtureOwnRunning(instanceID string) []managerFakeContainer {
	rows := sweepFixture(instanceID)
	for i := range rows {
		if rows[i].ID == sweepOwnID {
			rows[i].Running = true
		}
	}
	return rows
}

// recordNamesOwnContainer reports whether any field value of a main.log
// record carries a's container id (full or short) or name — independent
// of the key the record files it under.
func recordNamesOwnContainer(fields map[string]interface{}) bool {
	return recordNamesAny(fields, sweepOwnID, shortContainerID(sweepOwnID), sweepOwnName)
}

// recordNamesAny reports whether any string field value of a record carries
// one of needles.
func recordNamesAny(fields map[string]interface{}, needles ...string) bool {
	for _, v := range fields {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, needle := range needles {
			if strings.Contains(s, needle) {
				return true
			}
		}
	}
	return false
}

// Codex round 4, docker finding 1 (Spec 105 FR-007 / research D8, D9): the
// sweeps' intent records carried the read-back owner, but the OUTCOME
// records — stop succeeded/failed, force-kill intent/succeeded/failed,
// force-removal succeeded/failed — named the container's id or name with no
// `container_owner`, so a subject-evidence consumer had to withhold them.
// Every main.log record a sweep writes that names a container must carry
// the owner Docker reported for it, on the success and the failure branch
// alike.
func TestSweeps_EveryRecordNamingAContainerCarriesContainerOwner(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     []string
		expected []string
	}{
		{
			name: "docker_succeeds",
			expected: []string{
				"Stopping container", "Container stopped gracefully",
				"Force killing container", "Container force killed",
				"Force removing container", "Container force removed successfully",
			},
		},
		{
			name: "docker_fails",
			fail: []string{"stop", "kill", "rm"},
			expected: []string{
				"Stopping container", "Graceful stop failed, will force kill",
				"Force killing container", "Failed to force kill container",
				"Force removing container", "Failed to force remove container",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fd := installManagerFakeDocker(t, sweepFixtureOwnRunning(core.GetInstanceID()))
			if len(tc.fail) > 0 {
				fd.failVerbs(t, tc.fail...)
			}
			m, mainLogs := newSweepManager(t)

			m.cleanupAllManagedContainers(context.Background())
			m.ForceCleanupAllContainers()

			for _, msg := range tc.expected {
				require.NotEmpty(t, mainLogs.FilterMessage(msg).All(), "branch %q not exercised; invocations:\n%s",
					msg, strings.Join(fd.invocations(t), "\n"))
			}
			naming := 0
			for _, entry := range mainLogs.All() {
				fields := entry.ContextMap()
				if !recordNamesOwnContainer(fields) {
					continue
				}
				naming++
				assert.Equal(t, "a", fields["container_owner"],
					"record %q names a's container without the read-back owner: %v", entry.Message, fields)
			}
			assert.GreaterOrEqual(t, naming, len(tc.expected), "every expected branch names the container")
			for _, foreign := range []string{sweepCopiedID, sweepCopiedName, sweepAbID, sweepAbName, sweepCustomID, sweepCustomName} {
				assert.Empty(t, mainLogMentions(mainLogs, foreign), "foreign %s written into main.log", foreign)
			}
		})
	}
}

// Codex round 5, docker finding 1 (Spec 105 D8 subject-evidence rule): the
// disconnect-timeout path named the tracked id in its intent record before
// ownership was verified and in every outcome record without
// `container_owner`. A record written before the verdict exists must name
// no container id — the server only; the outcomes that name the id carry
// the owner the core read back at the mutation; a rejected or unverifiable
// container is never named by id at all.
func TestForceCleanupClient_NamesTheContainerOnlyWithOwnershipEvidence(t *testing.T) {
	const trackedID = "f0e1d2c3b4a5968778695a4b3c2d1e0ff0e1d2c3b4a5968778695a4b3c2d1e0f"
	needles := []string{trackedID, shortContainerID(trackedID)}
	for _, tc := range []struct {
		name      string
		owner     string
		owned     bool
		err       error
		wantNamed bool // some outcome record names the id (with the owner)
	}{
		{name: "removed", owner: "a", owned: true, wantNamed: true},
		{name: "removal failed after verification", owner: "a", owned: true, err: errors.New("rm: exit status 1"), wantNamed: true},
		{name: "not owned any more", owned: false},
		{name: "ownership unverifiable", owned: false, err: errors.New("ps: exit status 1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installManagerFakeDocker(t, nil)
			m, mainLogs := newSweepManager(t)
			target := &fakeForceCleanupTarget{name: "a", containerID: trackedID, owner: tc.owner, owned: tc.owned, err: tc.err, mainLogs: mainLogs}

			m.forceCleanupClient(target)

			require.True(t, target.recordsCaptured, "the ownership-checked removal must run")
			for _, entry := range target.recordsAtCall {
				assert.False(t, recordNamesAny(entry.ContextMap(), needles...),
					"record %q names the tracked id before ownership was verified: %v", entry.Message, entry.ContextMap())
			}
			named := 0
			for _, entry := range mainLogs.All() {
				fields := entry.ContextMap()
				if !recordNamesAny(fields, needles...) {
					continue
				}
				named++
				assert.True(t, tc.owned, "record %q names the container although ownership was not established: %v", entry.Message, fields)
				assert.Equal(t, tc.owner, fields["container_owner"], "record %q names the container without the read-back owner: %v", entry.Message, fields)
			}
			if tc.wantNamed {
				assert.NotZero(t, named, "the outcome of a verified removal names the container with its owner")
			}
			assert.NotEmpty(t, mainLogMentions(mainLogs, "a"), "the server is still named on every path")
		})
	}
}

// Codex round 5, docker finding 2 (Spec 105 D9 moment-of-mutation rule):
// the sweeps stopped, killed and removed containers on the ownership their
// initial `docker ps` established. Another Docker client can rename or
// relabel a container between that listing and the mutation, so the name
// and label are re-read immediately before each stop/kill/rm: a container
// that no longer satisfies the predicate is left alone and the refusal is
// recorded without its id or name; one that still does is mutated, and the
// owner every record carries is the one read at mutation time.
func TestSweeps_ReverifyOwnershipAtMutationTime(t *testing.T) {
	relabelled := func(name, owner string) []managerFakeContainer {
		labels := map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID()}
		if owner != "" {
			labels["com.mcpproxy.server"] = owner
		}
		return []managerFakeContainer{{ID: sweepOwnID, Name: name, Labels: labels, Running: true}}
	}
	sweeps := []struct {
		name string
		run  func(m *Manager)
		verb string
	}{
		{name: "shutdown", run: func(m *Manager) { m.cleanupAllManagedContainers(context.Background()) }, verb: "stop"},
		{name: "emergency", run: func(m *Manager) { m.ForceCleanupAllContainers() }, verb: "rm -f"},
	}
	arms := []struct {
		name      string
		after     []managerFakeContainer
		wantOwner string // "" — the container must be left alone
	}{
		{name: "relabelled foreign between listing and mutation", after: relabelled("postgres", "")},
		{name: "renamed to an unconfigured server's shape", after: relabelled("mcpproxy-a-b-xk3q", "a-b")},
		// A different container whose id extends the listed one: `--filter
		// id=` is a prefix match, so only an exact full-id comparison
		// tells it apart (codex round 6).
		{name: "replaced by a container whose id extends the listed one", after: []managerFakeContainer{{
			ID: sweepOwnID + "ffffffffffffffffffffffffffffffffffffffffffffffffffff", Name: sweepOwnName, Running: true,
			Labels: map[string]string{"com.mcpproxy.managed": "true", "com.mcpproxy.instance": core.GetInstanceID(), "com.mcpproxy.server": "a"}}}},
		{name: "unchanged", after: relabelled(sweepOwnName, "a"), wantOwner: "a"},
		{name: "re-owned by another configured server", after: relabelled("mcpproxy-a-b-xk3q", "a/b"), wantOwner: "a/b"},
	}
	for _, sw := range sweeps {
		for _, arm := range arms {
			t.Run(sw.name+"/"+arm.name, func(t *testing.T) {
				fd := installManagerFakeDocker(t, relabelled(sweepOwnName, "a"))
				fd.swapFixtureAfterNextPs(t, arm.after)
				m, mainLogs := newSweepManager(t)

				sw.run(m)

				mutations := fd.mutationsOf(t, sweepOwnID)
				if arm.wantOwner == "" {
					assert.Empty(t, mutations, "a container whose ownership changed was mutated; invocations:\n%s",
						strings.Join(fd.invocations(t), "\n"))
					for _, line := range fd.invocations(t) {
						verb := strings.Fields(line)[0]
						assert.NotContains(t, []string{"stop", "kill", "rm"}, verb, "nothing may be mutated: %q", line)
					}
					for _, entry := range mainLogs.All() {
						assert.False(t, recordNamesAny(entry.ContextMap(), sweepOwnID, shortContainerID(sweepOwnID), arm.after[0].Name),
							"record %q names a container that is no longer owned: %v", entry.Message, entry.ContextMap())
					}
					assert.NotEmpty(t, mainLogMentions(mainLogs, "no longer canonically owned"), "the refusal is recorded")
					return
				}
				assert.Contains(t, mutations, sw.verb+" "+sweepOwnID, "an owned container is still swept")
				named := 0
				for _, entry := range mainLogs.All() {
					fields := entry.ContextMap()
					if !recordNamesAny(fields, sweepOwnID, shortContainerID(sweepOwnID), arm.after[0].Name) {
						continue
					}
					named++
					assert.Equal(t, arm.wantOwner, fields["container_owner"],
						"record %q must carry the owner read at mutation time: %v", entry.Message, fields)
					if server, ok := fields["server"]; ok {
						assert.Equal(t, arm.wantOwner, server, "record %q attributes the container to the listing-time owner: %v", entry.Message, fields)
					}
				}
				assert.NotZero(t, named, "the mutation is recorded with its subject")
			})
		}
	}
}

// Codex round 8 (PR E), finding 2: verifyContainerHealthy decided health from
// `docker inspect <stored-id>` alone. inspect answers by id regardless of
// name or label, so a container another Docker client relabelled or
// renamed after tracking still reported Running=true under the same id,
// and ForceReconnectAll treated it as healthy — skipping recovery for a
// container that is no longer canonically this server's. The health check
// must re-establish ownership through the same read+predicate
// ContainerMutator.Verify uses before trusting inspect: a container that
// fails ownership now is NOT healthy (recovery proceeds) and the health
// record names no id; a container ownership confirms is named, with
// container_owner from that same read.
func TestVerifyDockerContainerHealthy_ReverifiesOwnershipBeforeInspect(t *testing.T) {
	t.Run("relabelled after tracking - unhealthy, no id recorded", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: true,
				Labels: map[string]string{"com.mcpproxy.server": "z"}}, // foreign label now
		})
		m, mainLogs := newSweepManager(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		healthy, err := m.verifyDockerContainerHealthy(ctx, sweepDocker, "a", sweepOwnID)

		assert.False(t, healthy, "a relabelled container must not be reported healthy")
		require.Error(t, err)
		for _, entry := range mainLogs.All() {
			assert.False(t, recordNamesAny(entry.ContextMap(), sweepOwnID, shortContainerID(sweepOwnID)),
				"record %q names the no-longer-owned container: %v", entry.Message, entry.ContextMap())
		}
	})

	t.Run("renamed after tracking - unhealthy, no id recorded", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: "custom", Running: true, // non-canonical name now
				Labels: map[string]string{"com.mcpproxy.server": "a"}},
		})
		m, mainLogs := newSweepManager(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		healthy, err := m.verifyDockerContainerHealthy(ctx, sweepDocker, "a", sweepOwnID)

		assert.False(t, healthy, "a renamed container must not be reported healthy")
		require.Error(t, err)
		for _, entry := range mainLogs.All() {
			assert.False(t, recordNamesAny(entry.ContextMap(), sweepOwnID, shortContainerID(sweepOwnID)),
				"record %q names the no-longer-owned container: %v", entry.Message, entry.ContextMap())
		}
	})

	t.Run("docker read failure - unhealthy, no id recorded", func(t *testing.T) {
		fd := installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: true,
				Labels: map[string]string{"com.mcpproxy.server": "a"}},
		})
		fd.failVerbs(t, "ps")
		m, mainLogs := newSweepManager(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		healthy, err := m.verifyDockerContainerHealthy(ctx, sweepDocker, "a", sweepOwnID)

		assert.False(t, healthy)
		require.Error(t, err)
		for _, entry := range mainLogs.All() {
			assert.False(t, recordNamesAny(entry.ContextMap(), sweepOwnID, shortContainerID(sweepOwnID)),
				"record %q names a container whose ownership read failed: %v", entry.Message, entry.ContextMap())
		}
	})

	t.Run("unchanged and running - healthy, record carries id and owner", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: true,
				Labels: map[string]string{"com.mcpproxy.server": "a"}},
		})
		m, mainLogs := newSweepManager(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		healthy, err := m.verifyDockerContainerHealthy(ctx, sweepDocker, "a", sweepOwnID)

		require.NoError(t, err)
		assert.True(t, healthy)
		var found bool
		for _, entry := range mainLogs.All() {
			fields := entry.ContextMap()
			if !recordNamesAny(fields, sweepOwnID) {
				continue
			}
			found = true
			assert.Equal(t, "a", fields["container_owner"], "record %q must carry the owner read back: %v", entry.Message, fields)
		}
		assert.True(t, found, "the healthy verification is recorded with its subject")
	})

	t.Run("unchanged but stopped - unhealthy, record still carries id and owner", func(t *testing.T) {
		installManagerFakeDocker(t, []managerFakeContainer{
			{ID: sweepOwnID, Name: sweepOwnName, Running: false,
				Labels: map[string]string{"com.mcpproxy.server": "a"}},
		})
		m, _ := newSweepManager(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		healthy, err := m.verifyDockerContainerHealthy(ctx, sweepDocker, "a", sweepOwnID)

		assert.False(t, healthy)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not running")
	})
}

// TestVerifyDockerContainerHealthy_RunningComesFromTheVerifyReadAlone is
// codex round 16 finding 1: the health check re-verified ownership with one
// `docker ps` read (ContainerMutator.Verify) and THEN issued a second,
// separately-timed `docker inspect <id>` to decide running/status. Between
// the two, another Docker client can rename or relabel the container into a
// colliding server's namespace; the inspect would then report the
// NOW-FOREIGN container's state while ForceReconnectAll kept treating it as
// this server's (a TOCTOU gap the round-8 ownership re-verification did not
// close, because it only re-verified identity, not state). Running and
// status must come from the Verify read itself (ContainerRow.Running /
// .Status), never a follow-up command: the fixture is swapped to a
// relabelled, stopped container right after the fix's one `ps` call, so any
// further read of this id — if a second command were reintroduced — would
// see that swapped data and this test would catch it.
func TestVerifyDockerContainerHealthy_RunningComesFromTheVerifyReadAlone(t *testing.T) {
	fd := installManagerFakeDocker(t, []managerFakeContainer{
		{ID: sweepOwnID, Name: sweepOwnName, Running: true,
			Labels: map[string]string{"com.mcpproxy.server": "a"}},
	})
	// After the fix's one `ps` read answers, swap to a relabelled, stopped
	// container: any FURTHER read of this id would see foreign, not-running
	// data.
	fd.swapFixtureAfterNextPs(t, []managerFakeContainer{
		{ID: sweepOwnID, Name: sweepOwnName, Running: false,
			Labels: map[string]string{"com.mcpproxy.server": "a-b"}},
	})
	m, mainLogs := newSweepManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	healthy, err := m.verifyDockerContainerHealthy(ctx, sweepDocker, "a", sweepOwnID)

	require.NoError(t, err)
	assert.True(t, healthy, "healthy must come from the one ps row Verify already has, not a second command that could see the swapped-in relabel/stop")
	var found bool
	for _, entry := range mainLogs.All() {
		fields := entry.ContextMap()
		if !recordNamesAny(fields, sweepOwnID) {
			continue
		}
		found = true
		assert.Equal(t, "a", fields["container_owner"], "record %q must carry the owner from the SAME read as the running state: %v", entry.Message, fields)
	}
	assert.True(t, found, "the healthy verification is recorded with its subject")

	var psCalls, inspectCalls int
	for _, line := range fd.invocations(t) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "ps":
			psCalls++
		case "inspect":
			inspectCalls++
		}
	}
	assert.Equal(t, 1, psCalls, "the health check must issue exactly one docker ps for this container, invocations:\n%s", strings.Join(fd.invocations(t), "\n"))
	assert.Zero(t, inspectCalls, "the health check must never issue a separate docker inspect, invocations:\n%s", strings.Join(fd.invocations(t), "\n"))
}
