package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 125 PR-2: `skills approve|block|unblock` and `skills show --diff`.

func skillFile(path string, size int64, digest string, text bool) contracts.SkillFileInfo {
	return contracts.SkillFileInfo{Path: path, Size: size, Digest: digest, Text: text}
}

func skillsApproveList(t *testing.T, incarnation int64, skills ...contracts.SkillEntry) string {
	t.Helper()
	return mustJSON(t, contracts.SkillsListResponse{
		Enabled: true, Server: "alpha", Supported: true, Incarnation: incarnation, Skills: skills,
	})
}

func pendingSkill(path, hash string) contracts.SkillEntry {
	scanned := time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)
	return contracts.SkillEntry{
		ID: "alpha:" + path, Path: path, Name: path, Status: "pending", Listed: true, CurrentHash: hash,
		Files: []contracts.SkillFileInfo{skillFile("SKILL.md", 10, "sha256:aa", true)},
		Scan: contracts.SkillScanInfo{Verdict: "clean", Complete: true, ScannedAt: &scanned,
			Findings: []contracts.SkillScanFinding{}},
	}
}

func TestSkillsApprove_SendsExactlyTheHashesAndIncarnationItPrinted(t *testing.T) {
	hf := pendingSkill("hf-cli", "sk1-hf")
	jobs := pendingSkill("jobs", "sk1-jobs")
	jobs.Scan = contracts.SkillScanInfo{Verdict: "warnings", Complete: false, Incomplete: "size_cap",
		Findings: []contracts.SkillScanFinding{{File: "SKILL.md", Rule: "r"}}, ScannedAt: hf.Scan.ScannedAt}
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":          okResp(skillsApproveList(t, 7, hf, jobs)),
		"POST /api/v1/servers/alpha/skills/approve": okResp(`{"approved":2}`),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:hf-cli", "alpha:jobs", "--yes")
	require.NoError(t, err)

	// What was printed names the hashes.
	for _, want := range []string{"alpha:hf-cli", "sk1-hf", "clean, 0 finding(s)", "alpha:jobs", "sk1-jobs", "scan incomplete (size_cap), warnings, 1 finding(s)", "Approved 2 skill(s)"} {
		require.Contains(t, out, want)
	}
	// What was sent is what was printed.
	var post recordedRequest
	for _, r := range rec.all() {
		if r.Method == http.MethodPost {
			post = r
		}
	}
	body := post.jsonBody(t)
	require.EqualValues(t, 7, body["expected_incarnation"])
	require.Equal(t, []any{"hf-cli", "jobs"}, body["skills"])
	require.Equal(t, map[string]any{"hf-cli": "sk1-hf", "jobs": "sk1-jobs"}, body["expected_hashes"])
}

func TestSkillsApprove_AllTakesPendingAndChangedUsableListedSkills(t *testing.T) {
	pend := pendingSkill("p", "sk1-p")
	chg := pendingSkill("c", "sk1-c")
	chg.Status = "changed"
	appr := pendingSkill("a", "sk1-a")
	appr.Status = "approved"
	bad := pendingSkill("bad", "")
	bad.Status = "unusable"
	unlisted := pendingSkill("gone", "sk1-g")
	unlisted.Listed = false
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":          okResp(skillsApproveList(t, 1, pend, chg, appr, bad, unlisted)),
		"POST /api/v1/servers/alpha/skills/approve": okResp(`{"approved":2}`),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "--server", "alpha", "--all", "--yes")
	require.NoError(t, err)
	var post recordedRequest
	for _, r := range rec.all() {
		if r.Method == http.MethodPost {
			post = r
		}
	}
	require.Equal(t, []any{"p", "c"}, post.jsonBody(t)["skills"])
}

func TestSkillsApprove_AllWithNothingPendingSendsNoApprove(t *testing.T) {
	appr := pendingSkill("a", "sk1-a")
	appr.Status = "approved"
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, appr)),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "--server", "alpha", "--all", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "Nothing to approve")
	require.Len(t, rec.all(), 1)
}

func TestSkillsApprove_ExpectHashMismatchSendsNoApprove(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-new"))),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:hf-cli", "--expect-hash", "sk1-old", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "sk1-new")
	require.Contains(t, err.Error(), "nothing was approved")
	for _, r := range rec.all() {
		require.NotEqual(t, http.MethodPost, r.Method, "no approve request may be sent")
	}

	// A matching hash goes through.
	rec = newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":          okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-new"))),
		"POST /api/v1/servers/alpha/skills/approve": okResp(`{"approved":1}`),
	})
	_, _, err = runCLI(t, GetSkillsCommand, "table", "approve", "alpha:hf-cli", "--expect-hash", "sk1-new", "--yes")
	require.NoError(t, err)
	require.Len(t, rec.all(), 2)
}

func TestSkillsApprove_ExpectHashNeedsExactlyOneSkill(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:a", "alpha:b", "--expect-hash", "sk1-x", "--yes")
	require.Error(t, err)
	_, _, err = runCLI(t, GetSkillsCommand, "table", "approve", "--server", "alpha", "--all", "--expect-hash", "sk1-x", "--yes")
	require.Error(t, err)
	require.Empty(t, rec.all())
}

func TestSkillsApprove_MixedServersRefused(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:a", "beta:b", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "one server")
	require.Empty(t, rec.all(), "no request before the usage error")
}

func TestSkillsApprove_UsageErrors(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	for _, args := range [][]string{
		{"approve", "--yes"},
		{"approve", "--all", "--yes"},
		{"approve", "--server", "alpha", "--all", "alpha:x", "--yes"},
		{"approve", "nocolon", "--yes"},
		{"approve", "--server", "beta", "alpha:x", "--yes"},
	} {
		_, _, err := runCLI(t, GetSkillsCommand, "table", args...)
		require.Error(t, err, args)
	}
	require.Empty(t, rec.all())
}

// Tests run without a terminal on stdin: the command must refuse and say how
// to proceed, before it sends anything.
func TestSkillsApprove_NoTerminalWithoutYesIsRefused(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-hf"))),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:hf-cli")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.Contains(t, out, "sk1-hf", "the review is printed before the refusal")
	for _, r := range rec.all() {
		require.NotEqual(t, http.MethodPost, r.Method)
	}
}

func TestSkillsApprove_ReviewOutOfDatePrintsWhatChanged(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-hf"))),
		"POST /api/v1/servers/alpha/skills/approve": refuse(http.StatusConflict,
			`{"success":false,"error":"review_out_of_date: stale","error_code":"review_out_of_date","changed":["hf-cli"],"unreviewed":[]}`),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:hf-cli", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing was approved")
	require.Contains(t, err.Error(), "Changed: hf-cli")
}

func TestSkillsApprove_ServerReplacedKeepsTheDaemonMessage(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-hf"))),
		"POST /api/v1/servers/alpha/skills/approve": refuse(http.StatusConflict,
			`{"success":false,"error":"server_replaced: the server was removed or replaced since it was reviewed"}`),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:hf-cli", "--yes")
	require.ErrorContains(t, err, "server_replaced")
}

func TestSkillsApprove_JSONPrintsTheResponseOnly(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":          okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-hf"))),
		"POST /api/v1/servers/alpha/skills/approve": okResp(`{"approved":1}`),
	})
	out, errOut, err := runCLI(t, GetSkillsCommand, "json", "approve", "alpha:hf-cli", "--yes")
	require.NoError(t, err)
	require.JSONEq(t, `{"approved":1}`, out)
	require.Contains(t, errOut, "sk1-hf", "the review goes to stderr in structured mode")
}

func TestSkillsBlockUnblock(t *testing.T) {
	for _, tc := range []struct{ cmd, route, want string }{
		{"block", "block", "1 skill(s) blocked on alpha"},
		{"unblock", "unblock", "1 skill(s) unblocked on alpha"},
	} {
		rec := newRESTRecorder(t, map[string]cannedResponse{
			"POST /api/v1/servers/alpha/skills/" + tc.route: okResp(`{"changed":1}`),
		})
		out, _, err := runCLI(t, GetSkillsCommand, "table", tc.cmd, "alpha:hf-cli", "alpha:hf-cli")
		require.NoError(t, err)
		require.Contains(t, out, tc.want)
		require.Equal(t, []any{"hf-cli"}, rec.only(t).jsonBody(t)["skills"])

		rec = newRESTRecorder(t, map[string]cannedResponse{
			"POST /api/v1/servers/alpha/skills/" + tc.route: okResp(`{"changed":1}`),
		})
		out, _, err = runCLI(t, GetSkillsCommand, "json", tc.cmd, "alpha:hf-cli")
		require.NoError(t, err)
		require.JSONEq(t, `{"changed":1}`, out)
	}
	newRESTRecorder(t, map[string]cannedResponse{})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "block", "alpha:a", "beta:b")
	require.Error(t, err)
}

func TestSkillsListAndShow_PrintApprovalAndScanFields(t *testing.T) {
	approvedAt := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	scanned := time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)
	e := pendingSkill("hf-cli", "sk1-hf")
	e.Status, e.ApprovedHash, e.ApprovedAt, e.ApprovedBy, e.Blocked = "changed", "sk1-old", &approvedAt, "api", true
	e.Scan = contracts.SkillScanInfo{Verdict: "warnings", Complete: false, Incomplete: "coverage", ScannedAt: &scanned,
		FindingsTruncated: true, Findings: []contracts.SkillScanFinding{{File: "refs/a.md", Offset: 42, Rule: "exfil", Severity: "high", Evidence: "curl x"}}}
	routes := map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":         okResp(skillsApproveList(t, 1, e)),
		"GET /api/v1/servers/alpha/skills/content": okResp(`{"text":"hi\n","offset":0,"size":3,"digest":"sha256:aa"}`),
	}
	newRESTRecorder(t, routes)
	out, _, err := runCLI(t, GetSkillsCommand, "table", "list", "--server", "alpha")
	require.NoError(t, err)
	for _, want := range []string{"SCAN", "changed, blocked", "scan incomplete (coverage), warnings, 1+ finding(s)"} {
		require.Contains(t, out, want)
	}
	newRESTRecorder(t, routes)
	out, _, err = runCLI(t, GetSkillsCommand, "table", "show", "alpha:hf-cli")
	require.NoError(t, err)
	for _, want := range []string{"Approved by:  api at 2026-11-16T10:00:00Z", "Scan:         scan incomplete (coverage), warnings", "Scanned at:   2026-11-17T07:00:00Z",
		"high exfil in refs/a.md at byte 42: curl x", "more findings exist"} {
		require.Contains(t, out, want)
	}
}

func TestSkillsApprove_EscapesHostileNamesAndFindings(t *testing.T) {
	e := pendingSkill("evil", "sk1-x")
	e.ID = "alpha:evil\x1b[2J\u202e"
	e.Scan.Verdict = "dangerous\x1b]0;t\x07"
	routes := map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":          okResp(skillsApproveList(t, 1, e)),
		"POST /api/v1/servers/alpha/skills/approve": okResp(`{"approved":1}`),
	}
	newRESTRecorder(t, routes)
	out, _, err := runCLI(t, GetSkillsCommand, "table", "approve", "alpha:evil", "--yes")
	require.NoError(t, err)
	for _, c := range []string{"\x1b", "\x07", "\u202e"} {
		require.NotContains(t, out, c, "raw control character %q reached the terminal", c)
	}
	require.Contains(t, out, `\x1b[2J`)
	require.Contains(t, out, `\u202e`)
}

// --- show --diff ---

func diffFixture(t *testing.T) (map[string]cannedResponse, string) {
	t.Helper()
	e := pendingSkill("hf-cli", "sk1-new")
	e.Status, e.ApprovedHash = "changed", "sk1-old"
	e.ApprovedFiles = []contracts.SkillFileInfo{
		skillFile("SKILL.md", 20, "sha256:old", true),
		skillFile("gone.md", 5, "sha256:gone", true),
		skillFile("same.md", 5, "sha256:same", true),
		skillFile("logo.png", 9, "sha256:png1", false),
	}
	e.Files = []contracts.SkillFileInfo{
		skillFile("SKILL.md", 22, "sha256:new", true),
		skillFile("added.md", 4, "sha256:add", true),
		skillFile("same.md", 5, "sha256:same", true),
		skillFile("logo.png", 10, "sha256:png2", false),
	}
	return map[string]cannedResponse{"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, e))}, "sk1-old"
}

func TestSkillsShowDiff_FilesAndUnifiedDiffs(t *testing.T) {
	routes, _ := diffFixture(t)
	rec := newRESTRecorder(t, routes)
	// The command reads, in file order (added.md, gone.md, SKILL.md; sorted by
	// path), the approved copy of a removed file, the current copy of an added
	// one and both copies of a changed one, approved first.
	rec.mu.Lock()
	rec.sequences = map[string][]cannedResponse{
		"GET /api/v1/servers/alpha/skills/content": {
			okResp(`{"text":"# Title\nold line\nend\n","offset":0,"size":25,"digest":"sha256:old"}`), // SKILL.md approved
			okResp(`{"text":"# Title\nnew line\nend\n","offset":0,"size":25,"digest":"sha256:new"}`), // SKILL.md current
			okResp(`{"text":"new file\n","offset":0,"size":9,"digest":"sha256:add"}`),                // added.md current
			okResp(`{"text":"bye\n","offset":0,"size":4,"digest":"sha256:gone"}`),                    // gone.md approved
		},
	}
	rec.mu.Unlock()
	// Sorted by path: SKILL.md < added.md < gone.md < logo.png (ASCII).
	out, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:hf-cli", "--diff")
	require.NoError(t, err)
	for _, want := range []string{
		"changed  SKILL.md", "added    added.md", "removed  gone.md", "changed  logo.png (binary, not compared)",
		"--- approved/SKILL.md\n+++ current/SKILL.md", "-old line\n+new line",
		"--- /dev/null\n+++ current/added.md", "+new file",
		"--- approved/gone.md\n+++ /dev/null", "-bye",
	} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "same.md")

	var queries []url.Values
	for _, r := range rec.all() {
		if strings.HasSuffix(r.Path, "/content") {
			q, _ := url.ParseQuery(r.RawQuery)
			queries = append(queries, q)
		}
	}
	require.Len(t, queries, 4)
	require.Equal(t, "approved", queries[0].Get("copy"))
	require.Equal(t, "", queries[1].Get("copy"))
	for _, q := range queries {
		require.NotEqual(t, "logo.png", q.Get("file"), "binary files are never fetched")
	}
}

func TestSkillsShowDiff_NoApprovedCopy(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, pendingSkill("hf-cli", "sk1-hf"))),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:hf-cli", "--diff")
	require.NoError(t, err)
	require.Contains(t, out, "no approved version")
	require.Len(t, rec.all(), 1, "nothing else is fetched")
}

func TestSkillsShowDiff_CapsEachFileAtOneMiB(t *testing.T) {
	e := pendingSkill("hf-cli", "sk1-new")
	e.Status, e.ApprovedHash = "changed", "sk1-old"
	e.ApprovedFiles = []contracts.SkillFileInfo{skillFile("SKILL.md", 1, "sha256:old", true)}
	e.Files = []contracts.SkillFileInfo{skillFile("SKILL.md", 3<<20, "sha256:new", true)}
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, e)),
	})
	chunk := strings.Repeat("0123456789abcdef\n", 4096) // 68 KiB per chunk
	big := func(off, next int) cannedResponse {
		body := mustJSON(t, map[string]any{"text": chunk, "offset": off, "next_offset": next, "size": 3 << 20, "digest": "sha256:new"})
		return okResp(body)
	}
	seq := []cannedResponse{okResp(`{"text":"x\n","offset":0,"size":2,"digest":"sha256:old"}`)}
	for i := 0; i < 40; i++ {
		seq = append(seq, big(i*len(chunk), (i+1)*len(chunk)))
	}
	rec.mu.Lock()
	rec.sequences = map[string][]cannedResponse{"GET /api/v1/servers/alpha/skills/content": seq}
	rec.mu.Unlock()
	out, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:hf-cli", "--diff")
	require.NoError(t, err)
	require.Contains(t, out, "was cut at 1 MiB")
	require.Less(t, len(out), 2<<20)
	n := 0
	for _, r := range rec.all() {
		if strings.HasSuffix(r.Path, "/content") {
			n++
		}
	}
	require.Less(t, n, 30, "reading stops once the cap is reached")
}

func TestSkillsShowDiff_JSON(t *testing.T) {
	routes, _ := diffFixture(t)
	rec := newRESTRecorder(t, routes)
	rec.mu.Lock()
	rec.sequences = map[string][]cannedResponse{
		"GET /api/v1/servers/alpha/skills/content": {
			okResp(`{"text":"a\n","offset":0,"size":2,"digest":"sha256:x"}`),
			okResp(`{"text":"b\n","offset":0,"size":2,"digest":"sha256:y"}`),
			okResp(`{"text":"n\n","offset":0,"size":2,"digest":"sha256:z"}`),
			okResp(`{"text":"g\n","offset":0,"size":2,"digest":"sha256:w"}`),
		},
	}
	rec.mu.Unlock()
	out, _, err := runCLI(t, GetSkillsCommand, "json", "show", "alpha:hf-cli", "--diff")
	require.NoError(t, err)
	require.Contains(t, out, `"approved_hash": "sk1-old"`)
	require.Contains(t, out, `"change": "added"`)
	require.Contains(t, out, "--- approved/SKILL.md")
}

func TestSkillsShowDiff_EscapesDiffLines(t *testing.T) {
	e := pendingSkill("evil", "sk1-new")
	e.Status, e.ApprovedHash = "changed", "sk1-old"
	e.ApprovedFiles = []contracts.SkillFileInfo{skillFile("a\rb.md", 1, "sha256:old", true)}
	e.Files = []contracts.SkillFileInfo{skillFile("a\rb.md", 2, "sha256:new", true)}
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsApproveList(t, 1, e)),
	})
	rec.mu.Lock()
	rec.sequences = map[string][]cannedResponse{
		"GET /api/v1/servers/alpha/skills/content": {
			okResp(mustJSON(t, map[string]any{"text": "ok\n", "offset": 0, "size": 3, "digest": "sha256:1"})),
			okResp(mustJSON(t, map[string]any{"text": "ok\n\x1b[2Jevil \u202egnp.exe\n", "offset": 0, "size": 20, "digest": "sha256:2"})),
		},
	}
	rec.mu.Unlock()
	out, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:evil", "--diff")
	require.NoError(t, err)
	for _, c := range []string{"\x1b", "\r", "\u202e"} {
		require.NotContains(t, out, c, "raw control character %q reached the terminal", c)
	}
	require.Contains(t, out, `+\x1b[2Jevil \u202egnp.exe`)
	require.Contains(t, out, `a\x0db.md`)
}

func TestSkillsShowDiff_RejectsFileAndApprovedFlags(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:x", "--diff", "--file", "a.md")
	require.Error(t, err)
	_, _, err = runCLI(t, GetSkillsCommand, "table", "show", "alpha:x", "--diff", "--approved")
	require.Error(t, err)
	require.Empty(t, rec.all())
}
