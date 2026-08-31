package replaycorpus

import (
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testOptions returns Options that never reach the network or the real
// repository root: every test in this package runs offline, so the tiktoken
// vocabulary download and the working-tree probe are both replaced.
func testOptions(root string) Options {
	return Options{
		Warnf:    func(string, ...any) {},
		repoRoot: root,
		counter:  countingStub{},
	}
}

// countingStub is a deterministic stand-in for the tiktoken counter. It also
// records every string it was handed, which is what the privacy tests use to
// prove tokenization happened INSIDE this package.
type countingStub struct{ seen *[]string }

func (c countingStub) Count(text string) int {
	if c.seen != nil {
		*c.seen = append(*c.seen, text)
	}
	return len(strings.Fields(text))
}

const (
	jsonlToolCall = `{"id":"01","type":"tool_call","server_name":"github","tool_name":"create_issue","status":"success","work_session_id":"ws-1","request_id":"req-1","timestamp":"2026-08-01T10:00:00Z","request_bytes":120,"response_bytes":4096}`
	jsonlSecond   = `{"id":"02","type":"tool_call","server_name":"github","tool_name":"list_issues","status":"success","work_session_id":"ws-1","request_id":"req-2","timestamp":"2026-08-01T10:00:05Z","request_bytes":30,"response_bytes":900}`
	jsonlOther    = `{"id":"03","type":"tool_call","server_name":"slack","tool_name":"post","status":"error","work_session_id":"ws-2","request_id":"req-3","timestamp":"2026-08-01T11:00:00Z","request_bytes":10,"response_bytes":20}`
)

func loadString(t *testing.T, jsonl string, opts Options) *Corpus {
	t.Helper()
	c, err := Load(strings.NewReader(jsonl), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestLoad_DecodesJSONL(t *testing.T) {
	c := loadString(t, jsonlToolCall+"\n"+jsonlSecond+"\n", testOptions(t.TempDir()))
	if c.RecordsRead != 2 {
		t.Fatalf("RecordsRead = %d, want 2", c.RecordsRead)
	}
	if len(c.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(c.Sessions))
	}
	s := c.Sessions[0]
	if s.WorkSessionID != "ws-1" {
		t.Errorf("WorkSessionID = %q, want ws-1", s.WorkSessionID)
	}
	if s.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2", s.CallCount)
	}
	if got := s.Calls[0].ToolName; got != "create_issue" {
		t.Errorf("Calls[0].ToolName = %q, want create_issue", got)
	}
	if got := s.Calls[0].ServerName; got != "github" {
		t.Errorf("Calls[0].ServerName = %q, want github", got)
	}
	if s.Calls[0].RequestBytes != 120 || s.Calls[0].ResponseBytes != 4096 {
		t.Errorf("byte lengths not carried: %+v", s.Calls[0])
	}
	if want := 5 * time.Second; s.Span() != want {
		t.Errorf("Span() = %v, want %v", s.Span(), want)
	}
}

func TestLoad_RejectsCSVByContent(t *testing.T) {
	csv := "id,type,server_name,tool_name,status\n01,tool_call,github,create_issue,success\n"
	_, err := Load(strings.NewReader(csv), testOptions(t.TempDir()))
	if !errors.Is(err, ErrCSVInput) {
		t.Fatalf("err = %v, want ErrCSVInput", err)
	}
	// The message must say WHY, not just "unsupported": an operator who reached
	// for CSV needs to know the export drops the fields replay depends on.
	for _, want := range []string{"work_session_id", "--format json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadFile_RejectsCSVByExtension(t *testing.T) {
	// The repository root is elsewhere, so the CSV refusal is what this test
	// actually exercises rather than the working-tree refusal.
	path := filepath.Join(t.TempDir(), "activity.csv")
	writeFile(t, path, jsonlToolCall+"\n") // JSON content, CSV name: still refused
	_, err := LoadFile(path, testOptions(t.TempDir()))
	if !errors.Is(err, ErrCSVInput) {
		t.Fatalf("err = %v, want ErrCSVInput", err)
	}
}

func TestLoad_GroupsByWorkSessionID(t *testing.T) {
	c := loadString(t, strings.Join([]string{jsonlToolCall, jsonlOther, jsonlSecond}, "\n"), testOptions(t.TempDir()))
	if len(c.Sessions) != 2 {
		t.Fatalf("len(Sessions) = %d, want 2", len(c.Sessions))
	}
	// Deterministic order: earliest first record wins, ties broken by id.
	if c.Sessions[0].WorkSessionID != "ws-1" || c.Sessions[1].WorkSessionID != "ws-2" {
		t.Fatalf("session order = %q, %q; want ws-1, ws-2",
			c.Sessions[0].WorkSessionID, c.Sessions[1].WorkSessionID)
	}
	if c.Sessions[0].CallCount != 2 || c.Sessions[1].CallCount != 1 {
		t.Errorf("call counts = %d, %d; want 2, 1", c.Sessions[0].CallCount, c.Sessions[1].CallCount)
	}
}

func TestLoad_JoinsCodeExecutionSubCalls(t *testing.T) {
	parent := `{"id":"10","type":"internal_tool_call","tool_name":"code_execution","status":"success","work_session_id":"ws-1","request_id":"req-parent","timestamp":"2026-08-01T10:00:00Z"}`
	// Sub-calls carry no work_session_id of their own — they are attributed
	// through the parent, which is the whole point of the join.
	sub1 := `{"id":"11","type":"tool_call","server_name":"github","tool_name":"list_issues","status":"success","parent_id":"req-parent","request_id":"req-sub-1","timestamp":"2026-08-01T10:00:01Z"}`
	sub2 := `{"id":"12","type":"tool_call","server_name":"github","tool_name":"get_issue","status":"success","parent_id":"req-parent","request_id":"req-sub-2","timestamp":"2026-08-01T10:00:02Z"}`

	c := loadString(t, strings.Join([]string{parent, sub1, sub2}, "\n"), testOptions(t.TempDir()))
	if len(c.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(c.Sessions))
	}
	s := c.Sessions[0]
	// Not double-counted: the sub-calls must NOT also appear at top level.
	if s.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1 (sub-calls hang off the parent)", s.CallCount)
	}
	if s.SubCallCount != 2 {
		t.Errorf("SubCallCount = %d, want 2", s.SubCallCount)
	}
	if got := len(s.Calls[0].SubCalls); got != 2 {
		t.Fatalf("len(SubCalls) = %d, want 2", got)
	}
	// Not orphaned: every record is reachable exactly once.
	all := s.AllCalls()
	if len(all) != 3 {
		t.Fatalf("len(AllCalls()) = %d, want 3", len(all))
	}
	seen := map[string]int{}
	for _, call := range all {
		seen[call.ID]++
	}
	for _, id := range []string{"10", "11", "12"} {
		if seen[id] != 1 {
			t.Errorf("record %s appears %d times, want exactly 1", id, seen[id])
		}
	}
	if c.Exclusions.OrphanedSubCalls != 0 {
		t.Errorf("OrphanedSubCalls = %d, want 0", c.Exclusions.OrphanedSubCalls)
	}
}

func TestLoad_OrphanedSubCallIsKeptAndCounted(t *testing.T) {
	// The parent code_execution is outside the exported window. Dropping the
	// sub-call would understate the workload; silently promoting it without
	// saying so would misattribute it. It is kept AND counted.
	orphan := `{"id":"20","type":"tool_call","server_name":"github","tool_name":"list_issues","status":"success","work_session_id":"ws-9","parent_id":"req-missing","request_id":"req-sub","timestamp":"2026-08-01T10:00:01Z","response_bytes":50}`
	c := loadString(t, orphan, testOptions(t.TempDir()))
	if c.Exclusions.OrphanedSubCalls != 1 {
		t.Fatalf("OrphanedSubCalls = %d, want 1", c.Exclusions.OrphanedSubCalls)
	}
	if len(c.Sessions) != 1 || c.Sessions[0].CallCount != 1 {
		t.Fatalf("orphan was not kept at top level: %+v", c.Sessions)
	}
}

func TestLoad_UnattributedRecordIsDroppedAndCounted(t *testing.T) {
	noSession := `{"id":"30","type":"tool_call","server_name":"github","tool_name":"list_issues","status":"success","request_id":"req-x","timestamp":"2026-08-01T10:00:00Z"}`
	c := loadString(t, noSession, testOptions(t.TempDir()))
	if len(c.Sessions) != 0 {
		t.Fatalf("len(Sessions) = %d, want 0", len(c.Sessions))
	}
	if got := c.Exclusions.Dropped[ReasonUnattributed]; got != 1 {
		t.Fatalf("Dropped[%s] = %d, want 1", ReasonUnattributed, got)
	}
	if c.Exclusions.TotalDropped() != 1 {
		t.Errorf("TotalDropped() = %d, want 1", c.Exclusions.TotalDropped())
	}
}

func TestLoad_NonCallRecordIsDroppedAndCounted(t *testing.T) {
	quarantine := `{"id":"40","type":"quarantine_change","server_name":"evil","status":"approved","work_session_id":"ws-1","timestamp":"2026-08-01T10:00:00Z"}`
	c := loadString(t, quarantine, testOptions(t.TempDir()))
	if len(c.Sessions) != 0 {
		t.Fatalf("len(Sessions) = %d, want 0", len(c.Sessions))
	}
	if got := c.Exclusions.Dropped[ReasonNotACall]; got != 1 {
		t.Errorf("Dropped[%s] = %d, want 1", ReasonNotACall, got)
	}
}

func TestLoad_MalformedLineIsAnError(t *testing.T) {
	_, err := Load(strings.NewReader(jsonlToolCall+"\nnot json\n"), testOptions(t.TempDir()))
	if err == nil {
		t.Fatal("want an error for malformed JSONL, got nil")
	}
	if !strings.Contains(err.Error(), "record 2") {
		t.Errorf("error %q does not locate the bad record", err)
	}
}

func TestLoad_EmptyInputIsWarnedNotSilent(t *testing.T) {
	c := loadString(t, "", testOptions(t.TempDir()))
	if c.RecordsRead != 0 {
		t.Fatalf("RecordsRead = %d, want 0", c.RecordsRead)
	}
	if len(c.Warnings) == 0 {
		t.Error("an empty export produced no warning — silence is never success")
	}
}

// TestPackageImportsNothingFromBench guards the invariant that gives this
// package its existence (see doc.go, invariant 1). Violating it does NOT break
// this package's build — it breaks bench/replay.go, one package away, with an
// import cycle — so the rule needs a test that fails HERE, where the mistake
// would be made.
func TestPackageImportsNothingFromBench(t *testing.T) {
	const benchPkg = "github.com/smart-mcp-proxy/mcpproxy-go/bench"

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for name, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, imp := range file.Imports {
				value, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("%s: unquote import %s: %v", path, imp.Path.Value, err)
				}
				if value == benchPkg || strings.HasPrefix(value, benchPkg+"/") {
					t.Errorf("%s (package %s) imports %q — bench/replay.go could then not import this package without a cycle", path, name, value)
				}
			}
		}
	}
}
