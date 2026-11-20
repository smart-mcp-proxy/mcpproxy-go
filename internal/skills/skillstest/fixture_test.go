package skillstest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func call(t *testing.T, fx *Fixture, method string, params any) (json.RawMessage, *rpcError, int) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "t-1", "method": method, "params": params})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, fx.URL(), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if len(raw) == 0 {
		return nil, nil, resp.StatusCode
	}
	var env struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	assert.JSONEq(t, `"t-1"`, string(env.ID))
	return env.Result, env.Error, resp.StatusCode
}

func list(t *testing.T, fx *Fixture, cursor string) skills.ListPage {
	t.Helper()
	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	res, rerr, _ := call(t, fx, "skills/list", params)
	require.Nil(t, rerr)
	page, err := skills.DecodeListResult(res)
	require.NoError(t, err)
	require.Empty(t, page.Errors)
	return page
}

func readFile(t *testing.T, fx *Fixture, uri string) (text string, blob []byte, isBlob bool, rerr *rpcError) {
	t.Helper()
	res, rerr, _ := call(t, fx, "resources/read", map[string]any{"uri": uri})
	if rerr != nil {
		return "", nil, false, rerr
	}
	var out struct {
		Contents []struct {
			URI  string  `json:"uri"`
			Text *string `json:"text"`
			Blob *string `json:"blob"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(res, &out))
	require.Len(t, out.Contents, 1)
	assert.Equal(t, uri, out.Contents[0].URI)
	if out.Contents[0].Blob != nil {
		b, err := base64.StdEncoding.DecodeString(*out.Contents[0].Blob)
		require.NoError(t, err)
		return "", b, true, nil
	}
	require.NotNil(t, out.Contents[0].Text)
	return *out.Contents[0].Text, nil, false, nil
}

func pdfSkill() Skill {
	return Skill{Path: "pdf", Files: map[string][]byte{
		"SKILL.md":        []byte("---\nname: pdf\ndescription: Work with PDFs\n---\n# PDF\n"),
		"scripts/x.py":    []byte("print('x')\n"),
		"references/a.md": []byte("ref"),
	}}
}

func TestInitializeCapabilities(t *testing.T) {
	cases := map[string]struct {
		opts      []Option
		extension bool
		resources bool
	}{
		"default":      {nil, true, true},
		"no extension": {[]Option{WithoutExtension()}, false, true},
		"no resources": {[]Option{WithoutResources()}, true, false},
		"neither":      {[]Option{WithoutExtension(), WithoutResources()}, false, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := New(t, tc.opts...)
			res, rerr, _ := call(t, fx, "initialize", map[string]any{"protocolVersion": "2025-11-25"})
			require.Nil(t, rerr)
			var out struct {
				ProtocolVersion string         `json:"protocolVersion"`
				Capabilities    map[string]any `json:"capabilities"`
			}
			require.NoError(t, json.Unmarshal(res, &out))
			assert.Equal(t, "2025-11-25", out.ProtocolVersion)
			_, hasRes := out.Capabilities["resources"]
			assert.Equal(t, tc.resources, hasRes)
			ext, _ := out.Capabilities["extensions"].(map[string]any)
			_, hasExt := ext[skills.ExtensionKey]
			assert.Equal(t, tc.extension, hasExt)
		})
	}
}

func TestNotificationAndToolsList(t *testing.T) {
	fx := New(t)
	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, fx.URL(), bytes.NewReader(body))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	res, rerr, _ := call(t, fx, "tools/list", map[string]any{})
	require.Nil(t, rerr)
	assert.Contains(t, string(res), `"tools"`)

	_, rerr, _ = call(t, fx, "nope/method", map[string]any{})
	require.NotNil(t, rerr)
	assert.Equal(t, -32601, rerr.Code)
}

func TestServedBytesHashToListedDigestByDefault(t *testing.T) {
	fx := New(t)
	fx.AddSkill(pdfSkill())
	page := list(t, fx, "")
	require.Len(t, page.Entries, 1)
	e := page.Entries[0]
	assert.Equal(t, "skill://pdf/SKILL.md", e.URI)
	require.Nil(t, e.Validate())
	require.Len(t, e.Files, 3)

	for _, f := range e.Files {
		text, _, isBlob, rerr := readFile(t, fx, f.URI)
		require.Nil(t, rerr)
		require.False(t, isBlob)
		assert.Nil(t, skills.VerifyFile(f, []byte(text)), f.URI)
	}

	fm, err := skills.ParseFrontmatter(pdfSkill().Files["SKILL.md"])
	require.NoError(t, err)
	assert.True(t, skills.FrontmatterEqual(e.Frontmatter, fm))

	res, rerr, _ := call(t, fx, "skills/get", map[string]any{"uri": e.URI})
	require.Nil(t, rerr)
	got, err := skills.DecodeGetResult(res)
	require.NoError(t, err)
	assert.Equal(t, e, got)

	_, rerr, _ = call(t, fx, "skills/get", map[string]any{"uri": "skill://missing/SKILL.md"})
	require.NotNil(t, rerr)
	_, _, _, rerr = readFile(t, fx, "skill://pdf/nope")
	require.NotNil(t, rerr)
}

func TestSkillMDIsGeneratedWhenOmitted(t *testing.T) {
	fx := New(t)
	fx.AddSkill(Skill{Path: "acme/billing/refunds", Files: map[string][]byte{"notes.txt": []byte("n")}})
	page := list(t, fx, "")
	require.Len(t, page.Entries, 1)
	assert.Nil(t, page.Entries[0].Validate())
	assert.Equal(t, "refunds", page.Entries[0].Frontmatter["name"])
}

func TestPagination(t *testing.T) {
	fx := New(t, WithPageSize(2))
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		fx.AddSkill(Skill{Path: n})
	}
	var seen []string
	cursor := ""
	pages := 0
	for {
		page := list(t, fx, cursor)
		pages++
		for _, e := range page.Entries {
			seen = append(seen, e.URI)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	assert.Equal(t, 3, pages)
	assert.Len(t, seen, 5)

	// Without a page size everything comes back and there is no cursor.
	fx2 := New(t)
	fx2.AddSkill(Skill{Path: "a"})
	fx2.AddSkill(Skill{Path: "b"})
	page := list(t, fx2, "")
	assert.Len(t, page.Entries, 2)
	assert.Empty(t, page.NextCursor)
}

func TestKnobs(t *testing.T) {
	files := func() map[string][]byte {
		return map[string][]byte{"SKILL.md": []byte("---\nname: k\ndescription: d\n---\nbody"), "f.txt": []byte("0123456789")}
	}
	entry := func(t *testing.T, fx *Fixture) skills.Entry {
		page := list(t, fx, "")
		require.Len(t, page.Entries, 1)
		return page.Entries[0]
	}
	find := func(e skills.Entry, rel string) skills.FileRef {
		for _, f := range e.Files {
			if f.URI == "skill://k/"+rel {
				return f
			}
		}
		t.Fatalf("no file %s", rel)
		return skills.FileRef{}
	}

	t.Run("wrong digest", func(t *testing.T) {
		fx := New(t)
		fx.AddSkill(Skill{Path: "k", Files: files(), WrongDigest: []string{"f.txt"}})
		e := entry(t, fx)
		text, _, _, _ := readFile(t, fx, "skill://k/f.txt")
		ue := skills.VerifyFile(find(e, "f.txt"), []byte(text))
		require.NotNil(t, ue)
		assert.Equal(t, skills.ReasonDigestMismatch, ue.Code)
		assert.Nil(t, skills.VerifyFile(find(e, "SKILL.md"), files()["SKILL.md"]))
	})
	t.Run("wrong size", func(t *testing.T) {
		fx := New(t)
		fx.AddSkill(Skill{Path: "k", Files: files(), WrongSize: []string{"f.txt"}})
		e := entry(t, fx)
		text, _, _, _ := readFile(t, fx, "skill://k/f.txt")
		ue := skills.VerifyFile(find(e, "f.txt"), []byte(text))
		require.NotNil(t, ue)
		assert.Equal(t, skills.ReasonSizeMismatch, ue.Code)
	})
	t.Run("frontmatter differs from listing", func(t *testing.T) {
		fx := New(t)
		fx.AddSkill(Skill{Path: "k", Files: files(), ListedFrontmatter: map[string]any{"name": "k", "description": "a different one"}})
		e := entry(t, fx)
		text, _, _, _ := readFile(t, fx, "skill://k/SKILL.md")
		fm, err := skills.ParseFrontmatter([]byte(text))
		require.NoError(t, err)
		assert.False(t, skills.FrontmatterEqual(e.Frontmatter, fm))
	})
	t.Run("dynamic", func(t *testing.T) {
		fx := New(t)
		fx.AddSkill(Skill{Path: "k", Files: files(), Dynamic: true})
		e := entry(t, fx)
		assert.True(t, e.Dynamic)
		ue := e.Validate()
		require.NotNil(t, ue)
		assert.Equal(t, skills.ReasonDynamic, ue.Code)
	})
	t.Run("binary file is served as a blob", func(t *testing.T) {
		fx := New(t)
		bin := []byte{0xff, 0xfe, 0x00, 0x01}
		f := files()
		f["img.bin"] = bin
		fx.AddSkill(Skill{Path: "k", Files: f, Binary: []string{"img.bin"}})
		e := entry(t, fx)
		_, blob, isBlob, rerr := readFile(t, fx, "skill://k/img.bin")
		require.Nil(t, rerr)
		require.True(t, isBlob)
		assert.Nil(t, skills.VerifyFile(find(e, "img.bin"), blob))
	})
	t.Run("oversized response body", func(t *testing.T) {
		fx := New(t)
		fx.AddSkill(Skill{Path: "k", Files: files(), Oversize: map[string]int{"f.txt": 1 << 20}})
		e := entry(t, fx)
		assert.EqualValues(t, 10, find(e, "f.txt").Size, "the listing still claims the original size")
		text, _, _, _ := readFile(t, fx, "skill://k/f.txt")
		assert.Len(t, text, 10+(1<<20))
	})
	t.Run("failing listing", func(t *testing.T) {
		fx := New(t)
		fx.AddSkill(Skill{Path: "k", Files: files()})
		fx.SetListFailure(true)
		_, rerr, _ := call(t, fx, "skills/list", map[string]any{})
		require.NotNil(t, rerr)
		fx.SetListFailure(false)
		assert.Len(t, list(t, fx, "").Entries, 1)
	})
	t.Run("slow response", func(t *testing.T) {
		fx := New(t)
		fx.SetDelay(150 * time.Millisecond)
		start := time.Now()
		_, rerr, _ := call(t, fx, "tools/list", map[string]any{})
		require.Nil(t, rerr)
		assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)
	})
}

func TestMutateChangesListingAndContent(t *testing.T) {
	fx := New(t)
	fx.AddSkill(pdfSkill())
	before := list(t, fx, "").Entries[0]

	fx.Mutate("pdf", "scripts/x.py", []byte("print('changed')\n"))

	after := list(t, fx, "").Entries[0]
	assert.NotEqual(t, before.Files, after.Files)
	for _, f := range after.Files {
		text, _, _, rerr := readFile(t, fx, f.URI)
		require.Nil(t, rerr)
		assert.Nil(t, skills.VerifyFile(f, []byte(text)))
	}
	text, _, _, _ := readFile(t, fx, "skill://pdf/scripts/x.py")
	assert.Equal(t, "print('changed')\n", text)
}

func TestRequestLog(t *testing.T) {
	fx := New(t)
	fx.AddSkill(pdfSkill())
	assert.Empty(t, fx.Requests())

	call(t, fx, "initialize", map[string]any{})
	list(t, fx, "")
	call(t, fx, "skills/list", map[string]any{"cursor": "1"})
	readFile(t, fx, "skill://pdf/SKILL.md")

	reqs := fx.Requests()
	require.Len(t, reqs, 4)
	assert.Equal(t, "initialize", reqs[0].Method)
	assert.Equal(t, "skills/list", reqs[1].Method)
	assert.Equal(t, "1", reqs[2].Cursor)
	assert.Equal(t, Request{Method: "resources/read", URI: "skill://pdf/SKILL.md"}, reqs[3])
	assert.Equal(t, []string{"initialize", "skills/list", "skills/list", "resources/read"}, fx.Methods())
	assert.Equal(t, 2, fx.Count("skills/list"))

	// The returned slice is a copy.
	reqs[0].Method = "tampered"
	assert.Equal(t, "initialize", fx.Requests()[0].Method)
}

func TestConcurrentUse(t *testing.T) {
	fx := New(t, WithPageSize(1))
	fx.AddSkill(pdfSkill())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := []byte(`{"jsonrpc":"2.0","id":1,"method":"skills/list","params":{}}`)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, fx.URL(), bytes.NewReader(body))
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			if i%2 == 0 {
				fx.Mutate("pdf", "scripts/x.py", []byte{byte(i)})
			}
			_ = fx.Requests()
		}(i)
	}
	wg.Wait()
	assert.Equal(t, 8, fx.Count("skills/list"))
}
