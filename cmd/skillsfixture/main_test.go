package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

func rpc(t *testing.T, url, method, uri string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"uri": uri}})
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { assert.NoError(t, resp.Body.Close()) }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// startFixture serves Handler on a loopback listener, bound to that listener's
// own address as the only accepted Host.
func startFixture(t *testing.T) *httptest.Server {
	t.Helper()
	f := skillstest.NewDetached()
	Seed(f)
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = Handler(f, srv.Listener.Addr().String())
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// mutate posts one file change with the given extra headers.
func mutate(t *testing.T, base, query, body string, header map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/_fixture/mutate?"+query, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	assert.NoError(t, resp.Body.Close())
	return resp.StatusCode
}

func TestSeedServesThreeSkillsAndMutateChangesOne(t *testing.T) {
	srv := startFixture(t)

	list := rpc(t, srv.URL+"/mcp", "skills/list", "")
	entries := list["result"].(map[string]any)["skills"].([]any)
	if len(entries) != 3 {
		t.Fatalf("want 3 skills, got %d", len(entries))
	}

	large := rpc(t, srv.URL+"/mcp", "resources/read", "skill://big/data/large.txt")
	text := large["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	if len(text) <= 128*1024 {
		t.Fatalf("large file is %d bytes, want more than two 64 KiB parts", len(text))
	}

	bin := rpc(t, srv.URL+"/mcp", "resources/read", "skill://with-files/logo.bin")
	if _, ok := bin["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["blob"]; !ok {
		t.Fatal("logo.bin must be served as a blob")
	}

	before := rpc(t, srv.URL+"/mcp", "skills/get", "skill://plain/SKILL.md")
	post := func(query, body string) int {
		return mutate(t, srv.URL, query, body, map[string]string{MutateHeader: MutateHeaderValue})
	}
	if c := post("skill=plain&file=SKILL.md", "---\nname: plain\ndescription: changed\n---\nnew\n"); c != http.StatusNoContent {
		t.Fatalf("mutate status %d", c)
	}
	after := rpc(t, srv.URL+"/mcp", "skills/get", "skill://plain/SKILL.md")
	if string(mustJSON(before)) == string(mustJSON(after)) {
		t.Fatal("the listing did not change after mutate")
	}
	if c := post("skill=nope&file=SKILL.md", "x"); c != http.StatusNotFound {
		t.Fatalf("unknown skill: status %d, want 404", c)
	}
	if c := post("skill=plain", "x"); c != http.StatusBadRequest {
		t.Fatalf("missing file: status %d, want 400", c)
	}
	if c := post("skill=plain&file=SKILL.md", ""); c != http.StatusBadRequest {
		t.Fatalf("empty body: status %d, want 400", c)
	}
}

// A web page can POST text/plain to 127.0.0.1 without a preflight; it cannot
// add a custom header, and a browser always names its Origin. Both refused.
func TestMutateRefusesCrossSiteWrites(t *testing.T) {
	srv := startFixture(t)
	const q, body = "skill=plain&file=SKILL.md", "---\nname: plain\ndescription: x\n---\nx\n"
	if c := mutate(t, srv.URL, q, body, nil); c != http.StatusForbidden {
		t.Fatalf("mutate without %s: status %d, want 403", MutateHeader, c)
	}
	if c := mutate(t, srv.URL, q, body, map[string]string{MutateHeader: "nope"}); c != http.StatusForbidden {
		t.Fatalf("mutate with a wrong %s: status %d, want 403", MutateHeader, c)
	}
	if c := mutate(t, srv.URL, q, body, map[string]string{MutateHeader: MutateHeaderValue, "Origin": "https://evil.example"}); c != http.StatusForbidden {
		t.Fatalf("mutate with an Origin header: status %d, want 403", c)
	}
	if c := mutate(t, srv.URL, q, body, map[string]string{MutateHeader: MutateHeaderValue, "Origin": "null"}); c != http.StatusForbidden {
		t.Fatalf("mutate with Origin: null: status %d, want 403", c)
	}
	// The refusals changed nothing.
	got := rpc(t, srv.URL+"/mcp", "skills/get", "skill://plain/SKILL.md")
	if strings.Contains(string(mustJSON(got)), "description: x") {
		t.Fatal("a refused mutate changed the skill")
	}
}

// DNS rebinding: a page on evil.example that resolves to 127.0.0.1 reaches the
// fixture with Host: evil.example:<port>. Every path refuses a foreign Host.
func TestRefusesAForeignHost(t *testing.T) {
	srv := startFixture(t)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, host := range []string{"evil.example:" + port, "evil.example", "localhost:" + port, "127.0.0.1:1"} {
		for _, path := range []string{"/mcp", "/_fixture/mutate?skill=plain&file=SKILL.md"} {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"skills/list"}`))
			req.Host = host
			req.Header.Set(MutateHeader, MutateHeaderValue)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			assert.NoError(t, resp.Body.Close())
			if resp.StatusCode != http.StatusMisdirectedRequest {
				t.Errorf("Host %q %s: status %d, want 421", host, path, resp.StatusCode)
			}
		}
	}
	// The listener's own address is accepted.
	if list := rpc(t, srv.URL+"/mcp", "skills/list", ""); list["result"] == nil {
		t.Fatalf("own Host refused: %v", list)
	}
}

func TestAllowedHosts(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 18931}
	if got := allowedHosts("127.0.0.1:0", addr); len(got) != 1 || got[0] != "127.0.0.1:18931" {
		t.Fatalf("127.0.0.1: %v", got)
	}
	if got := allowedHosts("localhost:18931", addr); len(got) != 2 || got[1] != "localhost:18931" {
		t.Fatalf("localhost: %v", got)
	}
}

func TestRunRefusesANonLoopbackAddress(t *testing.T) {
	if err := run("0.0.0.0:0"); err == nil {
		t.Fatal("want an error for a non-loopback address")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
