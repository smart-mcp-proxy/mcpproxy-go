package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-2: approve, block and unblock over REST.

func skillsPost(t *testing.T, srv *Server, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func errBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), rec.Body.String())
	return m
}

const approveURL = "/api/v1/servers/alpha/skills/approve"

func TestSkillsApprove_HappyPathPassesBindingsAndActor(t *testing.T) {
	type call struct {
		server   string
		skills   []string
		expected map[string]string
		by       string
		inc      int64
		actor    internalRuntime.Actor
		hasActor bool
	}
	var got call
	ctrl, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		c.approveFn = func(ctx context.Context, server string, skills []string, expected map[string]string, by string, inc int64) (int, error) {
			got = call{server: server, skills: skills, expected: expected, by: by, inc: inc}
			got.actor, got.hasActor = internalRuntime.RequestActor(ctx)
			return 2, nil
		}
	})
	rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey,
		`{"skills":["a","b","a"],"expected_hashes":{"a":"sk1-1","b":"sk1-2"},"expected_incarnation":3}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.EqualValues(t, 2, scopeDecodeData(t, rec)["approved"])
	require.Equal(t, "alpha", got.server)
	require.Equal(t, []string{"a", "b"}, got.skills, "duplicates are folded")
	require.Equal(t, map[string]string{"a": "sk1-1", "b": "sk1-2"}, got.expected)
	require.EqualValues(t, 3, got.inc)
	// The tool approval endpoint stores "api" for a REST caller; so do skills.
	require.Equal(t, "api", got.by)
	require.True(t, got.hasActor, "the request actor must ride in the controller's ctx")
	require.NotEmpty(t, got.actor.Kind)
	require.Contains(t, ctrl.recorded(), "ApproveSkillsReviewedAt")
}

func TestSkillsApprove_MissingHashesIsSentAsEmptyMap(t *testing.T) {
	var got map[string]string
	_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		c.approveFn = func(_ context.Context, _ string, _ []string, expected map[string]string, _ string, _ int64) (int, error) {
			got = expected
			return 0, nil
		}
	})
	rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, `{"skills":["a"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, got, "an unbound request must reach the runtime as bound-to-nothing")
	require.Empty(t, got)
}

func TestSkillsApprove_BadRequests(t *testing.T) {
	many := make([]string, 201)
	for i := range many {
		many[i] = fmt.Sprintf("s%d", i)
	}
	manyJSON, _ := json.Marshal(map[string]interface{}{"skills": many})
	huge := `{"skills":["a"],"expected_hashes":{"a":"` + strings.Repeat("x", 1<<20+10) + `"}}`
	cases := map[string]string{
		"not json":         `skills=a`,
		"unknown field":    `{"skills":["a"],"extra":1}`,
		"empty skills":     `{"skills":[]}`,
		"no skills":        `{}`,
		"too many":         string(manyJSON),
		"empty name":       `{"skills":[""]}`,
		"body too large":   huge,
		"trailing data":    `{"skills":["a"]} {}`,
		"negative inc":     `{"skills":["a"],"expected_incarnation":-1}`,
		"hashes not a map": `{"skills":["a"],"expected_hashes":["x"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl, srv, _ := skillsServer(t, true, nil)
			rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.NotContains(t, ctrl.recorded(), "ApproveSkillsReviewedAt")
		})
	}
}

func TestSkillsApprove_ErrorMapping(t *testing.T) {
	notFound := fmt.Errorf("skill %q: %w", "zz", internalRuntime.ErrSkillNotFound)
	cases := []struct {
		name string
		err  error
		code int
		ec   string
	}{
		{"unknown skill", notFound, http.StatusNotFound, ""},
		{"server not found", &internalRuntime.ServerNotFoundError{Name: "alpha"}, http.StatusNotFound, ""},
		{"server replaced", fmt.Errorf("x: %w", commitview.ErrServerReplaced), http.StatusConflict, ""},
		{"internal", fmt.Errorf("disk on fire"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
				c.approveFn = func(context.Context, string, []string, map[string]string, string, int64) (int, error) {
					return 0, tc.err
				}
			})
			rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, `{"skills":["a"],"expected_hashes":{"a":"sk1-1"}}`)
			require.Equal(t, tc.code, rec.Code, rec.Body.String())
		})
	}
}

func TestSkillsApprove_ServerReplacedBodyMatchesToolEndpoint(t *testing.T) {
	_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		c.approveFn = func(context.Context, string, []string, map[string]string, string, int64) (int, error) {
			return 0, commitview.ErrServerReplaced
		}
	})
	rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, `{"skills":["a"],"expected_hashes":{"a":"sk1-1"},"expected_incarnation":2}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, errBody(t, rec)["error"], "server_replaced")
}

func TestSkillsApprove_StaleReview409(t *testing.T) {
	_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		c.approveFn = func(context.Context, string, []string, map[string]string, string, int64) (int, error) {
			return 0, fmt.Errorf("wrapped: %w", &internalRuntime.StaleSkillReviewError{Server: "alpha", Changed: []string{"a"}})
		}
	})
	rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, `{"skills":["a","b"],"expected_hashes":{"a":"sk1-1"}}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	b := errBody(t, rec)
	require.Equal(t, "review_out_of_date", b["error_code"])
	require.Equal(t, false, b["success"])
	require.Equal(t, []interface{}{"a"}, b["changed"])
	require.Equal(t, []interface{}{}, b["unreviewed"], "unreviewed is present and empty, not null")
}

func TestSkillsApprove_Unusable422(t *testing.T) {
	_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		c.approveFn = func(context.Context, string, []string, map[string]string, string, int64) (int, error) {
			return 0, &internalRuntime.SkillsUnusableError{Server: "alpha", Skills: []string{"dyn"}}
		}
	})
	rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, `{"skills":["dyn"],"expected_hashes":{"dyn":"sk1-1"}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	b := errBody(t, rec)
	require.Equal(t, "skill_unusable", b["error_code"])
	require.Equal(t, []interface{}{"dyn"}, b["skills"])
}

func TestSkillsApprovalRoutes_UnknownServerAndFeatureOff(t *testing.T) {
	for _, route := range []string{"approve", "block", "unblock"} {
		t.Run(route, func(t *testing.T) {
			ctrl, srv, _ := skillsServer(t, true, nil)
			rec := skillsPost(t, srv, "/api/v1/servers/nope/skills/"+route, scopeAdminAPIKey, `{"skills":["a"]}`)
			require.Equal(t, http.StatusNotFound, rec.Code)
			require.Empty(t, ctrl.recorded())

			ctrl, srv, _ = skillsServer(t, false, nil)
			rec = skillsPost(t, srv, "/api/v1/servers/alpha/skills/"+route, scopeAdminAPIKey, `{"skills":["a"]}`)
			require.Equal(t, http.StatusConflict, rec.Code)
			require.Equal(t, "skills_disabled", errBody(t, rec)["error_code"])
			require.Empty(t, ctrl.recorded())
		})
	}
}

func TestSkillsBlockUnblock(t *testing.T) {
	for _, tc := range []struct {
		route   string
		blocked bool
	}{{"block", true}, {"unblock", false}} {
		t.Run(tc.route, func(t *testing.T) {
			var gotBlocked bool
			var gotInc int64
			var gotSkills []string
			var hasActor bool
			_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
				c.blockFn = func(ctx context.Context, _ string, skills []string, blocked bool, _ string, inc int64) (int, error) {
					gotBlocked, gotInc, gotSkills = blocked, inc, skills
					_, hasActor = internalRuntime.RequestActor(ctx)
					return 1, nil
				}
			})
			rec := skillsPost(t, srv, "/api/v1/servers/alpha/skills/"+tc.route, scopeAdminAPIKey, `{"skills":["a"],"expected_incarnation":4}`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.EqualValues(t, 1, scopeDecodeData(t, rec)["changed"])
			require.Equal(t, tc.blocked, gotBlocked)
			require.EqualValues(t, 4, gotInc)
			require.Equal(t, []string{"a"}, gotSkills)
			require.True(t, hasActor)

			// expected_incarnation is optional.
			rec = skillsPost(t, srv, "/api/v1/servers/alpha/skills/"+tc.route, scopeAdminAPIKey, `{"skills":["a"]}`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.EqualValues(t, 0, gotInc)

			for _, bad := range []string{`nope`, `{"skills":[]}`, `{"skills":["a"],"expected_hashes":{}}`} {
				rec = skillsPost(t, srv, "/api/v1/servers/alpha/skills/"+tc.route, scopeAdminAPIKey, bad)
				require.Equal(t, http.StatusBadRequest, rec.Code, bad)
			}
		})
	}
}

func TestSkillsBlock_ErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("skill: %w", internalRuntime.ErrSkillNotFound), http.StatusNotFound},
		{commitview.ErrServerReplaced, http.StatusConflict},
		{fmt.Errorf("boom"), http.StatusInternalServerError},
	} {
		_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
			c.blockFn = func(context.Context, string, []string, bool, string, int64) (int, error) { return 0, tc.err }
		})
		rec := skillsPost(t, srv, "/api/v1/servers/alpha/skills/block", scopeAdminAPIKey, `{"skills":["a"]}`)
		require.Equal(t, tc.code, rec.Code, rec.Body.String())
	}
}

func TestSkillsApprovalRoutes_ScopedTokenRefusedBeforeController(t *testing.T) {
	ctrl, srv, token := skillsServer(t, true, nil)
	ctrl.approveFn = func(context.Context, string, []string, map[string]string, string, int64) (int, error) {
		t.Fatal("controller reached by a scoped caller")
		return 0, nil
	}
	ctrl.blockFn = func(context.Context, string, []string, bool, string, int64) (int, error) {
		t.Fatal("controller reached by a scoped caller")
		return 0, nil
	}
	for _, route := range []string{"approve", "block", "unblock"} {
		rec := skillsPost(t, srv, "/api/v1/servers/alpha/skills/"+route, token, `{"skills":["a"],"expected_hashes":{"a":"x"}}`)
		require.Equal(t, http.StatusForbidden, rec.Code, "%s: %s", route, rec.Body.String())
	}
	require.Empty(t, ctrl.recorded())
}

func TestSkillsList_PR2Fields(t *testing.T) {
	approvedAt := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	scannedAt := time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)
	_, srv, _ := skillsServer(t, true, func(c *skillsTestController) {
		cur := skillRec("cur", storage.SkillStatusApproved, "sk1-cur")
		cur.ApprovedHash, cur.ApprovedAt, cur.ApprovedBy = "sk1-cur", approvedAt, "api"
		cur.Scan = &storage.SkillScan{
			Verdict: "warnings", Complete: false, Incomplete: "size_cap", Hash: "sk1-cur",
			ScannedAt: scannedAt, FindingsTruncated: true,
			Findings: []storage.SkillScanFinding{{File: "SKILL.md", Offset: 7, Rule: "r", Severity: "high", Evidence: "e"}},
		}
		stale := skillRec("stale", storage.SkillStatusChanged, "sk1-new")
		stale.Scan = &storage.SkillScan{Verdict: "dangerous", Complete: true, Hash: "sk1-old", ScannedAt: scannedAt,
			Findings: []storage.SkillScanFinding{{File: "x", Rule: "r"}}}
		fresh := skillRec("fresh", storage.SkillStatusPending, "sk1-f")
		c.recs["alpha"] = []*storage.SkillApprovalRecord{cur, stale, fresh}
		c.incarnations = map[string]int64{"alpha": 5}
	})
	rec := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	d := scopeDecodeData(t, rec)
	require.EqualValues(t, 5, d["incarnation"])
	list := d["skills"].([]interface{})

	c0 := list[0].(map[string]interface{})
	require.Equal(t, "2026-11-17T08:00:00Z", c0["approved_at"])
	require.Equal(t, "api", c0["approved_by"])
	s0 := c0["scan"].(map[string]interface{})
	require.Equal(t, "size_cap", s0["incomplete"])
	require.Equal(t, "2026-11-17T07:00:00Z", s0["scanned_at"])
	require.Equal(t, true, s0["findings_truncated"])
	require.Len(t, s0["findings"], 1)

	// A scan of older content is not shown.
	s1 := list[1].(map[string]interface{})["scan"].(map[string]interface{})
	require.Equal(t, "", s1["verdict"])
	require.Equal(t, []interface{}{}, s1["findings"])
	require.NotContains(t, s1, "scanned_at")

	// Never approved: approved_at omitted.
	c2 := list[2].(map[string]interface{})
	require.NotContains(t, c2, "approved_at")
	require.Equal(t, "", c2["approved_by"])
}

func TestSkillsApprove_BodyOverLimitDoesNotReachController(t *testing.T) {
	ctrl, srv, _ := skillsServer(t, true, nil)
	body := bytes.Repeat([]byte(" "), 1<<20+1)
	body = append([]byte(`{"skills":["a"]}`), body...)
	rec := skillsPost(t, srv, approveURL, scopeAdminAPIKey, string(body))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, ctrl.recorded(), "ApproveSkillsReviewedAt")
}
