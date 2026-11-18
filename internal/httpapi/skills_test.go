package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-1 (T020-T022): the three administrator-only skills endpoints.

type skillsTestController struct {
	scopeController

	mu     sync.Mutex
	calls  []string
	recs   map[string][]*storage.SkillApprovalRecord
	status map[string]internalRuntime.SkillsServerStatus
	// afterRefresh, when set, replaces the status once RefreshServerSkills runs.
	afterRefresh *internalRuntime.SkillsServerStatus
	refreshErr   error
	files        map[string][]byte // "<skill>|<rel>|<current|approved>"
	accesses     []internalRuntime.SkillAccessActivity
	// availErr, when set, is what AvailableSkillFile answers for a known file.
	availErr error
	fileErr  error

	// approveFn / blockFn stand in for the runtime approval writers (PR-2).
	approveFn func(ctx context.Context, server string, skills []string, expected map[string]string, by string, inc int64) (int, error)
	blockFn   func(ctx context.Context, server string, skills []string, blocked bool, by string, inc int64) (int, error)
	// incarnations answers ServerIncarnation (the optional reader capability).
	incarnations map[string]int64
	// onList runs on every read of the records.
	onList func()
}

func (c *skillsTestController) ServerIncarnation(name string) (int64, bool) {
	inc, ok := c.incarnations[name]
	return inc, ok
}

func (c *skillsTestController) ApproveSkillsReviewedAt(ctx context.Context, server string, skills []string, expected map[string]string, by string, inc int64) (int, error) {
	c.record("ApproveSkillsReviewedAt")
	if c.approveFn == nil {
		return 0, nil
	}
	return c.approveFn(ctx, server, skills, expected, by, inc)
}

func (c *skillsTestController) SetSkillsBlockedAt(ctx context.Context, server string, skills []string, blocked bool, by string, inc int64) (int, error) {
	c.record("SetSkillsBlockedAt")
	if c.blockFn == nil {
		return 0, nil
	}
	return c.blockFn(ctx, server, skills, blocked, by, inc)
}

func (c *skillsTestController) record(call string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *skillsTestController) EmitActivitySkillAccess(a internalRuntime.SkillAccessActivity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accesses = append(c.accesses, a)
}

func (c *skillsTestController) accessRows() []internalRuntime.SkillAccessActivity {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]internalRuntime.SkillAccessActivity(nil), c.accesses...)
}

func (c *skillsTestController) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func (c *skillsTestController) SkillsStatus(server string) internalRuntime.SkillsServerStatus {
	c.record("SkillsStatus")
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status[server]
}

func (c *skillsTestController) ListSkillRecords(server string) ([]*storage.SkillApprovalRecord, error) {
	c.record("ListSkillRecords")
	if c.onList != nil {
		c.onList()
	}
	return c.recs[server], nil
}

func (c *skillsTestController) SkillAvailable(_ string, rec *storage.SkillApprovalRecord) bool {
	c.record("SkillAvailable")
	return rec.Status == storage.SkillStatusApproved && !rec.Blocked && rec.Listed
}

// ListAvailableSkills answers a scoped caller from the approved copies only.
func (c *skillsTestController) ListAvailableSkills(server string) ([]*internalRuntime.AvailableSkill, error) {
	c.record("ListAvailableSkills")
	var out []*internalRuntime.AvailableSkill
	for _, rec := range c.recs[server] {
		if rec.Approved == nil || rec.Status != storage.SkillStatusApproved || rec.Blocked || !rec.Listed {
			continue
		}
		a := &internalRuntime.AvailableSkill{
			ID: server + ":" + rec.SkillPath, Server: server, Path: rec.SkillPath, URI: rec.SkillURI,
			Name: rec.Name, Description: rec.Description, ApprovedHash: rec.ApprovedHash,
			Files: append([]storage.SkillFile(nil), rec.Approved.Files...),
		}
		for _, f := range a.Files {
			a.TotalSize += f.Size
		}
		out = append(out, a)
	}
	return out, nil
}

// AvailableSkillFile serves the approved copy of an available skill.
func (c *skillsTestController) AvailableSkillFile(server, skill, rel string) ([]byte, storage.SkillFile, *internalRuntime.AvailableSkill, error) {
	c.record("AvailableSkillFile")
	avail, _ := c.ListAvailableSkills(server)
	c.mu.Lock()
	c.calls = c.calls[:len(c.calls)-1] // the nested list is not a call of its own
	c.mu.Unlock()
	for _, a := range avail {
		if a.Path != skill {
			continue
		}
		for _, f := range a.Files {
			if f.RelPath == rel {
				if !f.Text {
					return nil, f, a, internalRuntime.ErrSkillFileBinary
				}
				if c.availErr != nil {
					return nil, f, a, c.availErr
				}
				return c.files[skill+"|"+rel+"|approved"], f, a, nil
			}
		}
	}
	return nil, storage.SkillFile{}, nil, internalRuntime.ErrSkillNotFound
}

func (c *skillsTestController) SkillFileText(server, skill, rel string, approved bool) ([]byte, storage.SkillFile, error) {
	c.record("SkillFileText")
	if c.fileErr != nil {
		return nil, storage.SkillFile{}, c.fileErr
	}
	copyName := "current"
	if approved {
		copyName = "approved"
	}
	for _, rec := range c.recs[server] {
		if rec.SkillPath != skill {
			continue
		}
		for _, f := range rec.Current.Files {
			if f.RelPath == rel {
				if !f.Text {
					return nil, f, internalRuntime.ErrSkillFileBinary
				}
				if data, ok := c.files[skill+"|"+rel+"|"+copyName]; ok {
					return data, f, nil
				}
			}
		}
		return nil, storage.SkillFile{}, internalRuntime.ErrSkillFileNotFound
	}
	return nil, storage.SkillFile{}, internalRuntime.ErrSkillNotFound
}

func (c *skillsTestController) RefreshServerSkills(_ context.Context, server string) error {
	c.record("RefreshServerSkills")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.afterRefresh != nil {
		c.status[server] = *c.afterRefresh
	}
	return c.refreshErr
}

func skillRec(path, status string, hash string, files ...storage.SkillFile) *storage.SkillApprovalRecord {
	return &storage.SkillApprovalRecord{
		ServerName: "alpha", SkillURI: "skill://" + path + "/SKILL.md", SkillPath: path,
		Name: path, Description: "desc " + path, Status: status, Listed: true,
		Current: storage.SkillContent{Files: files}, CurrentHash: hash,
	}
}

func skillsServer(t *testing.T, enabled bool, mutate func(*skillsTestController)) (*skillsTestController, *Server, string) {
	t.Helper()
	cfg := scopeFixtureConfig(false)
	cfg.EnableSkills = enabled
	ctrl := &skillsTestController{
		scopeController: scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true},
		recs:            map[string][]*storage.SkillApprovalRecord{},
		status:          map[string]internalRuntime.SkillsServerStatus{},
		files:           map[string][]byte{},
	}
	if mutate != nil {
		mutate(ctrl)
	}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	return ctrl, srv, token
}

func skillsDo(t *testing.T, srv *Server, method, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, http.NoBody)
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestSkillsList_Shape(t *testing.T) {
	refresh := time.Date(2026, 11, 16, 19, 20, 0, 0, time.UTC)
	ctrl, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		c.recs["alpha"] = []*storage.SkillApprovalRecord{
			skillRec("hf-cli", storage.SkillStatusPending, "sk1-aaa",
				storage.SkillFile{RelPath: "SKILL.md", Size: 100, Digest: "sha256:abc", Text: true}),
			skillRec("ok", storage.SkillStatusApproved, "sk1-bbb",
				storage.SkillFile{RelPath: "SKILL.md", Size: 10, Digest: "sha256:def", Text: true},
				storage.SkillFile{RelPath: "img.png", Size: 20, Digest: "sha256:ghi"}),
		}
		c.recs["alpha"][1].ApprovedHash = "sk1-bbb"
		c.status["alpha"] = internalRuntime.SkillsServerStatus{Supported: true, LastRefresh: refresh, Truncated: true}
	})
	rec := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := scopeDecodeData(t, rec)
	require.Equal(t, true, data["enabled"])
	require.Equal(t, "alpha", data["server"])
	require.Equal(t, true, data["supported"])
	require.Contains(t, data, "expose_skills")
	require.Nil(t, data["expose_skills"])
	require.Equal(t, "2026-11-16T19:20:00Z", data["last_refresh"])
	require.Equal(t, "", data["error"])
	require.Equal(t, true, data["truncated"])
	list := data["skills"].([]interface{})
	require.Len(t, list, 2)
	p := list[0].(map[string]interface{})
	require.Equal(t, "alpha:hf-cli", p["id"])
	require.Equal(t, "skill://hf-cli/SKILL.md", p["uri"])
	require.Equal(t, "hf-cli", p["path"])
	require.Equal(t, "pending", p["status"])
	require.Equal(t, false, p["available"], "a pending skill is not available")
	require.Equal(t, true, p["listed"])
	require.Equal(t, false, p["blocked"])
	require.Equal(t, "sk1-aaa", p["current_hash"])
	require.Equal(t, "", p["approved_hash"])
	require.EqualValues(t, 100, p["total_size"])
	files := p["files"].([]interface{})
	f0 := files[0].(map[string]interface{})
	require.Equal(t, "SKILL.md", f0["path"])
	require.EqualValues(t, 100, f0["size"])
	require.Equal(t, "sha256:abc", f0["digest"])
	require.Equal(t, true, f0["text"])
	scan := p["scan"].(map[string]interface{})
	require.Equal(t, "", scan["verdict"])
	require.Equal(t, false, scan["complete"])
	require.Equal(t, []interface{}{}, scan["findings"])
	o := list[1].(map[string]interface{})
	require.Equal(t, true, o["available"])
	require.EqualValues(t, 30, o["total_size"])
	require.NotEmpty(t, ctrl.recorded())
}

func TestSkillsList_FeatureOff(t *testing.T) {
	ctrl, srv, _ := skillsServer(t, false, nil)
	rec := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := scopeDecodeData(t, rec)
	require.Equal(t, false, data["enabled"])
	require.Equal(t, []interface{}{}, data["skills"])
	require.Empty(t, ctrl.recorded(), "no runtime call when the feature is off")
}

func TestSkillsList_UnknownServer404(t *testing.T) {
	_, srv, _ := skillsServer(t, true, nil)
	rec := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/nope/skills", scopeAdminAPIKey)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "Server not found")
}

func contentURL(q url.Values) string {
	return "/api/v1/servers/alpha/skills/content?" + q.Encode()
}

func contentFixture(text string) func(*skillsTestController) {
	return func(c *skillsTestController) {
		c.recs["alpha"] = []*storage.SkillApprovalRecord{
			skillRec("s", storage.SkillStatusPending, "sk1-x",
				storage.SkillFile{RelPath: "SKILL.md", Size: int64(len(text)), Digest: "sha256:dd", Text: true},
				storage.SkillFile{RelPath: "bin.dat", Size: 4, Digest: "sha256:ee"}),
		}
		c.files["s|SKILL.md|current"] = []byte(text)
		c.files["s|SKILL.md|approved"] = []byte("approved text")
	}
}

func TestSkillContent_HappyPathDefaults(t *testing.T) {
	_, srv, _ := skillsServer(t, true, contentFixture("hello"))
	rec := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"s"}}), scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	d := scopeDecodeData(t, rec)
	require.Equal(t, "hello", d["text"])
	require.EqualValues(t, 0, d["offset"])
	require.NotContains(t, d, "next_offset")
	require.EqualValues(t, 5, d["size"])
	require.Equal(t, "sha256:dd", d["digest"])

	rec = skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"s"}, "copy": {"approved"}}), scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "approved text", scopeDecodeData(t, rec)["text"])
}

func TestSkillContent_PagingKeepsRunesWhole(t *testing.T) {
	// 1-, 2-, 3- and 4-byte runes so every limit cuts somewhere different.
	text := "aé€😀bé€😀cc"
	for limit := 1; limit <= len(text)+1; limit++ {
		_, srv, _ := skillsServer(t, true, contentFixture(text))
		var got []byte
		offset := 0
		for i := 0; i < len(text)+2; i++ {
			q := url.Values{"skill": {"s"}, "offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
			rec := skillsDo(t, srv, http.MethodGet, contentURL(q), scopeAdminAPIKey)
			require.Equal(t, http.StatusOK, rec.Code, "limit %d: %s", limit, rec.Body.String())
			d := scopeDecodeData(t, rec)
			part := d["text"].(string)
			require.True(t, utf8.ValidString(part), "limit %d offset %d: invalid UTF-8", limit, offset)
			require.EqualValues(t, offset, d["offset"])
			got = append(got, part...)
			next, more := d["next_offset"]
			if !more {
				break
			}
			require.EqualValues(t, offset+len(part), next)
			require.Greater(t, len(part), 0, "limit %d: no progress", limit)
			offset = int(next.(float64))
		}
		require.Equal(t, text, string(got), "limit %d", limit)
	}
}

func TestSkillContent_LimitIsCapped(t *testing.T) {
	big := make([]byte, 70000)
	for i := range big {
		big[i] = 'x'
	}
	_, srv, _ := skillsServer(t, true, contentFixture(string(big)))
	rec := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"s"}, "limit": {"1000000"}}), scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code)
	d := scopeDecodeData(t, rec)
	require.Len(t, d["text"].(string), 65536)
	require.EqualValues(t, 65536, d["next_offset"])
	// The default limit is the maximum.
	rec = skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"s"}}), scopeAdminAPIKey)
	require.Len(t, scopeDecodeData(t, rec)["text"].(string), 65536)
}

func TestSkillContent_Errors(t *testing.T) {
	cases := []struct {
		name  string
		q     url.Values
		setup func(*skillsTestController)
		code  int
		body  string
	}{
		{"binary", url.Values{"skill": {"s"}, "file": {"bin.dat"}}, nil, http.StatusUnsupportedMediaType, ""},
		{"unknown skill", url.Values{"skill": {"zzz"}}, nil, http.StatusNotFound, ""},
		{"unknown file", url.Values{"skill": {"s"}, "file": {"nope.md"}}, nil, http.StatusNotFound, ""},
		{"missing skill param", url.Values{}, nil, http.StatusBadRequest, ""},
		{"negative offset", url.Values{"skill": {"s"}, "offset": {"-1"}}, nil, http.StatusBadRequest, ""},
		{"non numeric offset", url.Values{"skill": {"s"}, "offset": {"abc"}}, nil, http.StatusBadRequest, ""},
		{"offset past end", url.Values{"skill": {"s"}, "offset": {"99"}}, nil, http.StatusBadRequest, ""},
		{"zero limit", url.Values{"skill": {"s"}, "limit": {"0"}}, nil, http.StatusBadRequest, ""},
		{"bad copy", url.Values{"skill": {"s"}, "copy": {"both"}}, nil, http.StatusBadRequest, ""},
		{"damaged copy", url.Values{"skill": {"s"}}, func(c *skillsTestController) { c.fileErr = internalRuntime.ErrSkillStoredCopyDamaged }, http.StatusConflict, "stored_copy_damaged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
				contentFixture("hello")(c)
				if tc.setup != nil {
					tc.setup(c)
				}
			})
			rec := skillsDo(t, srv, http.MethodGet, contentURL(tc.q), scopeAdminAPIKey)
			require.Equal(t, tc.code, rec.Code, rec.Body.String())
			if tc.body != "" {
				var e map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
				require.Equal(t, tc.body, e["error_code"])
			}
		})
	}
}

func TestSkillContent_OffsetInsideRuneIs400(t *testing.T) {
	_, srv, _ := skillsServer(t, true, contentFixture("é!"))
	rec := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"s"}, "offset": {"1"}}), scopeAdminAPIKey)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSkillContent_FeatureOff(t *testing.T) {
	ctrl, srv, _ := skillsServer(t, false, contentFixture("x"))
	rec := skillsDo(t, srv, http.MethodGet, contentURL(url.Values{"skill": {"s"}}), scopeAdminAPIKey)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Empty(t, ctrl.recorded())
}

func TestSkillsRefresh(t *testing.T) {
	t.Run("happy path returns the listing", func(t *testing.T) {
		ctrl, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
			c.recs["alpha"] = []*storage.SkillApprovalRecord{skillRec("s", storage.SkillStatusPending, "sk1-x")}
			c.afterRefresh = &internalRuntime.SkillsServerStatus{Supported: true, LastRefresh: time.Now()}
		})
		rec := skillsDo(t, srv, http.MethodPost, "/api/v1/servers/alpha/skills/refresh", scopeAdminAPIKey)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		d := scopeDecodeData(t, rec)
		require.Equal(t, true, d["supported"])
		require.Len(t, d["skills"], 1)
		require.Contains(t, ctrl.recorded(), "RefreshServerSkills")
	})
	t.Run("superseded by a reconnect is a conflict, not a server error", func(t *testing.T) {
		_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
			c.refreshErr = fmt.Errorf("apply: %w", internalRuntime.ErrSkillsRefreshSuperseded)
		})
		rec := skillsDo(t, srv, http.MethodPost, "/api/v1/servers/alpha/skills/refresh", scopeAdminAPIKey)
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "refresh_superseded")
	})
	t.Run("not supported", func(t *testing.T) {
		_, srv, _ := skillsServer(t, true, nil)
		rec := skillsDo(t, srv, http.MethodPost, "/api/v1/servers/alpha/skills/refresh", scopeAdminAPIKey)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Contains(t, rec.Body.String(), "skills_not_supported")
	})
	t.Run("feature disabled", func(t *testing.T) {
		ctrl, srv, _ := skillsServer(t, false, nil)
		rec := skillsDo(t, srv, http.MethodPost, "/api/v1/servers/alpha/skills/refresh", scopeAdminAPIKey)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Contains(t, rec.Body.String(), "skills_disabled")
		require.Empty(t, ctrl.recorded())
	})
	t.Run("unknown server", func(t *testing.T) {
		_, srv, _ := skillsServer(t, true, nil)
		rec := skillsDo(t, srv, http.MethodPost, "/api/v1/servers/nope/skills/refresh", scopeAdminAPIKey)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// FR-019a: a scoped agent token is refused on the refresh endpoint and the
// controller is never reached. The two GET endpoints answer a scoped caller
// with available skills only (skills_scoped_test.go).
func TestSkillsEndpoints_ScopedAgentTokenRefusedWithoutRuntimeCall(t *testing.T) {
	ctrl, srv, token := skillsServer(t, true, contentFixture("hello"))
	rec := skillsDo(t, srv, http.MethodPost, "/api/v1/servers/alpha/skills/refresh", token)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Agent tokens cannot")
	require.Empty(t, ctrl.recorded(), "FR-019a: no runtime call for a scoped caller")
}
