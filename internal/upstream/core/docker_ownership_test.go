package core

import (
	"context"
	"fmt"
	"os"
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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
)

// Spec 105 FR-007 (gap FR007-G5, research D9): every Docker cleanup path —
// ensureNoExistingContainers on connect, the disconnect name-pattern fallback
// and the image-name fallback — must touch only containers canonically owned
// by this server: label com.mcpproxy.server=<raw name> AND name matching
// ^mcpproxy-<sanitised>-[a-z0-9]{4}$. On HEAD `docker ps --filter
// name=mcpproxy-a-` is a substring match with no Go-side predicate, so server
// `a` rm -f'd hidden `a-b`'s live container and wrote its id and name into
// a's per-server log; the image-name fallback killed any container sharing
// the image. Foreign containers must be neither removed nor logged.

// ---------------------------------------------------------------------------
// Fake docker: a sh+awk shim (no re-exec of the race-instrumented test
// binary, which costs ~1s per call). It appends every invocation to the
// invocation log, answers `ps` from a TSV fixture honouring
// --filter name=<ERE> / id=<prefix> / label=<k>[=<v>] and --format templates ({{.ID}},
// {{.Names}}, {{.Image}}, {{.Status}}, {{.CreatedAt}}, {{.Labels}},
// {{.Label "k"}}), and exits 0 for rm/stop/kill/version.
// ---------------------------------------------------------------------------

// fakeContainer is one `docker ps` row of the fixture.
type fakeContainer struct {
	ID     string
	Name   string
	Image  string
	Status string
	Labels map[string]string
}

// fakeDocker is one installed fake docker: the invocation log, the `ps`
// fixture file the shim reads, and the file whose text makes `docker run`
// fail (printed to stderr, exit 125 — the docker CLI's own status for a
// daemon error) when non-empty.
type fakeDocker struct {
	logPath    string
	psPath     string
	runErrPath string
}

// failRunWith makes every `docker run` print stderr and exit 125.
func (fd *fakeDocker) failRunWith(t *testing.T, stderr string) {
	t.Helper()
	require.NoError(t, os.WriteFile(fd.runErrPath, []byte(stderr+"\n"), 0o600))
}

const fakeDockerShim = `#!/bin/sh
LOG=%s
PS=%s
RUNERR=%s
printf '%%s\n' "$*" >> "$LOG"
if [ "$1" = run ] && [ -s "$RUNERR" ]; then cat "$RUNERR" >&2; exit 125; fi
[ "$1" = ps ] || exit 0
shift
format='{{.ID}}	{{.Names}}'
namefilter=''
idfilter=''
labelkey=''
labelval=''
labelset=0
while [ $# -gt 0 ]; do
  case "$1" in
    --format) format="$2"; shift 2 ;;
    --filter|-f)
      case "$2" in
        name=*) namefilter="${2#name=}" ;;
        id=*) idfilter="${2#id=}" ;;
        label=*)
          l="${2#label=}"
          labelkey="${l%%%%=*}"
          case "$l" in *=*) labelval="${l#*=}"; labelset=1 ;; esac
          ;;
      esac
      shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$MCPPROXY_FAKE_DOCKER_IGNORE_FILTERS" ]; then namefilter=''; idfilter=''; labelkey=''; fi
awk -F'\t' -v fmt="$format" -v nf="$namefilter" -v idf="$idfilter" -v lk="$labelkey" -v lv="$labelval" -v ls="$labelset" '
function repl(s, lit, val,    i, out) {
  out = ""
  while ((i = index(s, lit)) > 0) { out = out substr(s, 1, i - 1) val; s = substr(s, i + length(lit)) }
  return out s
}
{
  if (nf != "" && $2 !~ nf) next
  if (idf != "" && index(idf, $1) != 1 && index($1, idf) != 1) next
  delete labels
  n = split($5, pairs, ",")
  for (i = 1; i <= n; i++) { eq = index(pairs[i], "="); if (eq > 0) labels[substr(pairs[i], 1, eq - 1)] = substr(pairs[i], eq + 1) }
  if (lk != "") { if (!(lk in labels)) next; if (ls && labels[lk] != lv) next }
  out = fmt
  out = repl(out, "{{.ID}}", $1)
  out = repl(out, "{{.Names}}", $2)
  out = repl(out, "{{.Image}}", $3)
  out = repl(out, "{{.Status}}", $4)
  out = repl(out, "{{.CreatedAt}}", "2026-09-16 00:00:00 +0000 UTC")
  out = repl(out, "{{.Labels}}", $5)
  while (match(out, /\{\{\.Label "[^"]*"\}\}/)) {
    key = substr(out, RSTART + 10, RLENGTH - 13)
    out = substr(out, 1, RSTART - 1) labels[key] substr(out, RSTART + RLENGTH)
  }
  print out
}' "$PS"
`

// installFakeDocker writes the shim, points the REAL resolver at it
// (SetWellKnownDockerPathsForTest + ResetDockerPathCacheForTest, the seams
// gap-map §7 names) and empties PATH so nothing else can resolve.
func installFakeDocker(t *testing.T, containers []fakeContainer) *fakeDocker {
	t.Helper()
	if runtime.GOOS == osWindows {
		t.Skip("unix shell shim")
	}
	dir := t.TempDir()
	fd := &fakeDocker{
		logPath:    filepath.Join(dir, "invocations.log"),
		psPath:     filepath.Join(dir, "ps.tsv"),
		runErrPath: filepath.Join(dir, "run.stderr"),
	}
	var tsv strings.Builder
	for _, c := range containers {
		labels := make([]string, 0, len(c.Labels))
		for k, v := range c.Labels {
			labels = append(labels, k+"="+v)
		}
		fmt.Fprintf(&tsv, "%s\t%s\t%s\t%s\t%s\n", c.ID, c.Name, c.Image, c.Status, strings.Join(labels, ","))
	}
	require.NoError(t, os.WriteFile(fd.psPath, []byte(tsv.String()), 0o600))

	shim := filepath.Join(dir, "docker")
	script := fmt.Sprintf(fakeDockerShim, shellQuote(fd.logPath), shellQuote(fd.psPath), shellQuote(fd.runErrPath))
	require.NoError(t, os.WriteFile(shim, []byte(script), 0o755))

	t.Setenv("PATH", "/usr/bin:/bin") // sh + awk only; no real docker here
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	useRealDockerResolver(t)
	restore := shellwrap.SetWellKnownDockerPathsForTest(func() []string { return []string{shim} })
	t.Cleanup(restore)
	return fd
}

// fakeDockerIgnoreFiltersEnv makes the shim answer `ps` with EVERY fixture
// row regardless of --filter, so a test can prove the Go-side ownership
// predicate drops what the daemon did not.
const fakeDockerIgnoreFiltersEnv = "MCPPROXY_FAKE_DOCKER_IGNORE_FILTERS"

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// invocations returns every docker command line the shim received.
func (fd *fakeDocker) invocations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(fd.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// mutationsOf returns the rm/stop/kill invocations that name id.
func (fd *fakeDocker) mutationsOf(t *testing.T, id string) []string {
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

// newOwnershipClient builds a client for server name with BOTH its loggers
// observed: c.logger (main.log) and c.upstreamLogger (server-<name>.log, the
// file tail_log serves).
func newOwnershipClient(name string, cfg *config.ServerConfig) (*Client, *observer.ObservedLogs, *observer.ObservedLogs) {
	mainCore, mainLogs := observer.New(zap.DebugLevel)
	upCore, upLogs := observer.New(zap.DebugLevel)
	if cfg == nil {
		cfg = &config.ServerConfig{Command: "python", Args: []string{"-m", "mcp_server"}}
	}
	cfg.Name = name
	c := &Client{
		config:           cfg,
		logger:           zap.New(mainCore),
		upstreamLogger:   zap.New(upCore).With(zap.String("server", name)),
		isolationManager: NewIsolationManager(config.DefaultDockerIsolationConfig()),
	}
	return c, mainLogs, upLogs
}

// recordsMentioning returns every observed record whose message or any field
// value contains needle.
func recordsMentioning(logs *observer.ObservedLogs, needle string) []string {
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

const (
	foreignContainerID   = "deadbeef1234"
	foreignContainerName = "mcpproxy-a-b-wxyz"
	ownContainerID       = "cafe00000001"
	ownContainerName     = "mcpproxy-a-wxyz"
	ownerLabel           = "com.mcpproxy.server"
)

func ownAndForeignFixture() []fakeContainer {
	return []fakeContainer{
		{ID: foreignContainerID, Name: foreignContainerName, Image: "mcp/example", Status: "Up 2 minutes",
			Labels: map[string]string{"com.mcpproxy.managed": "true", ownerLabel: "a-b"}},
		{ID: ownContainerID, Name: ownContainerName, Image: "mcp/example", Status: "Exited (0) 1 minute ago",
			Labels: map[string]string{"com.mcpproxy.managed": "true", ownerLabel: "a"}},
	}
}

// assertForeignUntouched is the shared oracle: the foreign container is
// never rm/stop/kill'd and never named — by id or by name — in either of a's
// loggers (main.log AND the per-server log tail_log serves).
func assertForeignUntouched(t *testing.T, fd *fakeDocker, mainLogs, upLogs *observer.ObservedLogs) {
	t.Helper()
	assert.Empty(t, fd.mutationsOf(t, foreignContainerID), "foreign container %s was mutated", foreignContainerID)
	for _, needle := range []string{foreignContainerID, foreignContainerName} {
		assert.Empty(t, recordsMentioning(upLogs, needle), "foreign %q written into a's per-server log", needle)
		assert.Empty(t, recordsMentioning(mainLogs, needle), "foreign %q written into main log under server=a", needle)
	}
}

// FR007-G5 (connect path): ensureNoExistingContainers for server `a` with
// hidden `a-b`'s live container and a's own stale container present.
func TestDockerCleanup_MatchesOnlyCanonicalOwner_Connect(t *testing.T) {
	fd := installFakeDocker(t, ownAndForeignFixture())
	c, mainLogs, upLogs := newOwnershipClient("a", nil)

	require.NoError(t, c.ensureNoExistingContainers(context.Background()))

	assertForeignUntouched(t, fd, mainLogs, upLogs)
	assert.NotEmpty(t, fd.mutationsOf(t, ownContainerID), "a's own stale container must still be removed; invocations:\n%s",
		strings.Join(fd.invocations(t), "\n"))
	assert.NotEmpty(t, recordsMentioning(upLogs, ownContainerID), "removing a's own container is still recorded in a's log")
}

// FR007-G5 (disconnect name-pattern fallback): no known container id or
// name, so the client falls back to pattern cleanup; the foreign container
// matches the name prefix but not the canonical-owner predicate.
func TestDockerCleanup_MatchesOnlyCanonicalOwner_DisconnectNamePattern(t *testing.T) {
	fd := installFakeDocker(t, ownAndForeignFixture())
	c, mainLogs, upLogs := newOwnershipClient("a", &config.ServerConfig{
		Command: "docker", Args: []string{"run", "-i", "--rm", "mcp/example"},
	})

	c.killDockerContainerByCommandWithContext(context.Background())

	assertForeignUntouched(t, fd, mainLogs, upLogs)
	assert.NotEmpty(t, fd.mutationsOf(t, ownContainerID), "a's own container must still be stopped; invocations:\n%s",
		strings.Join(fd.invocations(t), "\n"))
}

// FR007-G5 (image-name fallback, D9): no owned container at all, empty known
// container id, and two foreign containers on the SAME image — one whose
// name matches the prefix, one that does not. The name-pattern step finds no
// owned container and the image-name fallback must touch nothing.
func TestDockerCleanup_ImageNameFallback_TouchesNothingForeign(t *testing.T) {
	const unrelatedID = "feedface0002"
	fd := installFakeDocker(t, []fakeContainer{
		{ID: foreignContainerID, Name: foreignContainerName, Image: "mcp/example", Status: "Up 2 minutes",
			Labels: map[string]string{ownerLabel: "a-b"}},
		{ID: unrelatedID, Name: "unrelated-tool", Image: "mcp/example", Status: "Up 5 minutes",
			Labels: map[string]string{}},
	})
	c, mainLogs, upLogs := newOwnershipClient("a", &config.ServerConfig{
		Command: "docker", Args: []string{"run", "-i", "--rm", "mcp/example"},
	})

	c.killDockerContainerByCommandWithContext(context.Background())

	assertForeignUntouched(t, fd, mainLogs, upLogs)
	assert.Empty(t, fd.mutationsOf(t, unrelatedID), "a container merely sharing the image was mutated")
	assert.Empty(t, recordsMentioning(upLogs, unrelatedID), "a container merely sharing the image was written into a's log")
	assert.Empty(t, recordsMentioning(mainLogs, unrelatedID))
	for _, line := range fd.invocations(t) {
		f := strings.Fields(line)
		if len(f) > 0 {
			assert.NotContains(t, []string{"rm", "stop", "kill"}, f[0], "no container may be mutated: %q", line)
		}
	}
}

// FR007-G5 unit matcher table, driven through ensureNoExistingContainers so
// it compiles against HEAD: ownership = label com.mcpproxy.server == raw
// server name AND name =~ ^mcpproxy-<sanitised>-[a-z0-9]{4}$. Rows cover
// `a` vs `a-b` vs `a/b` vs `A` (the label is the only signal that separates
// a/b from a-b — both sanitise to mcpproxy-a-b-*), pre-label containers and
// the regex guard.
func TestDockerCleanup_OwnershipMatcherTable(t *testing.T) {
	cases := []struct {
		name   string
		server string
		cname  string
		labels map[string]string
		owned  bool
	}{
		{"own label and canonical name", "a", "mcpproxy-a-wxyz", map[string]string{ownerLabel: "a"}, true},
		{"a-b container, server a", "a", "mcpproxy-a-b-wxyz", map[string]string{ownerLabel: "a-b"}, false},
		{"a/b container, server a", "a", "mcpproxy-a-b-wxyz", map[string]string{ownerLabel: "a/b"}, false},
		{"case-different label", "a", "mcpproxy-a-wxyz", map[string]string{ownerLabel: "A"}, false},
		{"case-different name and label", "a", "mcpproxy-A-wxyz", map[string]string{ownerLabel: "A"}, false},
		{"pre-label container", "a", "mcpproxy-a-wxyz", map[string]string{}, false},
		{"label mismatch, canonical name", "a", "mcpproxy-a-wxyz", map[string]string{ownerLabel: "a-b"}, false},
		{"own label, name with extra segment", "a", "mcpproxy-a-wxyz-extra", map[string]string{ownerLabel: "a"}, false},
		{"own label, uppercase suffix", "a", "mcpproxy-a-WXYZ", map[string]string{ownerLabel: "a"}, false},
		{"own label, short suffix", "a", "mcpproxy-a-wxy", map[string]string{ownerLabel: "a"}, false},
		{"server a/b owns its container", "a/b", "mcpproxy-a-b-wxyz", map[string]string{ownerLabel: "a/b"}, true},
		{"server a/b vs a-b's container", "a/b", "mcpproxy-a-b-wxyz", map[string]string{ownerLabel: "a-b"}, false},
		{"server a-b owns its container", "a-b", "mcpproxy-a-b-wxyz", map[string]string{ownerLabel: "a-b"}, true},
		{"server a-b vs a/b's container", "a-b", "mcpproxy-a-b-wxyz", map[string]string{ownerLabel: "a/b"}, false},
		{"server A vs a's container", "A", "mcpproxy-a-wxyz", map[string]string{ownerLabel: "a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const id = "0123456789ab"
			fd := installFakeDocker(t, []fakeContainer{{ID: id, Name: tc.cname, Image: "img", Status: "Up", Labels: tc.labels}})
			c, mainLogs, upLogs := newOwnershipClient(tc.server, nil)

			require.NoError(t, c.ensureNoExistingContainers(context.Background()))

			mutated := fd.mutationsOf(t, id)
			if tc.owned {
				assert.NotEmpty(t, mutated, "owned container must be removed; invocations:\n%s", strings.Join(fd.invocations(t), "\n"))
			} else {
				assert.Empty(t, mutated, "foreign container removed by server %q", tc.server)
				assert.Empty(t, recordsMentioning(upLogs, id), "foreign container id written into %q's per-server log", tc.server)
				assert.Empty(t, recordsMentioning(upLogs, tc.cname), "foreign container name written into %q's per-server log", tc.server)
				assert.Empty(t, recordsMentioning(mainLogs, id), "foreign container id logged under server=%q", tc.server)
			}
		})
	}
}

// Critique round 1, finding C1.5: the pre-start sweep's count record is a
// container subject (internal/logs D8 rule 3 treats `container_count` like
// `container_id`), so the record written to the per-server log must carry
// `container_owner` == this server or the attributed reader withholds it
// from the server's own scoped agent.
func TestDockerCleanup_CountRecordCarriesContainerOwner(t *testing.T) {
	installFakeDocker(t, ownAndForeignFixture())
	c, _, upLogs := newOwnershipClient("a", nil)

	require.NoError(t, c.ensureNoExistingContainers(context.Background()))

	counts := upLogs.FilterMessage("Cleaning up existing containers before creating new one").All()
	require.Len(t, counts, 1)
	fields := counts[0].ContextMap()
	assert.EqualValues(t, 1, fields["container_count"], "the count is of a's own containers only")
	assert.Equal(t, "a", fields["container_owner"], "count record must carry container_owner so a's scoped reader can attribute it")
}

// Critique round 1, finding C2.3: ownsContainer is the Go-side half of the
// D9 belt-and-braces (docker filters server-side, Go re-checks). The table
// above drives it through the shim, which honours the same filters, so a
// predicate that returned true for everything still passed. This is the
// direct table, plus a filter-blind shim mode below.
func TestOwnsContainer_Predicate(t *testing.T) {
	cases := []struct {
		name   string
		server string
		cname  string
		label  string
		owned  bool
	}{
		{"own label and canonical name", "a", "mcpproxy-a-wxyz", "a", true},
		{"docker-style leading slash is not canonical", "a", "/mcpproxy-a-wxyz", "a", false},
		{"a-b container, server a", "a", "mcpproxy-a-b-wxyz", "a-b", false},
		{"a/b container, server a", "a", "mcpproxy-a-b-wxyz", "a/b", false},
		{"case-different label", "a", "mcpproxy-a-wxyz", "A", false},
		{"pre-label container", "a", "mcpproxy-a-wxyz", "", false},
		{"label mismatch, canonical name", "a", "mcpproxy-a-wxyz", "a-b", false},
		{"own label, name with extra segment", "a", "mcpproxy-a-wxyz-extra", "a", false},
		{"own label, uppercase suffix", "a", "mcpproxy-a-WXYZ", "a", false},
		{"own label, short suffix", "a", "mcpproxy-a-wxy", "a", false},
		{"own label, long suffix", "a", "mcpproxy-a-wxyz1", "a", false},
		{"own label, wrong prefix", "a", "other-a-wxyz", "a", false},
		{"server a/b owns its container", "a/b", "mcpproxy-a-b-wxyz", "a/b", true},
		{"server a/b vs a-b's container", "a/b", "mcpproxy-a-b-wxyz", "a-b", false},
		{"server a-b owns its container", "a-b", "mcpproxy-a-b-wxyz", "a-b", true},
		{"server a-b vs a/b's container", "a-b", "mcpproxy-a-b-wxyz", "a/b", false},
		{"server A vs a's container", "A", "mcpproxy-a-wxyz", "a", false},
		// The sanitiser keeps '.', so a.b names mcpproxy-a.b-*; QuoteMeta keeps
		// the dot literal in the pattern rather than a wildcard.
		{"regex metacharacters in the name are literal", "a.b", "mcpproxy-a.b-wxyz", "a.b", true},
		{"regex metacharacters do not widen the match", "a.b", "mcpproxy-aXb-wxyz", "a.b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.owned, ownsContainer(tc.server, tc.cname, tc.label))
		})
	}
}

// Critique round 1, finding C2.3: with the shim ignoring every --filter (a
// daemon that returned rows the filters should have dropped), listOwnedContainers
// must drop them itself; the pre-start sweep then still removes only a's own
// container and never names the foreign one.
func TestDockerCleanup_GoPredicateDropsRowsTheDaemonDidNotFilter(t *testing.T) {
	fd := installFakeDocker(t, ownAndForeignFixture())
	t.Setenv(fakeDockerIgnoreFiltersEnv, "1")
	c, mainLogs, upLogs := newOwnershipClient("a", nil)

	owned, err := c.listOwnedContainers(context.Background(), true)
	require.NoError(t, err)
	require.Len(t, owned, 1, "only a's own container survives the Go-side predicate; got %+v", owned)
	assert.Equal(t, ownContainerID, owned[0].ID)

	require.NoError(t, c.ensureNoExistingContainers(context.Background()))
	assertForeignUntouched(t, fd, mainLogs, upLogs)
	assert.NotEmpty(t, fd.mutationsOf(t, ownContainerID))
}

// shortenCidfilePoll makes readContainerIDWithContext give up on the cidfile
// almost immediately (the production wait is 10 s) so the name-recovery
// fallback is reachable in a unit test.
func shortenCidfilePoll(t *testing.T) {
	t.Helper()
	attempts, interval := cidfileReadAttempts, cidfileReadInterval
	cidfileReadAttempts, cidfileReadInterval = 2, time.Millisecond
	t.Cleanup(func() { cidfileReadAttempts, cidfileReadInterval = attempts, interval })
}

// Codex round 1 (PR E), finding 2: the cidfile path. A user-configured
// direct `docker run --name custom image` upstream gets --cidfile injected
// but carries neither the com.mcpproxy.server label nor a canonical name, so
// it fails ownsContainer on both halves. The pre-fix code recorded its id as
// owned, wrote it into a's per-server log with a fabricated
// container_owner=a, and stopped/killed it on disconnect. Under D9 a
// user-`--name` container is not ours: the id captured from the cidfile
// must be inspected, and a container that fails ownership is left alone and
// never named in a's per-server log.
func TestDockerCleanup_CidfileContainerMustPassOwnership(t *testing.T) {
	const customID = "c0ffee000001"
	const customFullID = customID + "0000000000000000000000000000000000000000000000000000"
	cases := []struct {
		name  string
		row   fakeContainer
		owned bool
	}{
		{"user --name custom, no label", fakeContainer{ID: customID, Name: "custom", Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{}}, false},
		{"own label, user --name custom via extra_args", fakeContainer{ID: customID, Name: "custom", Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{ownerLabel: "a"}}, false},
		{"foreign label, canonical-looking name", fakeContainer{ID: customID, Name: "mcpproxy-a-wxyz", Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{ownerLabel: "a-b"}}, false},
		{"own label and canonical name", fakeContainer{ID: customID, Name: "mcpproxy-a-wxyz", Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{ownerLabel: "a"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := installFakeDocker(t, []fakeContainer{tc.row})
			c, _, upLogs := newOwnershipClient("a", &config.ServerConfig{
				Command: "docker", Args: []string{"run", "-i", "--rm", "--name", "custom", "mcp/example"},
			})

			cidFile := filepath.Join(t.TempDir(), "cid")
			require.NoError(t, os.WriteFile(cidFile, []byte(customFullID+"\n"), 0o600))
			c.readContainerIDWithContext(context.Background(), cidFile)

			c.mu.Lock()
			c.killDockerContainerWithContext(context.Background())
			c.mu.Unlock()

			mutated := append(fd.mutationsOf(t, customFullID), fd.mutationsOf(t, customID)...)
			if tc.owned {
				assert.NotEmpty(t, mutated, "a's own container must still be stopped; invocations:\n%s", strings.Join(fd.invocations(t), "\n"))
				for _, entry := range upLogs.All() {
					if owner, ok := entry.ContextMap()["container_owner"]; ok {
						assert.Equal(t, "a", owner, "container_owner must be the label read back")
					}
				}
				return
			}
			assert.Empty(t, mutated, "container that fails ownership was stopped/killed: %v", mutated)
			for _, needle := range []string{customFullID, customID, tc.row.Name} {
				assert.Empty(t, recordsMentioning(upLogs, needle), "unowned %q written into a's per-server log", needle)
			}
			for _, entry := range upLogs.All() {
				_, has := entry.ContextMap()["container_owner"]
				assert.False(t, has, "container_owner fabricated on record %q", entry.Message)
			}
		})
	}
}

// Codex round 1 (PR E), finding 3: the exact-name paths (cidfile recovery
// by name and killDockerContainerByNameWithContext) filtered by label and
// the tracked name only, never applied ownsContainer, and wrote
// container_owner from the REQUESTED server rather than the label read
// back. A foreign `--label com.mcpproxy.server=a --name custom` container
// whose name is the tracked one must be neither stopped nor named in a's
// log; an owned canonical container on the same paths still is, with
// container_owner equal to its label.
func TestDockerCleanup_ExactNamePathsApplyOwnership(t *testing.T) {
	const foreignID = "f0e1d2c3b4a5"
	cases := []struct {
		name    string
		tracked string
		row     fakeContainer
		owned   bool
	}{
		{"foreign label=a --name custom", "custom",
			fakeContainer{ID: foreignID, Name: "custom", Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{ownerLabel: "a"}}, false},
		{"foreign label=a-b canonical-looking name", ownContainerName,
			fakeContainer{ID: foreignID, Name: ownContainerName, Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{ownerLabel: "a-b"}}, false},
		{"own label and canonical name", ownContainerName,
			fakeContainer{ID: ownContainerID, Name: ownContainerName, Image: "mcp/example", Status: "Up 1 second", Labels: map[string]string{ownerLabel: "a"}}, true},
	}
	for _, tc := range cases {
		t.Run("recovery/"+tc.name, func(t *testing.T) {
			fd := installFakeDocker(t, []fakeContainer{tc.row})
			c, _, upLogs := newOwnershipClient("a", nil)
			c.containerName = tc.tracked

			// A cidfile that never appears: the read times out and recovers by name.
			shortenCidfilePoll(t)
			c.readContainerIDWithContext(context.Background(), filepath.Join(t.TempDir(), "never-written"))

			if tc.owned {
				assert.Equal(t, tc.row.ID, c.containerID, "own container must be recovered by name")
				for _, entry := range upLogs.All() {
					if owner, ok := entry.ContextMap()["container_owner"]; ok {
						assert.Equal(t, "a", owner)
					}
				}
				return
			}
			assert.Empty(t, c.containerID, "foreign container recorded as owned via name recovery")
			assert.Empty(t, recordsMentioning(upLogs, tc.row.ID), "foreign id written into a's per-server log")
			assert.Empty(t, fd.mutationsOf(t, tc.row.ID))
		})
		t.Run("kill_by_name/"+tc.name, func(t *testing.T) {
			fd := installFakeDocker(t, []fakeContainer{tc.row})
			c, mainLogs, upLogs := newOwnershipClient("a", nil)

			ok := c.killDockerContainerByNameWithContext(context.Background(), tc.tracked)

			if tc.owned {
				assert.True(t, ok)
				assert.NotEmpty(t, fd.mutationsOf(t, tc.row.ID), "own container must be stopped; invocations:\n%s", strings.Join(fd.invocations(t), "\n"))
				for _, entry := range upLogs.All() {
					if owner, has := entry.ContextMap()["container_owner"]; has {
						assert.Equal(t, "a", owner, "container_owner must be the label read back")
					}
				}
				return
			}
			assert.False(t, ok)
			assert.Empty(t, fd.mutationsOf(t, tc.row.ID), "foreign container stopped/killed by exact name")
			assert.Empty(t, recordsMentioning(upLogs, tc.row.ID), "foreign id written into a's per-server log")
			assert.Empty(t, recordsMentioning(mainLogs, tc.row.ID), "foreign id written into main log under server=a")
			for _, entry := range upLogs.All() {
				_, has := entry.ContextMap()["container_owner"]
				assert.False(t, has, "container_owner fabricated on record %q", entry.Message)
			}
		})
	}
}
