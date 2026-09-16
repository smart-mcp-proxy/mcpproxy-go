package logs

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 FR-007 (gaps FR007-G1, G2, G3, G6; research D8): two raw server
// names can share ONE per-server log file — `a/b` and `a_b` both sanitise to
// server-a_b.log — and the pre-105 tail reader returned the last N lines of
// that file unfiltered, so an `a_b`-only agent token read `a/b`'s records.
// The attributed reader must return only records whose writer stamp
// (`server=<raw>`, logger.go NewUpstreamServerLogger) is exactly the requested
// name, filter BEFORE taking the last N, withhold every line with no accepted
// stamp (legacy plain lines, torn fragments), and apply the subject-evidence
// rule to historical container / callback records. The whole-file reader —
// what administrators, REST and the CLI use — stays byte-identical (SC-005).

// newAttributedLogDir returns a fresh log directory and a LogConfig pointing
// at it. os.MkdirTemp + best-effort RemoveAll (not t.TempDir) because the
// lumberjack sinks keep the file open until closed and a rotated backup can
// land after the closers ran; the existing logger_test.go uses the same
// pattern.
func newAttributedLogDir(t *testing.T, jsonFormat bool) *config.LogConfig {
	t.Helper()
	logDir, err := os.MkdirTemp("", "mcpproxy-attributed-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(logDir) })

	cfg := DefaultLogConfig()
	cfg.LogDir = logDir
	cfg.EnableFile = true
	cfg.EnableConsole = false
	cfg.JSONFormat = jsonFormat
	cfg.Compress = false
	return cfg
}

// openStampedWriter returns the REAL per-server writer for name (the one
// internal/upstream/core installs as upstreamLogger) and closes its sink at
// test end. Every record it emits carries the `server=<name>` stamp.
//
// The log file is pre-created so every sink opens it O_APPEND. lumberjack
// opens a NEW file O_TRUNC without O_APPEND, so when two writers share one
// file and the first creates it, the second's records are overwritten by the
// first's next write (the "torn fragment" corruption gap-map FR007-G3 probed;
// a retained effect, and torn fragments are non-attributable by rule). The
// fixtures here are about attribution, not about that corruption.
func openStampedWriter(t *testing.T, cfg *config.LogConfig, name string) *zap.Logger {
	t.Helper()
	logPath := filepath.Join(cfg.LogDir, ServerLogFilename(name))
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		require.NoError(t, os.WriteFile(logPath, nil, 0o600))
	}
	logger, closer, err := NewUpstreamServerLogger(cfg, name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	return logger
}

// stampedAs reports whether a rendered line carries the writer stamp for name
// under either encoder (the console encoder renders `"server": "x"`, the JSON
// encoder `"server":"x"`).
func stampedAs(line, name string) bool {
	return regexp.MustCompile(`"server":\s*"` + regexp.QuoteMeta(name) + `"`).MatchString(line)
}

// writeRecord emits one ordinary record and flushes it. All fixture records
// go through this single call site so the console encoder's caller segment
// is identical across records and only the payload differs.
func writeRecord(logger *zap.Logger, msg string, fields ...zap.Field) {
	logger.Info(msg, fields...)
	_ = logger.Sync()
}

// writeChildStderr mirrors the real child-stderr path exactly
// (internal/upstream/core/monitoring.go: Info("stderr", zap.String("message", line))).
func writeChildStderr(logger *zap.Logger, line string) {
	logger.Info("stderr", zap.String("message", line))
	_ = logger.Sync()
}

// writeChildStdoutMessage mirrors the launcher-pumped path
// (internal/upstream/core/connection_launcher.go loggerWriter: Info(line)),
// where the child's text IS the message.
func writeChildStdoutMessage(logger *zap.Logger, line string) {
	logger.Info(line)
	_ = logger.Sync()
}

func attributedTail(t *testing.T, cfg *config.LogConfig, name string, n int) []string {
	t.Helper()
	lines, err := ReadUpstreamServerLogTailAttributed(cfg, name, n)
	require.NoError(t, err)
	return lines
}

func wholeFileTail(t *testing.T, cfg *config.LogConfig, name string, n int) []string {
	t.Helper()
	lines, err := ReadUpstreamServerLogTail(cfg, name, n)
	require.NoError(t, err)
	return lines
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

type encoderCase struct {
	name string
	json bool
}

func encoderCases() []encoderCase {
	return []encoderCase{
		{"console_encoder", false},
		{"json_encoder", true},
	}
}

// FR007-G1: `a/b` and `a_b` share one file; a sentinel written by `a/b` must
// never be returned for `a_b`, under both encoders.
func TestReadUpstreamServerLogTail_AttributedOnly_CollidingNames(t *testing.T) {
	require.Equal(t, ServerLogFilename("a/b"), ServerLogFilename("a_b"),
		"fixture premise: the two raw names must sanitise to one log file")

	for _, enc := range encoderCases() {
		t.Run(enc.name, func(t *testing.T) {
			cfg := newAttributedLogDir(t, enc.json)
			slash := openStampedWriter(t, cfg, "a/b")
			under := openStampedWriter(t, cfg, "a_b")

			const sentinel = "SENTINEL-written-by-a-slash-b-9c1e"
			writeRecord(under, "own-record-1")
			writeRecord(slash, sentinel)
			writeRecord(under, "own-record-2")

			// Scoped reader for a_b: own records only, every line stamped a_b.
			got := attributedTail(t, cfg, "a_b", 50)
			body := joinLines(got)
			assert.NotContains(t, body, sentinel, "a/b's record leaked into a_b's attributed tail")
			assert.Contains(t, body, "own-record-1")
			assert.Contains(t, body, "own-record-2")
			for _, line := range got {
				assert.True(t, stampedAs(line, "a_b"), "every attributed line must carry the a_b stamp: %q", line)
			}

			// Scoped reader for a/b: the sentinel, none of a_b's records.
			got = attributedTail(t, cfg, "a/b", 50)
			body = joinLines(got)
			assert.Contains(t, body, sentinel)
			assert.NotContains(t, body, "own-record-1")
			assert.NotContains(t, body, "own-record-2")

			// Administrator control: the whole-file reader still returns all three.
			whole := joinLines(wholeFileTail(t, cfg, "a_b", 50))
			assert.Contains(t, whole, sentinel)
			assert.Contains(t, whole, "own-record-1")
			assert.Contains(t, whole, "own-record-2")
		})
	}
}

// FR007-G1 (D8 rules 1+2): child-controlled text is only ever a field value
// (stderr path) or the message (launcher path); neither can forge the writer
// stamp. A line `left | right | {"server":"a_b"}` emitted by `a/b` is
// attributed to `a/b` and never to `a_b`, for both encoders and both child
// paths, including the shapes that try to make an earlier ` | {` boundary
// decode as a complete JSON object.
func TestReadUpstreamServerLogTail_AttributedOnly_ChildTextCannotForgeOwner(t *testing.T) {
	childLines := []string{
		`left | right | {"server":"a_b"}`,
		`{"server":"a_b"}`,
		`x | {"server":"a_b"} | {"server":"a_b"}`,
		`left | {"server":"a_b"`,
		`{"x":"`,
		`{"x":"\`,
		`{"server":"a_b","message":"`,
	}

	for _, enc := range encoderCases() {
		t.Run(enc.name, func(t *testing.T) {
			for _, path := range []struct {
				name  string
				write func(*zap.Logger, string)
			}{
				{"stderr_field_value", writeChildStderr},
				{"launcher_message", writeChildStdoutMessage},
			} {
				t.Run(path.name, func(t *testing.T) {
					cfg := newAttributedLogDir(t, enc.json)
					slash := openStampedWriter(t, cfg, "a/b")
					under := openStampedWriter(t, cfg, "a_b")

					writeRecord(under, "own-record-before")
					for _, line := range childLines {
						path.write(slash, line)
					}
					writeRecord(under, "own-record-after")

					forUnder := attributedTail(t, cfg, "a_b", 50)
					underBody := joinLines(forUnder)
					require.Len(t, forUnder, 2, "a_b must see exactly its two own records, got:\n%s", underBody)
					assert.Contains(t, underBody, "own-record-before")
					assert.Contains(t, underBody, "own-record-after")
					for _, line := range childLines {
						assert.NotContains(t, underBody, line, "child text from a/b was attributed to a_b")
					}

					forSlash := attributedTail(t, cfg, "a/b", 50)
					require.Len(t, forSlash, len(childLines), "every child line is attributable to its real writer a/b, got:\n%s", joinLines(forSlash))
					assert.NotContains(t, joinLines(forSlash), "own-record-")
				})
			}
		})
	}
}

// FR007-G2: an unstamped line appended with O_APPEND (a pre-upgrade record, a
// hand-edited file, a torn fragment) is non-attributable: withheld from the
// scoped reader, still served by the whole-file reader.
func TestReadUpstreamServerLogTail_AttributedOnly_LegacyUnattributedWithheld(t *testing.T) {
	for _, enc := range encoderCases() {
		t.Run(enc.name, func(t *testing.T) {
			cfg := newAttributedLogDir(t, enc.json)
			under := openStampedWriter(t, cfg, "a_b")
			writeRecord(under, "own-record-1")

			logPath := filepath.Join(cfg.LogDir, ServerLogFilename("a_b"))
			f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
			require.NoError(t, err)
			const legacy = "LEGACY_PLAIN_LINE no stamp at all"
			const torn = ` | {"server":"a_b"`
			_, err = io.WriteString(f, legacy+"\n"+torn+"\n")
			require.NoError(t, err)
			require.NoError(t, f.Close())

			writeRecord(under, "own-record-2")

			got := attributedTail(t, cfg, "a_b", 50)
			body := joinLines(got)
			assert.NotContains(t, body, legacy, "unstamped legacy line served to the scoped reader")
			assert.NotContains(t, body, torn, "torn fragment served to the scoped reader")
			assert.Len(t, got, 2, "only the two stamped own records are attributable, got:\n%s", body)

			whole := joinLines(wholeFileTail(t, cfg, "a_b", 50))
			assert.Contains(t, whole, legacy, "administrator whole-file read must keep the legacy line")
			assert.Contains(t, whole, torn)
		})
	}
}

// FR007-G2 (D8 rule 3, spec.md "legacy records whose stamped identity
// conflicts with their subject are withheld"): a stamp is not evidence that
// the record's SUBJECT belongs to the stamped server. Container records are
// attributable only with container_owner == requested server (the sanitised
// name is never evidence: a/b and a-b both sanitise to mcpproxy-a-b-*);
// callback records naming another server are withheld. Administrators see all.
func TestReadUpstreamServerLogTail_AttributedOnly_SubjectEvidence(t *testing.T) {
	for _, enc := range encoderCases() {
		t.Run(enc.name, func(t *testing.T) {
			cfg := newAttributedLogDir(t, enc.json)
			a := openStampedWriter(t, cfg, "a")
			slash := openStampedWriter(t, cfg, "a/b")

			const foreignID = "deadbeef1234"
			const foreignName = "mcpproxy-a-b-wxyz"

			// server-a.log — every record below is stamped server=a.
			writeRecord(a, "own ordinary record")
			// (1) pre-upgrade cleanup record naming a-b's container by name.
			writeRecord(a, "Removing existing container",
				zap.String("container_id", foreignID),
				zap.String("container_name", foreignName),
				zap.String("status", "Up 3 minutes"))
			// (2) pre-upgrade record naming a container whose name LOOKS owned
			// but carries no container_owner — the sanitised name is not evidence.
			writeRecord(a, "Removing existing container",
				zap.String("container_id", "cafe000000aa"),
				zap.String("container_name", "mcpproxy-a-wxyz"))
			// (3) ID-only record from the disconnect fallback.
			writeRecord(a, "Killing container by name pattern",
				zap.String("container_id", foreignID))
			// (4) callback-stop record written through a's logger but naming b.
			writeRecord(a, "OAuth callback server stopped",
				zap.String("server", "b"),
				zap.String("bind_host", "127.0.0.1"),
				zap.Int("port", 54321))
			// (5) post-upgrade record whose owner is another server.
			writeRecord(a, "Removing existing container",
				zap.String("container_id", "feedface0001"),
				zap.String("container_name", "mcpproxy-a-wxyz"),
				zap.String("container_owner", "a-b"))
			// (6) callback-stop record naming a itself: subject matches.
			writeRecord(a, "OAuth callback server stopped",
				zap.String("server", "a"),
				zap.String("bind_host", "127.0.0.1"),
				zap.Int("port", 54322))
			// (7) post-upgrade housekeeping record owned by a.
			writeRecord(a, "Removing existing container",
				zap.String("container_id", "0123456789ab"),
				zap.String("container_name", "mcpproxy-a-wxyz"),
				zap.String("container_owner", "a"))

			got := attributedTail(t, cfg, "a", 50)
			body := joinLines(got)
			assert.Contains(t, body, "own ordinary record")
			assert.NotContains(t, body, foreignID, "foreign container id disclosed to a's scoped reader")
			assert.NotContains(t, body, foreignName, "foreign container name disclosed to a's scoped reader")
			assert.NotContains(t, body, "cafe000000aa", "container record without container_owner must be withheld")
			assert.NotContains(t, body, "54321", "callback record naming b's port disclosed to a")
			assert.NotContains(t, body, "feedface0001", "container owned by a-b disclosed to a")
			assert.Contains(t, body, "54322", "callback record naming a itself is attributable")
			assert.Contains(t, body, "0123456789ab", "post-upgrade record with container_owner=a is attributable")
			assert.Len(t, got, 3, "exactly: own ordinary, own callback-stop, own container record; got:\n%s", body)

			whole := joinLines(wholeFileTail(t, cfg, "a", 50))
			for _, s := range []string{foreignID, foreignName, "cafe000000aa", "54321", "feedface0001", "54322", "0123456789ab"} {
				assert.Contains(t, whole, s, "administrator whole-file read must keep every record")
			}

			// server-a_b.log — the same-sanitised-name case: `a/b` naming
			// mcpproxy-a-b-wxyz without container_owner is indistinguishable
			// from hidden `a-b`'s container and withheld; with container_owner
			// it is returned.
			writeRecord(slash, "Removing existing container",
				zap.String("container_id", foreignID),
				zap.String("container_name", foreignName))
			writeRecord(slash, "Removing existing container",
				zap.String("container_id", "abcdef012345"),
				zap.String("container_name", foreignName),
				zap.String("container_owner", "a/b"))

			got = attributedTail(t, cfg, "a/b", 50)
			body = joinLines(got)
			assert.NotContains(t, body, foreignID, "ownerless container record served to a/b")
			assert.Contains(t, body, "abcdef012345", "container_owner=a/b record must be returned to a/b")
			assert.Len(t, got, 1, "got:\n%s", body)

			whole = joinLines(wholeFileTail(t, cfg, "a/b", 50))
			assert.Contains(t, whole, foreignID)
			assert.Contains(t, whole, "abcdef012345")
		})
	}
}

// FR007-G3: ownership filtering precedes the tail limit, so an interleaved
// foreign line never displaces an authorized one from the returned window.
func TestReadUpstreamServerLogTail_AttributedOnly_InterleavedFilterBeforeLimit(t *testing.T) {
	for _, enc := range encoderCases() {
		t.Run(enc.name, func(t *testing.T) {
			cfg := newAttributedLogDir(t, enc.json)
			slash := openStampedWriter(t, cfg, "a/b")
			under := openStampedWriter(t, cfg, "a_b")

			writeRecord(under, "own1")
			writeRecord(slash, "foreign1")
			writeRecord(under, "own2")
			writeRecord(slash, "foreign2")

			got := attributedTail(t, cfg, "a_b", 2)
			require.Len(t, got, 2, "tail(a_b, 2) must be the last two OWN records, got:\n%s", joinLines(got))
			assert.Contains(t, got[0], "own1")
			assert.Contains(t, got[1], "own2")
			assert.NotContains(t, joinLines(got), "foreign")

			// Administrator control: last two raw lines are own2, foreign2.
			whole := wholeFileTail(t, cfg, "a_b", 2)
			require.Len(t, whole, 2)
			assert.Contains(t, whole[0], "own2")
			assert.Contains(t, whole[1], "foreign2")
		})
	}
}

// FR007-G6 / T053: names differing only by case collide only on a
// case-insensitive filesystem (macOS default, Windows); Linux CI writes two
// files. The attributed reader must give each name only its own records in
// both regimes; the administrator outcome is recorded per regime.
func TestReadUpstreamServerLogTail_AttributedOnly_CaseOnlyNames(t *testing.T) {
	cfg := newAttributedLogDir(t, false)
	upper := openStampedWriter(t, cfg, "A")
	lower := openStampedWriter(t, cfg, "a")

	writeRecord(lower, "lower-own-1")
	writeRecord(upper, "UPPER-SENTINEL")
	writeRecord(lower, "lower-own-2")

	upperInfo, err := os.Stat(filepath.Join(cfg.LogDir, ServerLogFilename("A")))
	require.NoError(t, err)
	lowerInfo, err := os.Stat(filepath.Join(cfg.LogDir, ServerLogFilename("a")))
	require.NoError(t, err)
	shared := os.SameFile(upperInfo, lowerInfo)
	t.Logf("case-only names share one file on this filesystem: %v", shared)

	got := attributedTail(t, cfg, "a", 50)
	body := joinLines(got)
	assert.NotContains(t, body, "UPPER-SENTINEL", "A's record served to a's scoped reader")
	assert.Contains(t, body, "lower-own-1")
	assert.Contains(t, body, "lower-own-2")
	assert.Len(t, got, 2, "got:\n%s", body)

	got = attributedTail(t, cfg, "A", 50)
	body = joinLines(got)
	assert.Contains(t, body, "UPPER-SENTINEL")
	assert.NotContains(t, body, "lower-own")
	assert.Len(t, got, 1, "got:\n%s", body)

	// Administrator outcome, recorded per regime: whole-file read of `a`
	// includes A's record only when the filesystem folded the two names.
	whole := joinLines(wholeFileTail(t, cfg, "a", 50))
	if shared {
		assert.Contains(t, whole, "UPPER-SENTINEL", "shared file: administrators see both writers")
	} else {
		assert.NotContains(t, whole, "UPPER-SENTINEL", "separate files: nothing to share")
	}
}

// T053: shared-file rotation and retention stay shared (spec FR-007 retained
// effect). A hidden co-owner's output can rotate an authorized record out of
// the readable history; the attributed reader then returns only what is still
// attributable in the current file — and never the co-owner's records.
func TestReadUpstreamServerLogTail_AttributedOnly_ForcedRotationSharedHistory(t *testing.T) {
	cfg := newAttributedLogDir(t, false)
	cfg.MaxSize = 1 // MB — lumberjack's minimum; the co-owner forces one rotation
	cfg.MaxBackups = 1
	slash := openStampedWriter(t, cfg, "a/b")
	under := openStampedWriter(t, cfg, "a_b")

	const sentinel = "a_b-record-before-rotation-77b2"
	writeRecord(under, sentinel)

	logPath := filepath.Join(cfg.LogDir, ServerLogFilename("a_b"))

	// a/b writes past MaxSize so lumberjack rotates the shared file.
	filler := strings.Repeat("x", 1024)
	for i := 0; i < 1100; i++ {
		slash.Info("co-owner filler", zap.String("payload", filler))
	}
	_ = slash.Sync()

	backups, err := filepath.Glob(filepath.Join(cfg.LogDir, "server-a_b-*.log"))
	require.NoError(t, err)
	require.NotEmpty(t, backups, "fixture premise: the co-owner's writes must have rotated %s", logPath)

	// Retained, documented effect: the pre-rotation own record is gone from
	// the current file for everyone — administrators included.
	whole := wholeFileTail(t, cfg, "a_b", 500)
	assert.NotContains(t, joinLines(whole), sentinel,
		"administrator outcome: a co-owner's rotation evicts the authorized record from the readable history")
	require.NotEmpty(t, whole, "the current file holds the co-owner's post-rotation records")

	// The scoped reader receives no co-owner record — an empty tail is the
	// correct answer here, a filler line is a disclosure.
	got := attributedTail(t, cfg, "a_b", 500)
	foreign := 0
	for _, line := range got {
		if !stampedAs(line, "a_b") {
			foreign++
		}
	}
	assert.Zero(t, foreign, "%d of %d lines served to a_b after rotation are not a_b's (co-owner filler disclosed)", foreign, len(got))
}

// Critique round 1, finding C1.2: one over-long record ANYWHERE in the shared
// file must not abort the scoped read. bufio.Scanner returns ErrTooLong for a
// line past its cap and the reader turned that into a tool error, so a hidden
// co-owner (or its child, whose lines pumpLines allows up to 1 MiB) could
// make `a_b`'s own tail fail until rotation — a response class that depends
// on the co-owner (SC-001). An over-long line is non-attributable and skipped,
// never fatal; the admin whole-file reader is untouched (SC-005).
func TestReadUpstreamServerLogTail_AttributedOnly_OverlongLineSkippedNotFatal(t *testing.T) {
	cfg := newAttributedLogDir(t, false)
	under := openStampedWriter(t, cfg, "a_b")

	writeRecord(under, "own-before-overlong")

	// A co-owner's over-long line, appended O_APPEND exactly as its sink
	// would leave it: stamped for a/b, longer than the reader's line cap.
	logPath := filepath.Join(cfg.LogDir, ServerLogFilename("a_b"))
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	overlong := `2026-09-16T00:00:00.000Z | INFO | x/y.go:1 | ` + strings.Repeat("Q", 2*1024*1024) + ` | {"server": "a/b"}` + "\n"
	_, err = f.WriteString(overlong)
	require.NoError(t, err)
	// And an over-long line stamped for a_b itself: withheld (non-attributable
	// past the cap), still not fatal.
	_, err = f.WriteString(`2026-09-16T00:00:00.000Z | INFO | x/y.go:1 | ` + strings.Repeat("R", 2*1024*1024) + ` | {"server": "a_b"}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	writeRecord(under, "own-after-overlong")

	got, err := ReadUpstreamServerLogTailAttributed(cfg, "a_b", 50)
	require.NoError(t, err, "an over-long co-owner line must be skipped, not turned into a scoped-caller error")
	body := joinLines(got)
	require.Len(t, got, 2, "exactly the two own records, got %d lines", len(got))
	assert.Contains(t, body, "own-before-overlong")
	assert.Contains(t, body, "own-after-overlong")
	assert.NotContains(t, body, "QQQQ", "co-owner's over-long line disclosed")
	assert.NotContains(t, body, "RRRR", "over-long own line must be withheld, not partially served")
}

// Critique round 1, finding C1.5: `container_count` is a container subject
// too. A pre-105 "Cleaning up existing containers before creating new one"
// record carries only a count — a count larger than the server's own
// container count hints at a hidden co-owner — so a record naming a count
// without `container_owner` is withheld like any other ownerless container
// record; with `container_owner` == the requested server it is served.
func TestReadUpstreamServerLogTail_AttributedOnly_ContainerCountIsSubjectEvidence(t *testing.T) {
	for _, enc := range encoderCases() {
		t.Run(enc.name, func(t *testing.T) {
			cfg := newAttributedLogDir(t, enc.json)
			a := openStampedWriter(t, cfg, "a")

			writeRecord(a, "own ordinary record")
			writeRecord(a, "Cleaning up existing containers before creating new one",
				zap.Int("container_count", 7))
			writeRecord(a, "Cleaning up existing containers before creating new one",
				zap.Int("container_count", 1),
				zap.String("container_owner", "a"))
			writeRecord(a, "Cleaning up existing containers before creating new one",
				zap.Int("container_count", 3),
				zap.String("container_owner", "a-b"))

			got := attributedTail(t, cfg, "a", 50)
			body := joinLines(got)
			assert.Contains(t, body, "own ordinary record")
			assert.NotContains(t, body, `"container_count": 7`, "ownerless container_count record served to a's scoped reader")
			assert.NotContains(t, body, `"container_count":7`)
			assert.NotContains(t, body, `"container_owner": "a-b"`, "container_count record owned by a-b served to a")
			assert.NotContains(t, body, `"container_owner":"a-b"`)
			assert.Len(t, got, 2, "exactly: own ordinary + own-owned count record; got:\n%s", body)

			whole := joinLines(wholeFileTail(t, cfg, "a", 50))
			for _, s := range []string{"container_count", "a-b"} {
				assert.Contains(t, whole, s, "administrator whole-file read must keep every record")
			}
		})
	}
}

// Critique round 1, finding C2.2: the reader keys on LINE boundaries and the
// console encoder writes a message verbatim, so a message carrying a line
// break starts a new line whose text the message author controls. The reader
// cannot defend against that by construction; the guarantee is the
// PRODUCER's (internal/upstream/core loggerWriter splits child output on
// '\n' before logging; monitoring.go keeps stderr as a field value). This
// test pins that division of labour: a line break inside a message DOES
// forge a record for another server under the console encoder, so any new
// producer that logs child text as a message must split on '\n' first.
func TestReadUpstreamServerLogTail_AttributedOnly_LineBreakInMessageIsProducerGuarantee(t *testing.T) {
	cfg := newAttributedLogDir(t, false)
	slash := openStampedWriter(t, cfg, "a/b")
	under := openStampedWriter(t, cfg, "a_b")

	writeRecord(under, "own-record")
	const forged = `2026-01-01T00:00:00.000Z | INFO | x/y.go:1 | FORGED-for-a_b | {"server": "a_b"}`
	// Bypasses every producer guard on purpose: the raw zap message. The
	// forged line sits in the MIDDLE of the message: the encoder appends its
	// own fields object to the message's last line, so a trailing forged line
	// would carry the real stamp as trailing bytes and be rejected; a middle
	// line stands alone.
	slash.Info("harmless-prefix\n" + forged + "\nharmless-trailer")
	_ = slash.Sync()

	got := attributedTail(t, cfg, "a_b", 50)
	body := joinLines(got)
	assert.Contains(t, body, "FORGED-for-a_b",
		"the reader is expected to be unable to reject a forged line the producer let through — "+
			"if this now fails, the reader grew a defence and the producer-side comment in attribution.go should be revisited")
	assert.Len(t, got, 2, "own record + the forged line, got:\n%s", body)

	// The JSON encoder escapes the break inside the message string, so the
	// same input yields exactly one a/b record and nothing for a_b.
	cfgJSON := newAttributedLogDir(t, true)
	slashJSON := openStampedWriter(t, cfgJSON, "a/b")
	slashJSON.Info("harmless-prefix\n" + forged + "\nharmless-trailer")
	_ = slashJSON.Sync()
	assert.Empty(t, attributedTail(t, cfgJSON, "a_b", 50), "JSON encoder must not let a message line break forge a record")
	assert.Len(t, attributedTail(t, cfgJSON, "a/b", 50), 1)
}
