package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-3 (T040): a scoped caller reads the approved copy of available
// skills and learns nothing about any other skill.

const heldMarker = "zzheldmarker"

func sf(rel string, size int64, digest string, text bool) storage.SkillFile {
	return storage.SkillFile{RelPath: rel, Size: size, Digest: digest, Text: text, Stored: text}
}

// scopedSkillsFixture: "ok" is approved (its current copy has since moved on),
// and four held skills carry the marker in every field.
func scopedSkillsFixture(c *skillsTestController) {
	ok := skillRec("ok", storage.SkillStatusApproved, "sk1-current-ok", sf("SKILL.md", 99, "sha256:cur", true))
	ok.Description = "approved description"
	ok.Approved = &storage.SkillContent{Files: []storage.SkillFile{sf("SKILL.md", 13, "sha256:app", true), sf("ref/a.md", 5, "sha256:a", true), sf("img.png", 9, "sha256:i", false)}}
	ok.ApprovedHash = "sk1-approved-ok"

	pending := skillRec(heldMarker+"-pending", storage.SkillStatusPending, "sk1-"+heldMarker+"-p", sf(heldMarker+".md", 1, "sha256:"+heldMarker, true))
	changed := skillRec(heldMarker+"-changed", storage.SkillStatusChanged, "sk1-"+heldMarker+"-new", sf("SKILL.md", 1, "sha256:x", true))
	changed.Approved = &storage.SkillContent{Files: []storage.SkillFile{sf("SKILL.md", 1, "sha256:y", true)}}
	changed.ApprovedHash = "sk1-" + heldMarker + "-old"
	blocked := skillRec(heldMarker+"-blocked", storage.SkillStatusApproved, "sk1-"+heldMarker+"-b", sf("SKILL.md", 1, "sha256:z", true))
	blocked.Blocked = true
	blocked.Approved = &blocked.Current
	blocked.ApprovedHash = blocked.CurrentHash
	unusable := skillRec(heldMarker+"-unusable", storage.SkillStatusUnusable, "sk1-"+heldMarker+"-u", sf("SKILL.md", 1, "sha256:w", true))
	unusable.UnusableReason = "digest_mismatch " + heldMarker
	for _, r := range []*storage.SkillApprovalRecord{pending, changed, blocked, unusable} {
		r.Description = "held " + heldMarker
		r.Scan = &storage.SkillScan{Verdict: "dangerous", Hash: r.CurrentHash,
			Findings: []storage.SkillScanFinding{{File: "SKILL.md", Rule: "r", Severity: "high", Evidence: heldMarker}}}
	}
	c.recs["alpha"] = []*storage.SkillApprovalRecord{ok, pending, changed, blocked, unusable}
	c.status["alpha"] = internalRuntime.SkillsServerStatus{Supported: true, Error: "refresh failed " + heldMarker, Truncated: true}
	c.files["ok|SKILL.md|approved"] = []byte("approved text")
	c.files["ok|SKILL.md|current"] = []byte("NEW UPSTREAM TEXT")
	c.files["ok|ref/a.md|approved"] = []byte("alpha")
	c.files[heldMarker+"-pending|SKILL.md|current"] = []byte(heldMarker)
}

func TestSkillsList_ScopedCallerSeesOnlyAvailableApprovedCopies(t *testing.T) {
	ctrl, srv, token := scopedSkillsServer(t)
	rec := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.NotContains(t, body, heldMarker, "a held skill, its hash, file, scan or error shows")
	require.NotContains(t, body, "sk1-current-ok", "the current hash of an available skill is not shown")
	require.NotContains(t, body, "NEW UPSTREAM")

	d := scopeDecodeData(t, rec)
	require.Equal(t, true, d["enabled"])
	require.Equal(t, "alpha", d["server"])
	for _, k := range []string{"supported", "expose_skills", "last_refresh", "error", "truncated", "incarnation", "total", "count"} {
		require.NotContains(t, d, k, "a scoped list carries no %s", k)
	}
	list := d["skills"].([]interface{})
	require.Len(t, list, 1)
	e := list[0].(map[string]interface{})
	require.Equal(t, "alpha:ok", e["id"])
	require.Equal(t, "approved", e["status"])
	require.Equal(t, true, e["available"])
	require.Equal(t, "sk1-approved-ok", e["approved_hash"])
	require.EqualValues(t, 27, e["total_size"])
	require.Len(t, e["files"], 3)
	for _, k := range []string{"current_hash", "scan", "blocked", "unusable_reason", "approved_files", "approved_by", "listed"} {
		require.NotContains(t, e, k)
	}
	for _, call := range ctrl.recorded() {
		require.NotContains(t, []string{"ListSkillRecords", "SkillFileText", "SkillsStatus"}, call)
	}

	// The administrator still sees every record.
	adm := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	require.Contains(t, adm.Body.String(), heldMarker)
}

func TestSkillsList_ScopedCallerFeatureOffAndEmpty(t *testing.T) {
	_, srv, token := skillsServer(t, false, scopedSkillsFixture)
	rec := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", token)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), heldMarker)
	d := scopeDecodeData(t, rec)
	require.Equal(t, false, d["enabled"])
	require.Empty(t, d["skills"])
}

func scopedSkillsServer(t *testing.T) (*skillsTestController, *Server, string) {
	t.Helper()
	return skillsServer(t, true, scopedSkillsFixture)
}

func TestSkillContent_ScopedCallerGetsTheApprovedCopyOnly(t *testing.T) {
	_, srv, token := scopedSkillsServer(t)
	for _, q := range []url.Values{
		{"skill": {"ok"}},
		{"skill": {"ok"}, "copy": {"approved"}},
		{"skill": {"ok"}, "copy": {"current"}}, // forced to approved
	} {
		rec := skillsDo(t, srv, http.MethodGet, contentURL(q), token)
		require.Equal(t, http.StatusOK, rec.Code, "%v: %s", q, rec.Body.String())
		require.Equal(t, "approved text", scopeDecodeData(t, rec)["text"], "%v", q)
		require.NotContains(t, rec.Body.String(), "NEW UPSTREAM")
	}
	rec := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}, "file": {"ref/a.md"}}), token)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "alpha", scopeDecodeData(t, rec)["text"])

	// A binary file of an available skill is 415 as for the administrator.
	rec = skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}, "file": {"img.png"}}), token)
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)

	// Bad parameters are still 400.
	rec = skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}, "copy": {"bogus"}}), token)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSkillContent_ScopedCallerHeldAndUnknownAnswerAlike(t *testing.T) {
	ctrl, srv, token := scopedSkillsServer(t)
	unknown := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"nope"}}), token)
	require.Equal(t, http.StatusNotFound, unknown.Code)
	want := scopeDecodeError(t, unknown)
	require.NotContains(t, want, "nope", "the body does not echo the name asked for")

	for _, q := range []url.Values{
		{"skill": {heldMarker + "-pending"}},
		{"skill": {heldMarker + "-pending"}, "copy": {"current"}},
		{"skill": {heldMarker + "-changed"}, "copy": {"approved"}},
		{"skill": {heldMarker + "-blocked"}},
		{"skill": {heldMarker + "-unusable"}},
		{"skill": {"ok"}, "file": {"nope.md"}},
		{"skill": {"ok"}, "file": {"../other/SKILL.md"}},
		{"skill": {"ok"}, "file": {"/etc/passwd"}},
	} {
		rec := skillsDo(t, srv, http.MethodGet, contentURL(q), token)
		require.Equal(t, http.StatusNotFound, rec.Code, "%v", q)
		require.Equal(t, want, scopeDecodeError(t, rec), "%v", q)
	}
	for _, call := range ctrl.recorded() {
		require.NotContains(t, []string{"ListSkillRecords", "SkillFileText"}, call)
	}
}

func TestSkills_ScopedCallerOutsideReachIsNotFoundLikeAMissingServer(t *testing.T) {
	_, srv, token := scopedSkillsServer(t)
	// beta exists in the configuration but is outside the token's reach.
	for _, path := range []string{"/skills", "/skills/content?skill=ok"} {
		hidden := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/beta"+path, token)
		missing := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/ghost"+path, token)
		require.Equal(t, http.StatusNotFound, hidden.Code, path)
		require.Equal(t, strings.ReplaceAll(scopeDecodeError(t, missing), "ghost", "beta"), scopeDecodeError(t, hidden), path)
	}
}

// --- pinned profile ---

func (c *pinBoundaryController) ListAvailableSkills(server string) ([]*internalRuntime.AvailableSkill, error) {
	return []*internalRuntime.AvailableSkill{{ID: server + ":s", Server: server, Path: "s", Name: "s", ApprovedHash: "sk1-a"}}, nil
}

func (c *pinBoundaryController) AvailableSkillFile(server, _, _ string) ([]byte, storage.SkillFile, *internalRuntime.AvailableSkill, error) {
	return []byte("text of " + server), storage.SkillFile{RelPath: "SKILL.md", Text: true}, nil, nil
}

func TestSkills_PinnedTokenOutsideThePinGets404(t *testing.T) {
	f := pinBoundaryServer(t, true)
	f.cfg.EnableSkills = true
	for _, path := range []string{"/skills", "/skills/content?skill=s"} {
		in := f.get(t, "/api/v1/servers/github"+path, f.pinned)
		require.Equal(t, http.StatusOK, in.Code, "%s: %s", path, in.Body.String())

		for _, hiddenName := range []string{"notion", "filesystem"} {
			hidden := f.get(t, "/api/v1/servers/"+hiddenName+path, f.pinned)
			missing := f.get(t, "/api/v1/servers/ghost"+path, f.pinned)
			require.Equal(t, http.StatusNotFound, hidden.Code, hiddenName+path)
			require.Equal(t, strings.ReplaceAll(scopeDecodeError(t, missing), "ghost", hiddenName), scopeDecodeError(t, hidden), hiddenName+path)
		}
		// A deleted pin hides everything.
		require.Equal(t, http.StatusNotFound, f.get(t, "/api/v1/servers/github"+path, f.deleted).Code)
	}
}

// FR-024: an agent token that reads skills over REST is recorded like one that
// uses the agent tools. An administrator reading its own store is not.
func TestSkills_ScopedRESTReadsAreRecordedAsSkillAccess(t *testing.T) {
	ctrl, srv, token := scopedSkillsServer(t)

	require.Equal(t, http.StatusOK, skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", token).Code)
	require.Equal(t, http.StatusOK, skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}}), token).Code)
	require.Equal(t, http.StatusNotFound, skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {heldMarker + "-pending"}}), token).Code)
	require.Equal(t, http.StatusUnsupportedMediaType, skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}, "file": {"img.png"}}), token).Code)

	rows := ctrl.accessRows()
	require.Len(t, rows, 4)
	for _, a := range rows {
		require.Equal(t, "api", a.Source)
		require.Equal(t, "alpha", a.Server)
		require.NotEmpty(t, a.Attribution.TokenName, "the row names the token that read")
	}
	require.Equal(t, "approved text", rows[1].DetectionText, "the delivered text is scanned for sensitive data like a tool answer")
	require.Empty(t, rows[2].DetectionText)
	require.Equal(t, internalRuntime.SkillActionList, rows[0].Action)
	require.Equal(t, 1, rows[0].Results, "only the available skill is counted")

	require.Equal(t, internalRuntime.SkillActionRead, rows[1].Action)
	require.Equal(t, internalRuntime.SkillAccessSuccess, rows[1].Outcome)
	require.Equal(t, "alpha:ok", rows[1].SkillID)
	require.Equal(t, "SKILL.md", rows[1].File)
	require.Equal(t, "sk1-approved-ok", rows[1].Hash)
	require.Equal(t, int64(len("approved text")), rows[1].Bytes)

	require.Equal(t, internalRuntime.SkillAccessNotFound, rows[2].Outcome)
	require.Empty(t, rows[2].Hash)
	require.Equal(t, internalRuntime.SkillAccessBinary, rows[3].Outcome)

	// An administrator's reads write no access row.
	require.Equal(t, http.StatusOK, skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey).Code)
	require.Equal(t, http.StatusOK, skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}}), scopeAdminAPIKey).Code)
	require.Len(t, ctrl.accessRows(), 4)
}

// A scoped read with unusable parameters reads nothing and is still recorded.
func TestSkills_ScopedRESTInvalidReadsAreRecorded(t *testing.T) {
	ctrl, srv, token := scopedSkillsServer(t)
	for _, q := range []url.Values{
		{},                                   // no skill
		{"skill": {"ok"}, "copy": {"bogus"}}, // bad copy
		{"skill": {"ok"}, "offset": {"-1"}},
		{"skill": {"ok"}, "limit": {"0"}},
		{"skill": {"ok"}, "offset": {"99999"}}, // past the end, known only after the read
	} {
		rec := skillsDo(t, srv, http.MethodGet, contentURL(q), token)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%v", q)
	}
	rows := ctrl.accessRows()
	require.Len(t, rows, 5)
	for _, a := range rows {
		require.Equal(t, internalRuntime.SkillAccessInvalid, a.Outcome)
		require.Empty(t, a.DetectionText)
	}

	// An administrator's bad request writes no row.
	require.Equal(t, http.StatusBadRequest, skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}, "copy": {"bogus"}}), scopeAdminAPIKey).Code)
	require.Len(t, ctrl.accessRows(), 5)
}

// What the output policy withholds is refused, and a damaged stored copy is
// the same not-found answer as an unknown skill: a scoped caller cannot tell
// that the skill was there a moment ago.
func TestSkills_ScopedRESTBlockedAndDamaged(t *testing.T) {
	ctrl, srv, token := scopedSkillsServer(t)
	unknown := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"nope"}}), token)
	require.Equal(t, http.StatusNotFound, unknown.Code)

	ctrl.availErr = internalRuntime.ErrSkillStoredCopyDamaged
	rec := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}}), token)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, scopeDecodeError(t, unknown), scopeDecodeError(t, rec))

	ctrl.availErr = internalRuntime.ErrSkillOutputBlocked
	rec = skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}}), token)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "output_blocked")
	require.NotContains(t, rec.Body.String(), "approved text")

	rows := ctrl.accessRows()
	require.Len(t, rows, 3)
	require.Equal(t, internalRuntime.SkillAccessNotFound, rows[0].Outcome)
	require.Equal(t, internalRuntime.SkillAccessDamaged, rows[1].Outcome)
	require.Equal(t, internalRuntime.SkillAccessBlocked, rows[2].Outcome)
}

// A scoped read that ends before a skill is looked up is recorded too.
func TestSkills_ScopedRESTReadsWithTheFeatureOffAreRecorded(t *testing.T) {
	ctrl, srv, token := skillsServer(t, false, scopedSkillsFixture)

	require.Equal(t, http.StatusOK, skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", token).Code)
	require.Equal(t, http.StatusNotFound, skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}}), token).Code)

	rows := ctrl.accessRows()
	require.Len(t, rows, 2)
	require.Equal(t, internalRuntime.SkillActionList, rows[0].Action)
	require.Equal(t, internalRuntime.SkillAccessSuccess, rows[0].Outcome)
	require.Zero(t, rows[0].Results)
	require.Equal(t, internalRuntime.SkillActionRead, rows[1].Action)
	require.Equal(t, internalRuntime.SkillAccessNotFound, rows[1].Outcome)
	require.Equal(t, "alpha:ok", rows[1].SkillID)

	// An administrator's reads write none.
	skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"ok"}}), scopeAdminAPIKey)
	require.Len(t, ctrl.accessRows(), 2)
}
