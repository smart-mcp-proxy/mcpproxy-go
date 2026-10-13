package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Issue #922: recent OpenCode versions bootstrap ~/.config/opencode/opencode.jsonc
// (not opencode.json). mcpproxy must detect, read, and write the file OpenCode
// actually loads — preferring .jsonc, which shadows .json for the same keys.

func opencodeDir(home string) string {
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

func writeOpencodeFile(t *testing.T, home, name, content string) string {
	t.Helper()
	dir := opencodeDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const jsoncStub = "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"

func TestOpencodeConfigPathResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"no files -> default .json for create-new", nil, "opencode.json"},
		{"jsonc only -> jsonc", []string{"opencode.jsonc"}, "opencode.jsonc"},
		{"json only -> json", []string{"opencode.json"}, "opencode.json"},
		{"both -> jsonc (it shadows .json)", []string{"opencode.json", "opencode.jsonc"}, "opencode.jsonc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for _, f := range tc.files {
				writeOpencodeFile(t, home, f, jsoncStub)
			}
			s := NewServiceWithHome("127.0.0.1:8080", "key", home)
			got := s.configPath("opencode")
			if filepath.Base(got) != tc.want {
				t.Fatalf("configPath = %s, want basename %s", got, tc.want)
			}
			if filepath.Dir(got) != opencodeDir(home) {
				t.Fatalf("configPath dir = %s, want %s", filepath.Dir(got), opencodeDir(home))
			}
		})
	}
}

func TestConfigPathResolverLeavesOtherClientsAlone(t *testing.T) {
	home := t.TempDir()
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	if got, want := s.configPath("gemini"), ConfigPath("gemini", home); got != want {
		t.Fatalf("gemini configPath = %s, want %s", got, want)
	}
}

func TestUnmarshalLenientJSONComments(t *testing.T) {
	raw := []byte(`{
  // line comment
  "$schema": "https://opencode.ai/config.json", /* block comment */
  "note": "a // string with slashes and /* not a comment */",
  "mcp": {
    "x": { "url": "http://example/mcp", },
  }
}`)
	var data map[string]interface{}
	if err := unmarshalLenientJSON(raw, &data); err != nil {
		t.Fatalf("unmarshalLenientJSON: %v", err)
	}
	if data["note"] != "a // string with slashes and /* not a comment */" {
		t.Fatalf("string content mangled: %q", data["note"])
	}
	if _, ok := data["mcp"].(map[string]interface{})["x"]; !ok {
		t.Fatal("nested key lost")
	}
}

func TestConnectOpencodeJsoncOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	jsoncPath := writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	res, err := s.Connect("opencode", "mcpproxy", false)
	if err != nil {
		t.Fatalf("Connect on a .jsonc-only install must succeed: %v", err)
	}
	if !res.Success {
		t.Fatalf("Connect not successful: %+v", res)
	}
	if res.ConfigPath != jsoncPath {
		t.Fatalf("wrote to %s, want the existing %s", res.ConfigPath, jsoncPath)
	}
	// The entry must land INSIDE the .jsonc; no stray opencode.json created.
	if _, err := os.Stat(filepath.Join(opencodeDir(home), "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("a stray opencode.json was created next to opencode.jsonc")
	}
	raw, _ := os.ReadFile(jsoncPath)
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("rewritten .jsonc is not valid JSON: %v", err)
	}
	mcp, _ := data["mcp"].(map[string]interface{})
	if _, ok := mcp["mcpproxy"]; !ok {
		t.Fatalf("mcp.mcpproxy entry missing in %s: %s", jsoncPath, raw)
	}
	if data["$schema"] != "https://opencode.ai/config.json" {
		t.Fatal("$schema stub key lost on rewrite")
	}

	// Detection: status must report the client as installed + connected.
	st, err := s.GetStatus("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || !st.Connected {
		t.Fatalf("GetStatus after connect: Exists=%v Connected=%v, want true/true", st.Exists, st.Connected)
	}

	// Disconnect must also target the .jsonc.
	dres, err := s.Disconnect("opencode", "mcpproxy")
	if err != nil || !dres.Success {
		t.Fatalf("Disconnect: %v %+v", err, dres)
	}
}

func TestGetAllStatusSeesJsoncOnlyInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	for _, st := range s.GetAllStatus() {
		if st.ID == "opencode" {
			if !st.Exists {
				t.Fatal("GetAllStatus: opencode .jsonc-only install reported as not installed")
			}
			if filepath.Base(st.ConfigPath) != "opencode.jsonc" {
				t.Fatalf("GetAllStatus ConfigPath = %s, want the existing opencode.jsonc", st.ConfigPath)
			}
			return
		}
	}
	t.Fatal("opencode not in GetAllStatus")
}

func TestConnectOpencodeCommentedJsoncRefusedSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	content := "{\n  // keep my comments\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"
	p := writeOpencodeFile(t, home, "opencode.jsonc", content)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	_, err := s.Connect("opencode", "mcpproxy", false)
	if err == nil {
		t.Fatal("Connect must refuse to rewrite a commented .jsonc (comments would be lost)")
	}
	if !strings.Contains(err.Error(), "comment") {
		t.Fatalf("error should explain the comment refusal, got: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(after) != content {
		t.Fatal("commented .jsonc was modified despite the refusal")
	}
}

func TestUnmarshalLenientJSONEdgeCases(t *testing.T) {
	t.Run("unterminated block comment is an error, not silently valid", func(t *testing.T) {
		var data map[string]interface{}
		if err := unmarshalLenientJSON([]byte(`{"mcp":{}} /* unterminated`), &data); err == nil {
			t.Fatal("unterminated /* must not parse as valid JSONC")
		}
	})
	t.Run("escaped quote inside string does not end string state", func(t *testing.T) {
		var data map[string]interface{}
		raw := []byte(`{"k": "quote \" then // not a comment", "n": 1}`)
		if err := unmarshalLenientJSON(raw, &data); err != nil {
			t.Fatalf("escaped-quote input failed: %v", err)
		}
		if data["k"] != `quote " then // not a comment` {
			t.Fatalf("string mangled: %q", data["k"])
		}
	})
	t.Run("line comment at EOF without newline", func(t *testing.T) {
		var data map[string]interface{}
		if err := unmarshalLenientJSON([]byte("{\"n\": 1} // trailing"), &data); err != nil {
			t.Fatalf("EOF line comment failed: %v", err)
		}
	})
}

func TestDisconnectOpencodeCommentedJsoncRefusedWithoutBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	content := "{\n  // comments\n  \"mcp\": {\"mcpproxy\": {\"type\": \"remote\", \"url\": \"http://127.0.0.1:8080/mcp\"}}\n}\n"
	writeOpencodeFile(t, home, "opencode.jsonc", content)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	_, err := s.Disconnect("opencode", "mcpproxy")
	if err == nil {
		t.Fatal("Disconnect must refuse a commented .jsonc")
	}
	entries, _ := os.ReadDir(opencodeDir(home))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Fatalf("refusal must not leave a backup behind, found %s", e.Name())
		}
	}
}

// TestGuardJsoncComments_SkipsReadForNonJsoncPath is a regression guard for a
// defect a cross-model (ZCode) review caught in an earlier version of the
// shared-read fix: guardJsoncComments must check the ".jsonc" suffix BEFORE
// reading the file, exactly like it did before this change. guardJsoncComments
// itself has no production caller as of PR #1352 — preview.go now calls the
// read-free guardJsoncCommentsBytes(cfgPath, pre.raw) instead, sharing
// preWriteState's single read rather than opening the file again — but this
// pins the same suffix-before-read ordering in the reading entry point in case
// a future caller reaches for it.
func TestGuardJsoncComments_SkipsReadForNonJsoncPath(t *testing.T) {
	s := NewServiceWithHome("127.0.0.1:8080", "key", t.TempDir())
	reads := 0
	s.setReadFile(func(string) ([]byte, error) {
		reads++
		return nil, os.ErrNotExist
	})

	if err := s.guardJsoncComments("/some/path/config.json"); err != nil {
		t.Fatalf("guardJsoncComments on a non-.jsonc path: %v", err)
	}
	if reads != 0 {
		t.Fatalf("guardJsoncComments must not read a non-.jsonc path at all, got %d reads", reads)
	}
}

// TestConnectJSON_CommentGuardSharesOneReadWithParse proves the TOCTOU
// flagged in cross-model review of PR #1340: guardJsoncComments used to
// perform its OWN read of cfgPath, and readOrCreateJSON a few lines later
// performed a SEPARATE, independent read. If the file were comment-free when
// the guard read it but gained comments before readOrCreateJSON's later read,
// the guard passed on stale bytes while the parse/write silently normalized
// the now-commented file to plain JSON — stripping the comments the guard
// exists to protect. (Pre-existing; unrelated to PR #1340's actual fix.)
//
// PR #1352 folded the guard's read into preWriteState's own single pre-write
// read (connectJSON itself no longer performs any read — it parses pre.raw,
// the same bytes ConnectWithPrecondition already resolved), so these subtests
// now drive the fix through ConnectWithPrecondition rather than calling
// connectJSON directly with a stubbed `resolved` argument — connectJSON's
// signature no longer takes one; it takes the shared preWriteResult.
//
// The first subtest is the actual regression detector: it counts reads and
// requires 4 (preWriteState's shared guard+parse read; PR #1340's
// refuseIfServersSectionRaced pre-backup check; its atomicWriteFile
// preRename-hook check; then verifyJSONEntry's legitimate post-write read)
// where the vulnerable pre-#1340, pre-this-fix code needed 5 (a separate
// guard read, plus the same four) — this is the only externally observable
// difference between the two implementations, since a silent-strip bug
// produces byte-identical output to a legitimate comment-free write. The
// second subtest does NOT by itself distinguish fixed from vulnerable code —
// the vulnerable guard's own first read would also see the comments and
// refuse at the same point — but it separately documents that the shared
// read, when it does see comments, refuses rather than silently stripping
// them.
func TestConnectJSON_CommentGuardSharesOneReadWithParse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	commentFree := []byte(`{"$schema":"https://opencode.ai/config.json"}`)
	commented := []byte("{\n  // keep my comments\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n")

	t.Run("shared guard+parse read is comment-free: writes normally with 4 total reads, not 5", func(t *testing.T) {
		home := t.TempDir()
		writeOpencodeFile(t, home, "opencode.jsonc", string(commentFree))
		s := NewServiceWithHome("127.0.0.1:8080", "key", home)

		calls := 0
		s.setReadFile(func(path string) ([]byte, error) {
			calls++
			if calls == 1 {
				// The one read that must back BOTH the comment guard and the
				// parse — preWriteState's own read. A vulnerable,
				// two-independent-reads implementation would still be looking
				// at this same comment-free snapshot on its first (guard)
				// call too, then race ahead to a second, later call below for
				// its parse.
				return commentFree, nil
			}
			// PR #1340's refuseIfServersSectionRaced pre-backup and preRename
			// checks, and verifyJSONEntry's legitimate post-write re-read (and,
			// in a vulnerable implementation, the parse call the guard's read
			// should have shared) — all reflect the real file so none of them
			// spuriously fail.
			return os.ReadFile(path)
		})

		if _, err := s.ConnectWithPrecondition("opencode", "mcpproxy", false, ""); err != nil {
			t.Fatalf("ConnectWithPrecondition: unexpected error: %v", err)
		}
		// The fixed code reads once for preWriteState's shared guard+parse
		// step, twice more for PR #1340's refuseIfServersSectionRaced checks
		// (pre-backup and the atomicWriteFile preRename hook), and once more
		// for verifyJSONEntry's post-write check: 4 total. The vulnerable code
		// needed a separate guard read on top of all of those: 5.
		if calls != 4 {
			t.Fatalf("ConnectWithPrecondition should need 4 reads (shared guard+parse, the two #1340 race checks, then verify), got %d — a separate guard read is exactly the TOCTOU window this closes", calls)
		}
	})

	t.Run("single read is commented: refuses instead of silently stripping", func(t *testing.T) {
		home := t.TempDir()
		p := writeOpencodeFile(t, home, "opencode.jsonc", string(commentFree))
		s := NewServiceWithHome("127.0.0.1:8080", "key", home)

		calls := 0
		s.setReadFile(func(string) ([]byte, error) {
			calls++
			if calls == 1 {
				return commented, nil
			}
			return commentFree, nil
		})

		_, err := s.ConnectWithPrecondition("opencode", "mcpproxy", false, "")
		if err == nil {
			t.Fatal("ConnectWithPrecondition must refuse when the shared read sees comments, not silently strip them")
		}
		if !strings.Contains(err.Error(), "comment") {
			t.Fatalf("error should explain the comment refusal, got: %v", err)
		}
		if calls != 1 {
			t.Fatalf("ConnectWithPrecondition must read cfgPath exactly once before refusing; got %d reads", calls)
		}
		after, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(commentFree) {
			t.Fatalf("refused write must not touch the on-disk file, got %q", after)
		}
		entries, _ := os.ReadDir(opencodeDir(home))
		for _, e := range entries {
			if strings.Contains(e.Name(), ".bak.") {
				t.Fatalf("refusal must not leave a backup behind, found %s", e.Name())
			}
		}
	})
}

// TestDisconnectJSON_CommentGuardSharesOneRead is disconnectJSON's counterpart
// to TestConnectJSON_CommentGuardSharesOneReadWithParse: disconnectJSON had
// the identical guard-then-separate-read bug, fixed the identical way (check
// guardJsoncCommentsBytes against the bytes just read, no second s.read).
// disconnectJSON has no post-write verify step, so a successful disconnect
// needs exactly ONE read — the vulnerable code needed 2 (guard, then parse).
func TestDisconnectJSON_CommentGuardSharesOneRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	client := FindClient("opencode")
	if client == nil {
		t.Fatal("opencode client definition not found")
	}
	content := `{"mcp":{"mcpproxy":{"type":"remote","url":"http://127.0.0.1:8080/mcp","enabled":true}}}`
	home := t.TempDir()
	p := writeOpencodeFile(t, home, "opencode.jsonc", content)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	calls := 0
	s.setReadFile(func(string) ([]byte, error) {
		calls++
		return []byte(content), nil
	})

	if _, err := s.disconnectJSON(client, p, "mcpproxy"); err != nil {
		t.Fatalf("disconnectJSON: unexpected error: %v", err)
	}
	// The fixed code reads once (shared guard+parse); the vulnerable code read
	// separately for the guard and the parse: 2. disconnectJSON has no
	// post-write verify step, unlike connectJSON, so 1 is the whole budget.
	if calls != 1 {
		t.Fatalf("disconnectJSON should need exactly 1 read (shared guard+parse), got %d — a separate guard read is exactly the TOCTOU window this closes", calls)
	}
}

func TestDisconnectOpencodeFindsEntryInOtherCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	// Entry written into opencode.json first; opencode.jsonc bootstrapped later
	// (resolver now prefers it). Disconnect must still find and remove the entry.
	writeOpencodeFile(t, home, "opencode.json",
		`{"mcp":{"mcpproxy":{"type":"remote","url":"http://127.0.0.1:8080/mcp","enabled":true}}}`)
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	res, err := s.Disconnect("opencode", "mcpproxy")
	if err != nil || !res.Success {
		t.Fatalf("Disconnect across candidates: err=%v res=%+v", err, res)
	}
	raw, _ := os.ReadFile(filepath.Join(opencodeDir(home), "opencode.json"))
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if mcp, _ := data["mcp"].(map[string]interface{}); mcp != nil {
		if _, still := mcp["mcpproxy"]; still {
			t.Fatal("entry still present in opencode.json after cross-candidate disconnect")
		}
	}
}

func TestDisconnectOpencodeAlternateCandidateErrorPropagates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	// Resolved target: comment-free .jsonc without the entry. Alternate: a
	// commented .json... comments only guard .jsonc, so use malformed JSON to
	// force a real error on the alternate path instead.
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	writeOpencodeFile(t, home, "opencode.json", `{"mcp": not-valid-json`)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	_, err := s.Disconnect("opencode", "mcpproxy")
	if err == nil {
		t.Fatal("an unreadable/malformed alternate candidate must surface an error, not a silent not_found")
	}
}

func TestUndoOpencodeBackupTargetsItsOwnFileAfterDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	writeOpencodeFile(t, home, "opencode.json", `{"theme":"dark"}`)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	res, err := s.Connect("opencode", "mcpproxy", false)
	if err != nil || !res.Success || res.BackupPath == "" {
		t.Fatalf("connect: err=%v res=%+v", err, res)
	}
	backupName := filepath.Base(res.BackupPath)

	// OpenCode upgrade bootstraps a .jsonc — resolver drift.
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)

	ures, err := s.Undo("opencode", "mcpproxy", backupName)
	if err != nil || !ures.Success {
		t.Fatalf("undo after drift must accept the opencode.json backup: err=%v res=%+v", err, ures)
	}
	raw, _ := os.ReadFile(filepath.Join(opencodeDir(home), "opencode.json"))
	if string(raw) != `{"theme":"dark"}` {
		t.Fatalf("opencode.json not restored to pre-connect content: %s", raw)
	}
	jsoncRaw, _ := os.ReadFile(filepath.Join(opencodeDir(home), "opencode.jsonc"))
	if string(jsoncRaw) != jsoncStub {
		t.Fatal("undo must not touch the unrelated opencode.jsonc")
	}
}

func TestConnectOpencodeNoConfigMentionsBothCandidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	_, err := s.Connect("opencode", "mcpproxy", false)
	if err == nil {
		t.Fatal("Connect with no OpenCode install must still refuse")
	}
	if !strings.Contains(err.Error(), "opencode.jsonc") || !strings.Contains(err.Error(), "opencode.json") {
		t.Fatalf("refusal should name both probed files, got: %v", err)
	}
}
