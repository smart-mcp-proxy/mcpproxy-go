package logs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 FR-007 (research D8): per-record log ownership.
//
// Two raw server names can share ONE per-server log file — `a/b` and `a_b`
// both sanitise to server-a_b.log (sanitizeServerLogName), and on a
// case-insensitive filesystem so do `A` and `a` — so a scoped caller tailing
// "its" server must receive only the records its server actually wrote. There
// is NO new field for that (D8): every per-server writer already stamps each
// record with `server=<raw name>` (NewUpstreamServerLogger), so administrator
// records and the whole-file reader (ReadUpstreamServerLogTail) are
// byte-identical to before. Unforgeability comes from two rules:
//
//  1. Producer rule: child-controlled text (stderr lines, launcher-pumped
//     stdout/stderr, docker CLI output) is only ever a zap FIELD VALUE, where
//     the encoder escapes it inside the fields object, on a record stamped
//     child_output=true (ChildOutputField). internal/upstream/core audits
//     every per-server log call site for a constant message. Files written
//     by builds before codex round 2 carry the launcher-pumped child line as
//     the MESSAGE; rule 2 judges that shape and withholds it when the child
//     text carries a boundary or a record header.
//  2. Reader rule: a console-encoder record is
//     `ts | LEVEL | caller | msg | {fields}` (`caller` is absent on the
//     OAuth tee, which has no AddCaller). The reader takes the FIRST ` | {`
//     boundary on the line and accepts the record only if (a) the text in
//     front of the boundary starts with exactly ONE record header
//     (`ts | LEVEL | `, consoleHeaderPattern) and contains no second one,
//     and (b) the boundary's suffix decodes as exactly one complete JSON
//     object with no trailing bytes that is not itself a JSON-encoder record.
//     Child text inside a field value is escaped and cannot close the object
//     early or start a boundary. The reader never scans past a failed
//     boundary, and (a) is what makes a torn record harmless: a torn record
//     with no terminator (partial final write) followed by an appended
//     complete record shares one physical line, and the later record's
//     boundary would otherwise attribute the whole line — foreign fragment
//     included — to the later writer (codex round 1). A tear inside the
//     fragment's header leaves no header at offset 0; a tear anywhere after
//     it leaves the fragment's header in front of the later record's — two
//     headers (codex round 2). A fragment torn right after its caller
//     separator in front of a JSON-encoder record leaves one header and a
//     suffix that decodes: that suffix carries the JSON encoder's own
//     `level`/`ts`/`msg` keys, which a console fields object never does, so
//     (b) rejects it. A line that starts with `{` is a JSON-encoder record
//     (or a torn one) and is judged as that single object, never by the
//     console scan. Lines with no accepted boundary (pre-stamp records, torn
//     fragments, launcher-era child lines carrying ` | {` as the message)
//     are non-attributable and withheld from scoped callers.
//  3. Subject-evidence rule (historical records): a stamp proves who WROTE a
//     record, not that every subject it names is that server's. A record
//     that names a container is attributable only when it carries
//     `container_owner` (the container's com.mcpproxy.server label, written
//     by the housekeeping paths since Spec 105) equal to the requested
//     server; the sanitised container name is never evidence (`a/b` and
//     `a-b` both name mcpproxy-a-b-*). A record whose `server` field names
//     another server (a pre-105 callback-stop record routed through the
//     wrong logger) is withheld. Child output is a container subject too
//     when it mentions one: Docker's own `docker run` failure names the
//     colliding container's id and name (`a/b` and `a-b` both generate
//     mcpproxy-a-b-*), and that text reaches the per-server log as a child
//     line. The producers stamp every child line with `child_output=true`
//     (ChildOutputField), and a child-output record that mentions a container
//     id, a canonical container name or Docker's name-conflict phrase
//     (containerMentionPattern) is withheld unless it carries a matching
//     `container_owner` — which child output never does. The check runs over
//     the decoded CHILD-CONTROLLED values only (attributionChildMessageField
//     / attributionChildErrorField: the `message` field the stderr and
//     launcher producers write the child's line into, and the `error` field
//     of a record whose error re-emits the recent-stderr buffer — the
//     "Connection failed" record, codex round 3), never over the serialized
//     line: the writer stamp of a server that is
//     itself named like a container (`mcpproxy-tenant-abcd`) is not a
//     subject, so its ordinary child output stays attributable.
//
// Administrators, REST and the CLI keep the whole file (SC-005).

// consoleFieldsBoundary separates the console encoder's message from its
// fields object (getFileEncoder: ConsoleSeparator " | ", fields rendered as a
// JSON object).
const consoleFieldsBoundary = " | {"

// consoleHeaderPattern is the fixed-shape start of every console-encoder
// record this package writes (getFileEncoder: TimeEncoderOfLayout
// "2006-01-02T15:04:05.000Z07:00", CapitalLevelEncoder, ConsoleSeparator
// " | "). The text in front of an accepted boundary must start with exactly
// one of these and contain no other.
var consoleHeaderPattern = regexp.MustCompile(
	`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(?:Z|[+-]\d{2}:\d{2}) \| (?:DEBUG|INFO|WARN|ERROR|DPANIC|PANIC|FATAL) \| `)

// containerMentionPattern matches text that names a container: a full
// 64-hex container id (what the daemon's messages carry), a canonical
// mcpproxy container name (generateContainerName: mcpproxy-<sanitised>-<4
// alphanumerics>), or Docker's name-conflict phrase. Applied to the
// child-controlled values of child-output records only, and by
// RedactContainerMentions to status text served to scoped callers.
var containerMentionPattern = regexp.MustCompile(
	`\b[0-9a-f]{64}\b|\bmcpproxy-[A-Za-z0-9_.-]+-[a-z0-9]{4}\b|already in use by container`)

// containerMentionRedacted replaces every containerMentionPattern match in
// RedactContainerMentions.
const containerMentionRedacted = "[container]"

// RedactContainerMentions blanks every container id, canonical container
// name and Docker name-conflict phrase in s. The MCP status surface
// (`upstream_servers` tail_log / list `connection_status.last_error`, and
// the health detail derived from it) renders a connect error that re-emits
// the child's stderr — on a `docker run` name collision that text names the
// colliding container, which belongs to another server (`a/b` and `a-b`
// generate the same name). A scoped caller gets the same redaction whether
// or not a co-owner exists (FR-007: uniform, non-disclosing); administrators
// see the text unchanged (SC-005).
func RedactContainerMentions(s string) string {
	return containerMentionPattern.ReplaceAllLiteralString(s, containerMentionRedacted)
}

// Field names the attribution rules key on.
const (
	attributionServerField         = "server"
	attributionContainerOwnerField = "container_owner"
	attributionContainerIDField    = "container_id"
	attributionContainerNameField  = "container_name"
	attributionContainerCountField = "container_count"
	attributionChildOutputField    = "child_output"
	// Child-controlled values on a child-output record: the child's line
	// (stderr / launcher producers) and an error that re-emits the
	// recent-stderr buffer (recordConnectionFailure). Only these are
	// searched for a container mention.
	attributionChildMessageField = "message"
	attributionChildErrorField   = "error"

	// JSON-encoder entry keys (getJSONEncoder / zap.NewProductionEncoderConfig).
	// A console fields object never carries all three; a suffix that does is
	// a JSON-encoder record appended after a torn console fragment.
	jsonEncoderLevelKey   = "level"
	jsonEncoderTimeKey    = "ts"
	jsonEncoderMessageKey = "msg"
)

// ChildOutputField stamps a record whose payload is a child process's own
// output (stderr lines, launcher-pumped stdout/stderr, docker CLI output).
// Every producer that writes child text into the per-server log — always as
// a field VALUE, never the message (rule 1) — attaches it, so the attributed
// reader can apply the child-output subject rule (rule 3).
func ChildOutputField() zap.Field {
	return zap.Bool(attributionChildOutputField, true)
}

// ReadUpstreamServerLogTailAttributed reads the last N records of an upstream
// server log that are attributable to serverName (Spec 105 FR-007, research
// D8). Attribution is decided per record BEFORE the tail limit, so an
// interleaved co-owner record never displaces an attributable one from the
// returned window, and the returned length is the authorized tail length.
// Records with no accepted stamp, records stamped for another server and
// records failing the subject-evidence rule are withheld. Administrators use
// ReadUpstreamServerLogTail (whole file, byte-identical to pre-105).
func ReadUpstreamServerLogTailAttributed(config *config.LogConfig, serverName string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 50
	}
	if lines > 500 {
		lines = 500
	}

	filename := serverLogFilename(serverName)
	logFilePath, err := GetLogFilePathWithDir(config.LogDir, filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get log file path for server %s: %w", serverName, err)
	}

	if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
		return []string{}, nil
	}

	file, err := os.Open(logFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file for server %s: %w", serverName, err)
	}
	defer file.Close()

	// Filter first, limit second: only attributable records enter the window.
	// A line past the cap is skipped as non-attributable rather than aborting
	// the read: a shared file means a co-owner (or its child, whose lines the
	// launcher pumps up to 1 MiB) could otherwise make the scoped caller's
	// own tail fail until rotation — a response class that would depend on
	// the hidden co-owner (SC-001). The whole-file reader is untouched.
	var attributed []string
	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		line, ok, err := readBoundedLine(reader, attributedLineCap)
		if err != nil {
			return nil, fmt.Errorf("failed to read log file for server %s: %w", serverName, err)
		}
		if !ok {
			break
		}
		if recordAttributableTo(line, serverName) {
			attributed = append(attributed, line)
		}
	}

	if attributed == nil {
		return []string{}, nil
	}
	if len(attributed) <= lines {
		return attributed, nil
	}
	return attributed[len(attributed)-lines:], nil
}

// attributedLineCap bounds one rendered record the attributed reader will
// consider: a record whose content (terminator excluded) is longer than the
// cap is non-attributable (skipped), never fatal; exactly the cap is eligible.
const attributedLineCap = 1024 * 1024

// readBoundedLine returns the next line (without its terminator) and
// ok=true, or ok=false at end of input. A line whose content exceeds limit is
// consumed to its terminator and returned as an empty, non-attributable line
// so the caller keeps reading; only a genuine read error is returned.
func readBoundedLine(r *bufio.Reader, limit int) (string, bool, error) {
	var buf []byte
	overlong := false
	for {
		chunk, err := r.ReadSlice('\n')
		content := chunk
		if err == nil {
			content = chunk[:len(chunk)-1] // the terminator is not content
		}
		if !overlong {
			if len(buf)+len(content) > limit {
				overlong = true
				buf = nil
			} else {
				buf = append(buf, content...)
			}
		}
		switch {
		case err == nil:
			// Terminator reached.
			if overlong {
				return "", true, nil
			}
			return string(buf), true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue // more of the same line follows
		case errors.Is(err, io.EOF):
			if len(buf) == 0 && !overlong {
				return "", false, nil
			}
			if overlong {
				return "", true, nil
			}
			return string(buf), true, nil
		default:
			return "", false, err
		}
	}
}

// recordAttributableTo reports whether one rendered log line is attributable
// to serverName under the D8 reader and subject-evidence rules. It is
// encoder-agnostic: a line that is itself one complete JSON object is a
// JSON-encoder record; otherwise the console boundary scan applies, so a file
// written under both encoders over its lifetime is read correctly.
func recordAttributableTo(line, serverName string) bool {
	fields, ok := recordFields(line)
	if !ok {
		return false // no accepted stamp: legacy line, torn fragment, foreign shape
	}
	return fields.attributableTo(serverName)
}

// recordFields extracts the fields object of a rendered record, or ok=false
// when the line carries no accepted fields object.
func recordFields(line string) (attributionFields, bool) {
	// JSON encoder: the whole line is the record. A `{`-prefixed line that is
	// not exactly one object is torn or foreign; it never falls through to the
	// console scan, whose boundary could belong to a record appended after
	// the tear.
	if strings.HasPrefix(line, "{") {
		fields, ok := decodeExactlyOneObject(line)
		if !ok {
			return attributionFields{}, false
		}
		fields.applyChildOutputSubject()
		return fields, true
	}

	// Console encoder: the FIRST ` | {` boundary decides. A boundary whose
	// suffix does not decode is evidence of a torn or foreign prefix, so the
	// whole line is non-attributable; scanning on to a later boundary would
	// hand the prefix to whoever wrote the later record.
	idx := strings.Index(line, consoleFieldsBoundary)
	if idx < 0 {
		return attributionFields{}, false
	}
	// The text in front of the boundary must be exactly one record: a
	// header at offset 0 and no second header anywhere before the boundary.
	// A torn foreign record (no terminator) followed by an appended complete
	// record shares this physical line; a tear inside the fragment's header
	// leaves no header at offset 0, a tear anywhere after it leaves two.
	headers := consoleHeaderPattern.FindAllStringIndex(line[:idx], 2)
	if len(headers) != 1 || headers[0][0] != 0 {
		return attributionFields{}, false
	}
	start := idx + len(consoleFieldsBoundary) - 1 // at the '{'
	fields, ok := decodeExactlyOneObject(line[start:])
	if !ok || fields.jsonEncoderRecord {
		// A fields object that is itself a JSON-encoder record is a complete
		// record appended after a fragment torn right behind its separator.
		return attributionFields{}, false
	}
	fields.applyChildOutputSubject()
	return fields, true
}

// attributionFields is the subset of a record's top-level fields the
// attribution rules consult. Every occurrence of a key is kept: zap renders a
// logger's With fields first and the call's fields after them, so a record
// can legitimately carry the writer stamp AND a subject `server` field, and
// the rule is that ALL of them must agree.
type attributionFields struct {
	servers         []string
	containerOwners []string
	namesContainer  bool
	// childOutput marks a record whose payload is child process output
	// (ChildOutputField); such a record is a container subject when one of
	// its child-controlled values (childPayload) mentions a container.
	childOutput  bool
	childPayload []string
	// jsonEncoderRecord is set when the object carries all three JSON-encoder
	// entry keys, i.e. it is a whole JSON-encoder record rather than a console
	// record's fields object.
	jsonEncoderRecord bool
}

// applyChildOutputSubject marks a child-output record as a container subject
// when a child-controlled value names a container: Docker's own name-conflict
// error carries the colliding container's id and name, so the record needs
// container_owner like any other container record. The stamp fields and the
// serialized line are never searched — a server named like a container is
// not a subject (codex round 3).
func (f *attributionFields) applyChildOutputSubject() {
	if !f.childOutput {
		return
	}
	for _, value := range f.childPayload {
		if containerMentionPattern.MatchString(value) {
			f.namesContainer = true
			return
		}
	}
}

// attributableTo applies the stamp and subject-evidence rules.
func (f attributionFields) attributableTo(serverName string) bool {
	// Stamp: at least one `server` value, and every one exactly the requested
	// name — a callback record naming another server fails here.
	if len(f.servers) == 0 {
		return false
	}
	for _, s := range f.servers {
		if s != serverName {
			return false
		}
	}
	// Subject evidence: a container record needs container_owner == requested
	// name; without it (pre-105 housekeeping record) it is withheld, since the
	// sanitised container name cannot tell `a/b`'s container from `a-b`'s.
	if f.namesContainer && len(f.containerOwners) == 0 {
		return false
	}
	for _, owner := range f.containerOwners {
		if owner != serverName {
			return false
		}
	}
	return true
}

// decodeExactlyOneObject decodes s as exactly one complete JSON object with
// nothing after it, collecting the top-level fields the attribution rules
// use. A syntax error, a non-object value, a non-string value under an
// attribution key, or trailing bytes rejects the candidate.
func decodeExactlyOneObject(s string) (attributionFields, bool) {
	var fields attributionFields
	var hasLevel, hasTime, hasMessage bool
	dec := json.NewDecoder(strings.NewReader(s))

	tok, err := dec.Token()
	if err != nil {
		return attributionFields{}, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return attributionFields{}, false
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return attributionFields{}, false
		}
		key, ok := keyTok.(string)
		if !ok {
			return attributionFields{}, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return attributionFields{}, false
		}
		switch key {
		case attributionServerField:
			value, ok := decodeStringValue(raw)
			if !ok {
				return attributionFields{}, false
			}
			fields.servers = append(fields.servers, value)
		case attributionContainerOwnerField:
			value, ok := decodeStringValue(raw)
			if !ok {
				return attributionFields{}, false
			}
			fields.containerOwners = append(fields.containerOwners, value)
		case attributionContainerIDField, attributionContainerNameField, attributionContainerCountField:
			// A count is a container subject too: a pre-105 sweep record's
			// count of "existing containers" included a co-owner's.
			fields.namesContainer = true
		case attributionChildOutputField:
			var flag bool
			if err := json.Unmarshal(raw, &flag); err != nil {
				return attributionFields{}, false
			}
			fields.childOutput = fields.childOutput || flag
		case attributionChildMessageField, attributionChildErrorField:
			// Only string values are child payload; anything else is not a
			// child line and is left out of the subject check.
			if value, ok := decodeStringValue(raw); ok {
				fields.childPayload = append(fields.childPayload, value)
			}
		case jsonEncoderLevelKey:
			hasLevel = true
		case jsonEncoderTimeKey:
			hasTime = true
		case jsonEncoderMessageKey:
			hasMessage = true
		}
	}
	fields.jsonEncoderRecord = hasLevel && hasTime && hasMessage

	closeTok, err := dec.Token()
	if err != nil {
		return attributionFields{}, false
	}
	if delim, ok := closeTok.(json.Delim); !ok || delim != '}' {
		return attributionFields{}, false
	}
	// Exactly one object: nothing may follow it.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return attributionFields{}, false
	}
	return fields, true
}

func decodeStringValue(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}
