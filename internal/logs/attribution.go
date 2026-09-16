package logs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
//  1. Producer rule: child-controlled text (stderr lines, docker output) is
//     only ever a zap FIELD VALUE, where the encoder escapes it inside the
//     fields object. internal/upstream/core audits its upstreamLogger call
//     sites for that. The launcher-pumped path writes one child line per
//     record as the MESSAGE; rule 2 handles that shape and loggerWriter never
//     lets a newline into a message.
//  2. Reader rule: a console-encoder record is
//     `ts | LEVEL | caller | msg | {fields}`. The reader scans ` | {`
//     boundaries LEFT TO RIGHT and accepts the first whose suffix decodes as
//     exactly one complete JSON object with no trailing bytes. Child text that
//     contains ` | {` sits to the left of the encoder's own boundary, so its
//     suffix always carries the real fields object as trailing bytes and is
//     rejected; child text inside a field value is escaped and cannot close
//     the object early. A JSON-encoder record is the whole line. Lines with
//     no accepted boundary (pre-stamp records, torn fragments from two sinks
//     on one file) are non-attributable and withheld from scoped callers.
//  3. Subject-evidence rule (historical records): a stamp proves who WROTE a
//     record, not that every subject it names is that server's. A record
//     that names a container is attributable only when it carries
//     `container_owner` (the container's com.mcpproxy.server label, written
//     by the housekeeping paths since Spec 105) equal to the requested
//     server; the sanitised container name is never evidence (`a/b` and
//     `a-b` both name mcpproxy-a-b-*). A record whose `server` field names
//     another server (a pre-105 callback-stop record routed through the
//     wrong logger) is withheld.
//
// Administrators, REST and the CLI keep the whole file (SC-005).

// consoleFieldsBoundary separates the console encoder's message from its
// fields object (getFileEncoder: ConsoleSeparator " | ", fields rendered as a
// JSON object).
const consoleFieldsBoundary = " | {"

// Field names the attribution rules key on.
const (
	attributionServerField         = "server"
	attributionContainerOwnerField = "container_owner"
	attributionContainerIDField    = "container_id"
	attributionContainerNameField  = "container_name"
)

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
	var attributed []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if recordAttributableTo(line, serverName) {
			attributed = append(attributed, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read log file for server %s: %w", serverName, err)
	}

	if attributed == nil {
		return []string{}, nil
	}
	if len(attributed) <= lines {
		return attributed, nil
	}
	return attributed[len(attributed)-lines:], nil
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
	// JSON encoder: the whole line is the record.
	if strings.HasPrefix(line, "{") {
		if fields, ok := decodeExactlyOneObject(line); ok {
			return fields, true
		}
	}

	// Console encoder: the first ` | {` boundary, scanning left to right,
	// whose suffix is exactly one complete JSON object.
	from := 0
	for {
		idx := strings.Index(line[from:], consoleFieldsBoundary)
		if idx < 0 {
			return attributionFields{}, false
		}
		start := from + idx + len(consoleFieldsBoundary) - 1 // at the '{'
		if fields, ok := decodeExactlyOneObject(line[start:]); ok {
			return fields, true
		}
		from = start
	}
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
		case attributionContainerIDField, attributionContainerNameField:
			fields.namesContainer = true
		}
	}

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
