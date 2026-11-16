package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

func connectSkillsClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	disableOAuthForTest(t)
	cfg := &config.ServerConfig{Name: "skills-up", Protocol: "streamable-http", URL: endpoint, Enabled: true}
	c, err := NewClient("skills-up", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

func methodsOf(fx *skillstest.Fixture) []string { return fx.Methods() }

func TestSupportsSkillsMatrix(t *testing.T) {
	cases := []struct {
		name string
		opts []skillstest.Option
		want bool
	}{
		{"extension and resources", nil, true},
		{"extension only", []skillstest.Option{skillstest.WithoutResources()}, false},
		{"neither", []skillstest.Option{skillstest.WithoutResources(), skillstest.WithoutExtension()}, false},
		{"resources only", []skillstest.Option{skillstest.WithoutExtension()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := skillstest.New(t, tc.opts...)
			c := connectSkillsClient(t, fx.URL())
			assert.Equal(t, tc.want, c.SupportsSkills())
		})
	}
}

func TestListSkillsSendsNothingWithoutSupport(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithoutExtension())
	fx.AddSkill(skillstest.Skill{Path: "pdf"})
	c := connectSkillsClient(t, fx.URL())

	entries, errs, err := c.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Empty(t, errs)
	assert.Zero(t, fx.Count("skills/list"))
}

func TestListSkillsSendsNothingWhenNotExposed(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf"})
	c := connectSkillsClient(t, fx.URL())
	off := false
	c.SetExposeSkills(&off)

	entries, _, err := c.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Zero(t, fx.Count("skills/list"))

	c.SetExposeSkills(nil)
	entries, _, err = c.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestListSkillsPagination(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithPageSize(2))
	for i := 0; i < 5; i++ {
		fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%d", i)})
	}
	c := connectSkillsClient(t, fx.URL())

	entries, errs, err := c.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Empty(t, errs)
	assert.Len(t, entries, 5)
	assert.Equal(t, 3, fx.Count("skills/list"))
	assert.Equal(t, "skill://s0/SKILL.md", entries[0].URI)
	assert.Len(t, entries[0].Files, 1)
}

func TestListSkillsEntryCapReturnsTruncated(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithPageSize(100))
	for i := 0; i < 250; i++ {
		fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%03d", i)})
	}
	c := connectSkillsClient(t, fx.URL())

	entries, _, err := c.ListSkills(context.Background())
	var tr *skills.TruncatedError
	require.ErrorAs(t, err, &tr)
	assert.Equal(t, skills.BoundEntries, tr.Bound)
	assert.Len(t, entries, skills.MaxSkillsPerServer)
}

func TestListSkillsPageCapReturnsTruncated(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithPageSize(1))
	for i := 0; i < skills.MaxListPages+10; i++ {
		fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%03d", i)})
	}
	c := connectSkillsClient(t, fx.URL())

	entries, _, err := c.ListSkills(context.Background())
	var tr *skills.TruncatedError
	require.ErrorAs(t, err, &tr)
	assert.Equal(t, skills.BoundPages, tr.Bound)
	assert.Len(t, entries, skills.MaxListPages)
	assert.Equal(t, skills.MaxListPages, fx.Count("skills/list"))
}

func TestListSkillsExactlyFullListingIsNotTruncated(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithPageSize(1))
	for i := 0; i < skills.MaxListPages; i++ {
		fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%03d", i)})
	}
	c := connectSkillsClient(t, fx.URL())
	entries, _, err := c.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, skills.MaxListPages)
}

func TestListSkillsFailureIsAnError(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf"})
	c := connectSkillsClient(t, fx.URL())
	fx.SetListFailure(true)

	_, _, err := c.ListSkills(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skills/list failed on purpose")
}

// recordingProxy forwards to target and records every JSON-RPC request body.
func recordingProxy(t *testing.T, target string) (string, func() [][]byte) {
	t.Helper()
	u, err := url.Parse(target)
	require.NoError(t, err)
	var mu sync.Mutex
	var bodies [][]byte
	rp := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), bodies...)
	}
}

func TestSkillRequestIDsAreStrings(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithPageSize(1))
	fx.AddSkill(skillstest.Skill{Path: "a"})
	fx.AddSkill(skillstest.Skill{Path: "b"})
	endpoint, bodies := recordingProxy(t, fx.URL())
	c := connectSkillsClient(t, endpoint)

	_, _, err := c.ListSkills(context.Background())
	require.NoError(t, err)
	_, err = c.GetSkill(context.Background(), "skill://a/SKILL.md")
	require.NoError(t, err)

	seen := 0
	ids := map[string]bool{}
	for _, b := range bodies() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.Unmarshal(b, &req))
		if !strings.HasPrefix(req.Method, "skills/") {
			continue
		}
		seen++
		var id string
		require.NoError(t, json.Unmarshal(req.ID, &id), "id must be a JSON string: %s", req.ID)
		assert.True(t, strings.HasPrefix(id, "mcpproxy-skills-"), id)
		assert.False(t, ids[id], "ids must be unique: %s", id)
		ids[id] = true
	}
	assert.Equal(t, 3, seen)
}

// A listing entry that does not decode is reported without failing the list.
func TestListSkillsPerEntryDecodeError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(b, &req)
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities": map[string]any{
					"resources":  map[string]any{},
					"extensions": map[string]any{skills.ExtensionKey: map[string]any{}},
				},
				"serverInfo": map[string]any{"name": "raw", "version": "1"},
			}
		case "skills/list":
			result = map[string]any{"skills": []any{
				map[string]any{"uri": "skill://bad/SKILL.md", "frontmatter": map[string]any{}, "resources": 7},
				map[string]any{"uri": "skill://ok/SKILL.md", "frontmatter": map[string]any{"name": "ok", "description": "d"},
					"resources": []any{map[string]any{"uri": "skill://ok/SKILL.md", "digest": "sha256:" + strings.Repeat("0", 64), "size": 1}}},
			}}
		default:
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer up.Close()
	c := connectSkillsClient(t, up.URL)

	entries, errs, err := c.ListSkills(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "skill://ok/SKILL.md", entries[0].URI)
	require.Len(t, errs, 1)
	assert.Equal(t, "skill://bad/SKILL.md", errs[0].URI)
}

func TestGetSkill(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"ref/a.md": []byte("A")}})
	c := connectSkillsClient(t, fx.URL())

	e, err := c.GetSkill(context.Background(), "skill://pdf/SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, "skill://pdf/SKILL.md", e.URI)
	assert.Len(t, e.Files, 2)

	_, err = c.GetSkill(context.Background(), "skill://nope/SKILL.md")
	require.Error(t, err)
}

func fileRef(t *testing.T, c *Client, skillURI, rel string) skills.FileRef {
	t.Helper()
	e, err := c.GetSkill(context.Background(), skillURI)
	require.NoError(t, err)
	for _, f := range e.Files {
		if strings.HasSuffix(f.URI, "/"+rel) {
			return f
		}
	}
	t.Fatalf("file %s not listed", rel)
	return skills.FileRef{}
}

func TestReadSkillFileText(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"ref/a.md": []byte("héllo")}})
	c := connectSkillsClient(t, fx.URL())

	ref := fileRef(t, c, "skill://pdf/SKILL.md", "ref/a.md")
	content, isText, mime, err := c.ReadSkillFile(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, []byte("héllo"), content)
	assert.True(t, isText)
	assert.Equal(t, "text/plain", mime)
	assert.Nil(t, skills.VerifyFile(ref, content))
}

func TestReadSkillFileBlob(t *testing.T) {
	data := []byte{0x00, 0xff, 0x10, 0x80}
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"img.bin": data}, Binary: []string{"img.bin"}})
	c := connectSkillsClient(t, fx.URL())

	ref := fileRef(t, c, "skill://pdf/SKILL.md", "img.bin")
	content, isText, mime, err := c.ReadSkillFile(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, data, content)
	assert.False(t, isText)
	assert.Equal(t, "application/octet-stream", mime)
}

// A JSON string cannot carry invalid UTF-8 through mcp-go's decoder (it is
// replaced), so the classification is tested on its own.
func TestSkillTextClassification(t *testing.T) {
	b, isText := classifySkillText("héllo")
	assert.True(t, isText)
	assert.Equal(t, []byte("héllo"), b)

	b, isText = classifySkillText(string([]byte{'a', 0xff, 'b'}))
	assert.False(t, isText, "text that is not valid UTF-8 is binary")
	assert.Equal(t, []byte{'a', 0xff, 'b'}, b)
}

func TestReadSkillFileSizeMismatchRefusedBeforeHashing(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("abc")}, WrongSize: []string{"a.md"}})
	c := connectSkillsClient(t, fx.URL())
	ref := fileRef(t, c, "skill://pdf/SKILL.md", "a.md")

	_, _, _, err := c.ReadSkillFile(context.Background(), ref)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "size")
}

func TestReadSkillFileContentURIMismatchRefused(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("abc")}})
	c := connectSkillsClient(t, fx.URL())
	ref := fileRef(t, c, "skill://pdf/SKILL.md", "a.md")

	// A proxy that rewrites the reply's content uri.
	u, _ := url.Parse(fx.URL())
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.ModifyResponse = func(r *http.Response) error {
		b, _ := io.ReadAll(r.Body)
		b = []byte(strings.ReplaceAll(string(b), `"uri":"skill://pdf/a.md"`, `"uri":"skill://pdf/other.md"`))
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		r.ContentLength = int64(len(b))
		r.Header.Set("Content-Length", fmt.Sprint(len(b)))
		return nil
	}
	srv := httptest.NewServer(rp)
	defer srv.Close()
	c2 := connectSkillsClient(t, srv.URL)

	_, _, _, err := c2.ReadSkillFile(context.Background(), ref)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uri")
}

func TestReadSkillFileOversizedResponseRefusedByLimiter(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{
		Path:     "pdf",
		Files:    map[string][]byte{"a.md": []byte("abc")},
		Oversize: map[string]int{"a.md": 1 << 20},
	})
	c := connectSkillsClient(t, fx.URL())
	ref := skills.FileRef{URI: "skill://pdf/a.md", Digest: "sha256:" + strings.Repeat("0", 64), Size: 3}

	_, _, _, err := c.ReadSkillFile(context.Background(), ref)
	require.Error(t, err)
	assert.ErrorContains(t, err, "size limit", "the limiter, not size verification, must have refused it")
}

func TestListSkillsConnectedRequired(t *testing.T) {
	fx := skillstest.New(t)
	c := connectSkillsClient(t, fx.URL())
	require.NoError(t, c.Disconnect())
	_, _, err := c.ListSkills(context.Background())
	require.Error(t, err)
	_ = methodsOf
}
