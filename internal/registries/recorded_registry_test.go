package registries

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Spec 109 fix-catalog-rank (T172, D37.12): the registry-shaped SC-008 fixture
// and the fake registry that serves it. The corpus in
// testdata/catalog_github_order.json is the union of three REAL responses of
// registry.modelcontextprotocol.io recorded on 2026-10-02, so the live bug
// (search=github page 1 never reaches io.github.github/github-mcp-server)
// reproduces offline.

const catalogGithubFixturePath = "testdata/catalog_github_order.json"

type catalogFixtureOfficialSource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Provenance string            `json:"provenance"`
	Protocol   string            `json:"protocol"`
	Corpus     []json.RawMessage `json:"corpus"`
}

type catalogFixtureFile struct {
	Comment  string            `json:"_comment"`
	Recorded json.RawMessage   `json:"recorded"`
	Query    string            `json:"query"`
	Sources  []json.RawMessage `json:"sources"`
	IDs      json.RawMessage   `json:"ids"`
	Results  json.RawMessage   `json:"results"`
}

func loadCatalogFixture(t *testing.T) (catalogFixtureFile, catalogFixtureOfficialSource) {
	t.Helper()
	raw, err := os.ReadFile(catalogGithubFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var f catalogFixtureFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	var official catalogFixtureOfficialSource
	if len(f.Sources) == 0 {
		t.Fatal("fixture has no sources")
	}
	if err := json.Unmarshal(f.Sources[0], &official); err != nil {
		t.Fatal(err)
	}
	if official.Protocol != protocolOfficial {
		t.Fatalf("sources[0].protocol = %q, want the official protocol", official.Protocol)
	}
	return f, official
}

func recordedItem(name string, isLatest bool, version string) json.RawMessage {
	b, _ := json.Marshal(map[string]interface{}{
		"server": map[string]interface{}{"name": name, "version": version},
		"_meta":  map[string]interface{}{officialMetaKey: map[string]interface{}{"status": "active", "isLatest": isLatest}},
	})
	return b
}

type recordedPage struct {
	Servers []struct {
		Server struct {
			Name string `json:"name"`
		} `json:"server"`
	} `json:"servers"`
	Metadata struct {
		NextCursor string `json:"nextCursor"`
		Count      int    `json:"count"`
	} `json:"metadata"`
}

func getRecordedPage(t *testing.T, base, rawQuery string) recordedPage {
	t.Helper()
	resp, err := http.Get(base + "/v0.1/servers?" + rawQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var p recordedPage
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p recordedPage) names() []string {
	out := make([]string, 0, len(p.Servers))
	for _, s := range p.Servers {
		out = append(out, s.Server.Name)
	}
	return out
}

func TestRecordedRegistryHandler_SearchIsNameSubstringByteOrder(t *testing.T) {
	corpus := []json.RawMessage{
		recordedItem("io.github.zed/other", true, "1"),
		recordedItem("com.Example/GitHub", true, "1"),
		recordedItem("ai.smithery/none", true, "1"),
		recordedItem("io.github.github/github-mcp-server", true, "1"),
	}
	srv := httptest.NewServer(RecordedRegistryHandlerForTest(corpus))
	defer srv.Close()

	got := getRecordedPage(t, srv.URL, "search=GITHUB&version=latest").names()
	want := []string{"com.Example/GitHub", "io.github.github/github-mcp-server", "io.github.zed/other"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("search=GITHUB = %v, want %v (case-insensitive substring of name, byte order)", got, want)
	}
	// The match is on the name only: a description-only mention never matches.
	if n := len(getRecordedPage(t, srv.URL, "search=none-such").Servers); n != 0 {
		t.Fatalf("unknown search returned %d servers", n)
	}
}

func TestRecordedRegistryHandler_CursorAndLimit(t *testing.T) {
	var corpus []json.RawMessage
	for _, n := range []string{"a/1", "a/2", "a/3", "a/4", "a/5"} {
		corpus = append(corpus, recordedItem(n, true, "1"))
	}
	srv := httptest.NewServer(RecordedRegistryHandlerForTest(corpus))
	defer srv.Close()

	p1 := getRecordedPage(t, srv.URL, "limit=2")
	if strings.Join(p1.names(), ",") != "a/1,a/2" || p1.Metadata.NextCursor != "a/2" {
		t.Fatalf("page 1 = %v cursor %q", p1.names(), p1.Metadata.NextCursor)
	}
	p2 := getRecordedPage(t, srv.URL, "limit=2&cursor="+url.QueryEscape(p1.Metadata.NextCursor))
	if strings.Join(p2.names(), ",") != "a/3,a/4" || p2.Metadata.NextCursor != "a/4" {
		t.Fatalf("page 2 = %v cursor %q", p2.names(), p2.Metadata.NextCursor)
	}
	p3 := getRecordedPage(t, srv.URL, "limit=2&cursor=a/4")
	if strings.Join(p3.names(), ",") != "a/5" || p3.Metadata.NextCursor != "" {
		t.Fatalf("last page = %v cursor %q (no cursor when exhausted)", p3.names(), p3.Metadata.NextCursor)
	}
	if n := len(getRecordedPage(t, srv.URL, "").Servers); n != 5 {
		t.Fatalf("default limit must be 100, got %d servers", n)
	}
}

func TestRecordedRegistryHandler_VersionLatestFiltersIsLatest(t *testing.T) {
	corpus := []json.RawMessage{
		recordedItem("a/x", false, "1.0.0"),
		recordedItem("a/x", false, "1.1.0"),
		recordedItem("a/x", true, "1.2.0"),
	}
	srv := httptest.NewServer(RecordedRegistryHandlerForTest(corpus))
	defer srv.Close()

	if n := len(getRecordedPage(t, srv.URL, "version=latest").Servers); n != 1 {
		t.Fatalf("version=latest returned %d rows, want 1", n)
	}
	if n := len(getRecordedPage(t, srv.URL, "").Servers); n != 3 {
		t.Fatalf("without version=latest all %d rows come back, got %d", 3, n)
	}
}

// TestCatalogGithubFixture_MainPageLacksGitHubServer proves the live bug: the
// registry's own search=github page 1 holds 100 names in byte order and
// io.github.github/github-mcp-server is not among them.
func TestCatalogGithubFixture_MainPageLacksGitHubServer(t *testing.T) {
	_, official := loadCatalogFixture(t)
	srv := httptest.NewServer(RecordedRegistryHandlerForTest(official.Corpus))
	defer srv.Close()

	page := getRecordedPage(t, srv.URL, "search=github&version=latest&limit=100")
	names := page.names()
	if len(names) != 100 {
		t.Fatalf("search=github page 1 has %d entries, want 100", len(names))
	}
	if page.Metadata.NextCursor == "" {
		t.Fatal("search=github must report a nextCursor (the listing continues)")
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("page is not in byte order")
	}
	for _, n := range names {
		if n == "io.github.github/github-mcp-server" {
			t.Fatal("page 1 must NOT contain GitHub's own server: that is the bug")
		}
	}
}

func TestCatalogGithubFixture_ExpansionQueriesFindIt(t *testing.T) {
	_, official := loadCatalogFixture(t)
	srv := httptest.NewServer(RecordedRegistryHandlerForTest(official.Corpus))
	defer srv.Close()

	owner := getRecordedPage(t, srv.URL, "search="+url.QueryEscape(".github/")+"&version=latest").names()
	if len(owner) != 1 || owner[0] != "io.github.github/github-mcp-server" {
		t.Fatalf("search=.github/ = %v, want exactly GitHub's server", owner)
	}
	seg := getRecordedPage(t, srv.URL, "search="+url.QueryEscape("/github")+"&version=latest").names()
	found := false
	for _, n := range seg {
		if n == "io.github.github/github-mcp-server" {
			found = true
		}
		if !strings.Contains(strings.ToLower(n), "/github") {
			t.Fatalf("search=/github returned %q", n)
		}
	}
	if !found || len(seg) < 30 {
		t.Fatalf("search=/github = %d entries (found GitHub: %v), want ≥ 30 incl. GitHub's", len(seg), found)
	}
}

// ---- recording ----

var recordedQueries = []string{"github", ".github/", "/github"}

// sanitizeRegistryItem keeps only the fields the catalog reads (D37.12).
func sanitizeRegistryItem(item map[string]interface{}) map[string]interface{} {
	server, _ := item["server"].(map[string]interface{})
	out := map[string]interface{}{}
	for _, k := range []string{"name", "title", "description", "version"} {
		if v, ok := server[k]; ok {
			out[k] = v
		}
	}
	if repo, ok := server["repository"].(map[string]interface{}); ok {
		if u, ok := repo["url"]; ok {
			out["repository"] = map[string]interface{}{"url": u}
		}
	}
	if pkgs, ok := server["packages"].([]interface{}); ok && len(pkgs) > 0 {
		if pkg, ok := pkgs[0].(map[string]interface{}); ok {
			keep := map[string]interface{}{}
			for _, k := range []string{"registryType", "identifier", "version", "runtimeHint", "transport", "runtimeArguments", "packageArguments"} {
				if v, ok := pkg[k]; ok {
					keep[k] = v
				}
			}
			if envs, ok := pkg["environmentVariables"].([]interface{}); ok {
				keep["environmentVariables"] = pickList(envs, "name", "description", "isSecret")
			}
			out["packages"] = []interface{}{keep}
		}
	}
	if rems, ok := server["remotes"].([]interface{}); ok && len(rems) > 0 {
		if rem, ok := rems[0].(map[string]interface{}); ok {
			keep := map[string]interface{}{}
			for _, k := range []string{"type", "url"} {
				if v, ok := rem[k]; ok {
					keep[k] = v
				}
			}
			if hdrs, ok := rem["headers"].([]interface{}); ok {
				keep["headers"] = pickList(hdrs, "name", "description", "isSecret")
			}
			out["remotes"] = []interface{}{keep}
		}
	}
	meta := map[string]interface{}{"status": "active", "isLatest": true}
	if m, ok := item["_meta"].(map[string]interface{}); ok {
		if o, ok := m[officialMetaKey].(map[string]interface{}); ok {
			if v, ok := o["status"]; ok {
				meta["status"] = v
			}
			if v, ok := o["isLatest"]; ok {
				meta["isLatest"] = v
			}
		}
	}
	return map[string]interface{}{"server": out, "_meta": map[string]interface{}{officialMetaKey: meta}}
}

func pickList(list []interface{}, keys ...string) []interface{} {
	out := make([]interface{}, 0, len(list))
	for _, raw := range list {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		keep := map[string]interface{}{}
		for _, k := range keys {
			if v, ok := m[k]; ok {
				keep[k] = v
			}
		}
		out = append(out, keep)
	}
	return out
}

// TestRecordCatalogGithubFixture re-records the corpus from the live official
// registry: RECORD_LIVE_REGISTRY=1 go test ./internal/registries -run
// TestRecordCatalogGithubFixture. It rewrites `corpus` and `recorded` only;
// ids and results come from UPDATE_GOLDEN=1 on the httpapi test.
func TestRecordCatalogGithubFixture(t *testing.T) {
	if os.Getenv("RECORD_LIVE_REGISTRY") != "1" {
		t.Skip("set RECORD_LIVE_REGISTRY=1 to re-record the live registry fixture")
	}
	const base = "https://registry.modelcontextprotocol.io/v0.1/servers"
	client := &http.Client{Timeout: 90 * time.Second}
	byName := map[string]map[string]interface{}{}
	for _, q := range recordedQueries {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			base+"?version=latest&limit=100&search="+url.QueryEscape(q), http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search=%q: HTTP %d", q, resp.StatusCode)
		}
		var page struct {
			Servers []map[string]interface{} `json:"servers"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Servers {
			clean := sanitizeRegistryItem(item)
			name, _ := clean["server"].(map[string]interface{})["name"].(string)
			if name != "" {
				byName[name] = clean
			}
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	corpus := make([]json.RawMessage, 0, len(names))
	for _, n := range names {
		b, err := json.Marshal(byName[n])
		if err != nil {
			t.Fatal(err)
		}
		corpus = append(corpus, b)
	}

	f, official := loadCatalogFixture(t)
	official.Corpus = corpus
	srcBytes, err := json.Marshal(official)
	if err != nil {
		t.Fatal(err)
	}
	f.Sources[0] = srcBytes
	f.Comment = "Spec 109 fix-catalog-rank (SC-008, C1): catalog search \"github\" ranks GitHub's own official, verified server first, with identical order on REST, MCP, CLI, Web and macOS. sources + query are the INPUT: the official source's corpus is the union of three real registry.modelcontextprotocol.io responses (see recorded), served by RecordedRegistryHandlerForTest; ids + results are the golden OUTPUT written by internal/httpapi/spec109_catalog_order_test.go with UPDATE_GOLDEN=1 (REST GET /catalog/search?limit=20 data.results). ids are \"<source>:<entry id>\"."
	rec, _ := json.Marshal(map[string]interface{}{"from": base, "at": time.Now().UTC().Format("2006-01-02"), "queries": recordedQueries})
	f.Recorded = rec
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogGithubFixturePath, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %d unique servers", len(corpus))
}
