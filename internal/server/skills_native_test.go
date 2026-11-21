package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-5 (T047, T048, T050): the native skills surface on
// /mcp/s/<server>, driven with a raw JSON-RPC client over HTTP against a real
// Server whose upstream is the skills fixture.

// rawMCP is a minimal Streamable HTTP client: it speaks the legacy handshake
// mcpproxy serves and nothing else, so every check is visible.
type rawMCP struct {
	t   *testing.T
	url string
	sid string
	id  int
}

type rawRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rawReply struct {
	status int
	header http.Header
	body   []byte
	result json.RawMessage
	err    *rawRPCError
}

func newRawMCP(t *testing.T, url string) *rawMCP { return &rawMCP{t: t, url: url} }

// send posts one message with the given session id and protocol header.
func (c *rawMCP) send(sid, proto string, msg map[string]any) rawReply {
	c.t.Helper()
	return c.sendWith(sid, proto, nil, msg)
}

// sendWith is send with extra request headers.
func (c *rawMCP) sendWith(sid, proto string, extra map[string]string, msg map[string]any) rawReply {
	c.t.Helper()
	body, err := json.Marshal(msg)
	require.NoError(c.t, err)
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if proto != "" {
		req.Header.Set("Mcp-Protocol-Version", proto)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer func() { assert.NoError(c.t, resp.Body.Close()) }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	out := rawReply{status: resp.StatusCode, header: resp.Header, body: raw}
	payload := raw
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(string(raw), "\n") {
			if d, ok := strings.CutPrefix(line, "data: "); ok {
				payload = []byte(d)
			}
		}
	}
	var msgOut struct {
		Result json.RawMessage `json:"result"`
		Error  *rawRPCError    `json:"error"`
	}
	if json.Unmarshal(payload, &msgOut) == nil {
		out.result, out.err = msgOut.Result, msgOut.Error
	}
	return out
}

func (c *rawMCP) nextID() int { c.id++; return c.id }

// initialize runs the handshake and keeps the session id.
func (c *rawMCP) initialize() (caps map[string]any, instructions string) {
	c.t.Helper()
	r := c.send("", "", map[string]any{"jsonrpc": "2.0", "id": c.nextID(), "method": "initialize", "params": map[string]any{
		"protocolVersion": mcp.LATEST_LEGACY_PROTOCOL_VERSION, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "native-skills-test", "version": "1"},
	}})
	require.Equal(c.t, http.StatusOK, r.status, string(r.body))
	require.Nil(c.t, r.err, string(r.body))
	c.sid = r.header.Get("Mcp-Session-Id")
	require.NotEmpty(c.t, c.sid)
	var res struct {
		Capabilities map[string]any `json:"capabilities"`
		Instructions string         `json:"instructions"`
	}
	require.NoError(c.t, json.Unmarshal(r.result, &res), string(r.body))
	c.send(c.sid, mcp.LATEST_LEGACY_PROTOCOL_VERSION, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return res.Capabilities, res.Instructions
}

func (c *rawMCP) call(method string, params map[string]any) rawReply {
	c.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "id": c.nextID(), "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(c.sid, mcp.LATEST_LEGACY_PROTOCOL_VERSION, msg)
}

func hasSkillsExtension(caps map[string]any) bool {
	ext, _ := caps["extensions"].(map[string]any)
	_, ok := ext[skills.ExtensionKey]
	return ok
}

type nativeListing struct {
	Skills []struct {
		URI         string         `json:"uri"`
		Frontmatter map[string]any `json:"frontmatter"`
		Resources   []struct {
			URI    string `json:"uri"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"resources"`
	} `json:"skills"`
}

func (g *realSkills) singleURL() string {
	return g.env.baseURL() + "/mcp/s/" + realSkillsSrv
}

func (g *realSkills) nativeRows(t *testing.T) []*storage.ActivityRecord {
	t.Helper()
	f := storage.DefaultActivityFilter()
	f.Types = []string{string(storage.ActivityTypeSkillAccess)}
	rows, _, err := g.rt.StorageManager().ListActivities(f)
	require.NoError(t, err)
	var out []*storage.ActivityRecord
	for _, r := range rows {
		switch r.ToolName {
		case nativeMethodList, nativeMethodGet, nativeMethodRead:
			out = append(out, r)
		}
	}
	return out
}

const nativeUpstreamInstructions = "Prefer the pdf skill for reading documents."

func newNativeSkills(t *testing.T) *realSkills {
	t.Helper()
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.SetInstructions(nativeUpstreamInstructions)
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
			"SKILL.md":     realSkillMD("pdf", "reads pdf files", "Use the script.\n«ünïcødé» line"),
			"scripts/x.py": []byte("print('hello')\n"),
		}})
		fx.AddSkill(skillstest.Skill{Path: "held", Files: map[string][]byte{
			"SKILL.md": realSkillMD("held", "not approved", "HELD-TEXT"),
		}})
	}, nil)
	g.approve(t, "pdf")
	return g
}

// T047: announced, exact listing with upstream addresses, byte-exact reads,
// held skill absent, aggregated endpoints never announce, one activity row per
// access including not-found.
func TestNativeSkills_SingleServerSurface(t *testing.T) {
	g := newNativeSkills(t)
	c := newRawMCP(t, g.singleURL())
	caps, _ := c.initialize()
	require.True(t, hasSkillsExtension(caps), "the extension is announced: %v", caps)
	require.Contains(t, caps, "resources", "the extension requires the resources capability")

	list := c.call(nativeMethodList, map[string]any{})
	require.Nil(t, list.err, string(list.body))
	var listing nativeListing
	require.NoError(t, json.Unmarshal(list.result, &listing), string(list.body))
	require.Len(t, listing.Skills, 1, "exactly the approved skill: %s", list.body)
	entry := listing.Skills[0]
	assert.Equal(t, "skill://pdf/SKILL.md", entry.URI, "the upstream's address")
	assert.Equal(t, "pdf", entry.Frontmatter["name"])
	require.Len(t, entry.Resources, 2)
	assert.NotContains(t, string(list.body), "held")

	for _, res := range entry.Resources {
		read := c.call(nativeMethodRead, map[string]any{"uri": res.URI})
		require.Nil(t, read.err, string(read.body))
		var contents struct {
			Contents []struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"contents"`
		}
		require.NoError(t, json.Unmarshal(read.result, &contents))
		require.Len(t, contents.Contents, 1)
		assert.Equal(t, res.URI, contents.Contents[0].URI)
		data := []byte(contents.Contents[0].Text)
		sum := sha256.Sum256(data)
		assert.Equal(t, res.Digest, "sha256:"+hex.EncodeToString(sum[:]), "byte for byte: %s", res.URI)
		assert.Equal(t, res.Size, int64(len(data)))
	}

	get := c.call(nativeMethodGet, map[string]any{"uri": "skill://pdf/SKILL.md"})
	require.Nil(t, get.err, string(get.body))
	var single struct {
		Skill json.RawMessage `json:"skill"`
	}
	require.NoError(t, json.Unmarshal(get.result, &single), string(get.body))
	var listed struct {
		Skills []json.RawMessage `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(list.result, &listed))
	require.Len(t, listed.Skills, 1)
	assert.JSONEq(t, string(listed.Skills[0]), string(single.Skill), "the single entry is the listed entry")

	// The held skill: absent, and not found by address.
	for _, r := range []rawReply{
		c.call(nativeMethodGet, map[string]any{"uri": "skill://held/SKILL.md"}),
		c.call(nativeMethodRead, map[string]any{"uri": "skill://held/SKILL.md"}),
		c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/nope.md"}),
	} {
		require.NotNil(t, r.err, string(r.body))
		assert.Equal(t, nativeNotFoundCode, r.err.Code)
		assert.Contains(t, r.err.Message, "not found")
		assert.NotContains(t, string(r.body), "HELD-TEXT")
	}

	// Tools still work on the same session.
	tools := c.call("tools/list", nil)
	require.Nil(t, tools.err, string(tools.body))

	// Aggregated endpoints never announce the extension, and do not answer it.
	for _, path := range []string{"/mcp", "/mcp/all"} {
		agg := newRawMCP(t, g.env.baseURL()+path)
		caps, _ := agg.initialize()
		assert.False(t, hasSkillsExtension(caps), "%s must not announce the extension", path)
		r := agg.call(nativeMethodList, map[string]any{})
		require.NotNil(t, r.err, "%s: %s", path, r.body)
		assert.Equal(t, mcp.METHOD_NOT_FOUND, r.err.Code)
	}

	// One row per native access, not-found included: list, 2 reads, get, 3 refusals.
	var rows []*storage.ActivityRecord
	require.Eventually(t, func() bool { rows = g.nativeRows(t); return len(rows) >= 7 }, 10*time.Second, 50*time.Millisecond)
	assert.Len(t, rows, 7)
	outcomes := map[string]int{}
	for _, r := range rows {
		outcomes[r.ToolName+"/"+r.Metadata["outcome"].(string)]++
		assert.Equal(t, realSkillsSrv, r.ServerName)
		assert.Equal(t, c.sid, r.SessionID, "the row carries the caller's session")
	}
	assert.Equal(t, map[string]int{
		"skills/list/success":      1,
		"resources/read/success":   2,
		"skills/get/success":       1,
		"skills/get/not_found":     1,
		"resources/read/not_found": 2,
	}, outcomes)
}

// T048: each extension method gets exactly the refusal tools/call gets for a
// missing session id, a foreign one and an unsupported protocol version.
func TestNativeSkills_RefusalParityWithToolsCall(t *testing.T) {
	g := newNativeSkills(t)
	c := newRawMCP(t, g.singleURL())
	caps, _ := c.initialize()
	require.True(t, hasSkillsExtension(caps))

	other := newSingleServerSessionIDs("other", 0).Generate()
	stale := newSingleServerSessionIDs(realSkillsSrv, 7).Generate()
	cases := []struct {
		name, sid, proto string
		meta             bool
	}{
		{"missing session id", "", mcp.LATEST_LEGACY_PROTOCOL_VERSION, false},
		{"foreign session id", other, mcp.LATEST_LEGACY_PROTOCOL_VERSION, false},
		{"session id of another generation", stale, mcp.LATEST_LEGACY_PROTOCOL_VERSION, false},
		{"malformed session id", "mcp-session-nope", mcp.LATEST_LEGACY_PROTOCOL_VERSION, false},
		{"unsupported protocol version", c.sid, mcp.ProtocolVersion20260728, true},
		{"protocol version header without meta", c.sid, mcp.ProtocolVersion20260728, false},
	}
	// The modern case is otherwise a valid modern request (Mcp-Method agrees
	// with the body), so the refusal is the one for the version itself.
	wantCode := map[string]int{"unsupported protocol version": mcp.UNSUPPORTED_PROTOCOL_VERSION}
	methods := map[string]map[string]any{
		"tools/call":     {"name": "echo", "arguments": map[string]any{}},
		nativeMethodList: {},
		nativeMethodGet:  {"uri": "skill://pdf/SKILL.md"},
		nativeMethodRead: {"uri": "skill://pdf/SKILL.md"},
	}
	before := len(g.nativeRows(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want rawReply
			for _, m := range []string{"tools/call", nativeMethodList, nativeMethodGet, nativeMethodRead} {
				params := map[string]any{}
				for k, v := range methods[m] {
					params[k] = v
				}
				if tc.meta {
					params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": tc.proto}
				}
				var extra map[string]string
				if tc.meta {
					extra = map[string]string{mcp.HeaderMethod: m}
				}
				got := c.sendWith(tc.sid, tc.proto, extra, map[string]any{"jsonrpc": "2.0", "id": 99, "method": m, "params": params})
				assert.GreaterOrEqual(t, got.status, 400, "%s is refused: %s", m, got.body)
				if code, ok := wantCode[tc.name]; ok {
					require.NotNil(t, got.err, "%s: %s", m, got.body)
					assert.Equal(t, code, got.err.Code, "%s: refused for the version: %s", m, got.body)
				}
				if m == "tools/call" {
					want = got
					continue
				}
				assert.Equal(t, want.status, got.status, "%s status like tools/call", m)
				assert.Equal(t, strings.ReplaceAll(string(want.body), "tools/call", "ping"),
					strings.ReplaceAll(string(got.body), m, "ping"), "%s body like tools/call", m)
				assert.NotContains(t, string(got.body), "skill://pdf", "a refusal carries no skill")
			}
		})
	}
	assert.Equal(t, before, len(g.nativeRows(t)), "a refused request reads nothing and records no skill access")
}

// openStream opens the legacy standalone GET stream of a session and reports
// on the returned channel when the server closes it.
func openStream(t *testing.T, url, sid string) (closed <-chan struct{}, cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	req.Header.Set("Mcp-Protocol-Version", mcp.LATEST_LEGACY_PROTOCOL_VERSION)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"),
		"a listening stream: %s", resp.Header.Get("Content-Type"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		r := bufio.NewReader(resp.Body)
		for {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
		t.Fatalf("the stream closed by itself")
	case <-time.After(300 * time.Millisecond):
	}
	return done, stop
}

// T050: off -> on -> off on one endpoint. At each step the new session sees
// the right capabilities, the previous step's session is refused, its stream
// is closed, and transports do not accumulate.
func TestNativeSkills_EndpointFollowsTheSwitch(t *testing.T) {
	g := newNativeSkills(t)
	url := g.singleURL()

	type step struct {
		name   string
		enable bool
	}
	var prev *rawMCP
	var prevClosed <-chan struct{}
	for i, st := range []step{{"on", true}, {"off", false}, {"on again", true}, {"off again", false}} {
		if i > 0 {
			g.commitGlobal(t, func(c *config.Config) { c.EnableSkills = st.enable })
			if i%2 == 0 {
				// Every other step through the reload hook, the others lazily on
				// the next request: both paths must retire.
				g.p.RefreshSkillsAvailability()
			}
		}
		c := newRawMCP(t, url)
		caps, _ := c.initialize()
		assert.Equal(t, st.enable, hasSkillsExtension(caps), "step %s: announcement", st.name)
		list := c.call(nativeMethodList, map[string]any{})
		if st.enable {
			require.Nil(t, list.err, "step %s: %s", st.name, list.body)
			assert.Contains(t, string(list.result), "skill://pdf/SKILL.md")
			read := c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/scripts/x.py"})
			require.Nil(t, read.err, string(read.body))
			assert.Contains(t, string(read.result), "print('hello')")
		} else {
			require.NotNil(t, list.err, "step %s: %s", st.name, list.body)
			assert.Equal(t, mcp.METHOD_NOT_FOUND, list.err.Code, "off: the method is unknown")
		}

		if prev != nil {
			old := prev.call("tools/list", nil)
			assert.Equal(t, http.StatusNotFound, old.status, "step %s: the previous session is refused: %s", st.name, old.body)
			select {
			case <-prevClosed:
			case <-time.After(10 * time.Second):
				t.Fatalf("step %s: the previous step's stream is still open", st.name)
			}
		}
		assert.LessOrEqual(t, g.p.singleServerTransportCount(), 1, "step %s: transports do not accumulate", st.name)

		var cancel func()
		prevClosed, cancel = openStream(t, url, c.sid)
		t.Cleanup(cancel)
		prev = c
	}
}

// T050: acquisition racing retirement never leaves a transport behind its
// instance.
func TestNativeSkills_AcquireRacesRetire(t *testing.T) {
	g := newNativeSkills(t)
	newTransport := func(srv *mcpserver.MCPServer, gen uint64) *mcpserver.StreamableHTTPServer {
		return mcpserver.NewStreamableHTTPServer(srv, singleServerStreamableOptions(nil, realSkillsSrv, gen)...)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mu sync.Mutex
	var acquired []*singleServerEndpoint
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ep, ok := g.p.acquireSingleServerEndpoint(realSkillsSrv, newTransport)
				if ok {
					mu.Lock()
					acquired = append(acquired, ep)
					mu.Unlock()
				}
			}
		}()
	}
	countAcquired := func() int { mu.Lock(); defer mu.Unlock(); return len(acquired) }
	require.Eventually(t, func() bool { return countAcquired() > 0 }, 10*time.Second, time.Millisecond,
		"the workers acquire before the toggling starts")
	on := true
	for i := 0; i < 40; i++ {
		on = !on
		g.commitGlobal(t, func(c *config.Config) { c.EnableSkills = on })
		before := countAcquired()
		g.p.retireStaleNativeSurfaces()
		require.Eventually(t, func() bool { return countAcquired() > before }, 10*time.Second, time.Millisecond,
			"acquisitions keep running between retirements")
	}
	close(stop)
	wg.Wait()

	require.LessOrEqual(t, g.p.singleServerTransportCount(), 1)
	g.p.singleServersMu.Lock()
	live := g.p.singleServers[realSkillsSrv]
	g.p.singleServersMu.Unlock()
	retiredSeen := 0
	for _, ep := range acquired {
		if live != nil && ep.server == live.server {
			assert.Same(t, live.transport, ep.transport, "the live instance has exactly one transport")
			continue
		}
		select {
		case <-ep.retired:
			retiredSeen++
		default:
			t.Fatalf("an acquired endpoint that is not live was never retired")
		}
	}
	assert.Positive(t, retiredSeen, "endpoints were acquired and then retired")
}

// T050: an initialize that acquired its endpoint before a retirement answers
// with that upstream's instructions, never the aggregate ones.
func TestNativeSkills_PausedInitializeKeepsItsUpstream(t *testing.T) {
	g := newNativeSkills(t)
	url := g.singleURL()

	// The upstream's own instructions reach a client of the endpoint.
	plain := newRawMCP(t, url)
	_, instr := plain.initialize()
	require.Equal(t, nativeUpstreamInstructions, instr)

	entered, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	hook := func(string) {
		paused := false
		pauseOnce.Do(func() { paused = true })
		if paused {
			close(entered)
			<-release
		}
	}
	g.env.proxyServer.singleServerAcquired.Store(&hook)
	t.Cleanup(func() { g.env.proxyServer.singleServerAcquired.Store(nil) })

	type initOut struct {
		ok           bool
		caps         map[string]any
		instructions string
	}
	out := make(chan initOut, 1)
	go func() {
		res := initOut{}
		// require in this goroutine ends it with Goexit: still report.
		defer func() { out <- res }()
		c := newRawMCP(t, url)
		res.caps, res.instructions = c.initialize()
		res.ok = true
	}()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the initialize never reached the endpoint")
	}
	g.commitGlobal(t, func(c *config.Config) { c.EnableSkills = false })
	g.p.RefreshSkillsAvailability()
	g.p.singleServersMu.Lock()
	_, cached := g.p.singleServers[realSkillsSrv]
	g.p.singleServersMu.Unlock()
	assert.False(t, cached, "the endpoint was retired while the initialize was paused")
	releaseAll()

	var got initOut
	select {
	case got = <-out:
	case <-time.After(30 * time.Second):
		t.Fatal("the paused initialize never answered")
	}
	require.True(t, got.ok, "the paused initialize failed")
	assert.True(t, hasSkillsExtension(got.caps), "served by the instance it acquired")
	assert.Equal(t, nativeUpstreamInstructions, got.instructions, "the upstream's instructions, never the aggregate ones")
}

// An excluded server (expose_skills: false) does not announce the extension;
// a skill changed upstream is withdrawn natively until it is approved again,
// and its new bytes are never served (SC-002).
func TestNativeSkills_ExcludedAndChangedSkills(t *testing.T) {
	g := newNativeSkills(t)
	url := g.singleURL()

	g.fx.Mutate("pdf", "scripts/x.py", []byte("print('CHANGED')\n"))
	g.refreshUntil(t, func() bool { return g.rec(t, "pdf").Status == storage.SkillStatusChanged })
	c := newRawMCP(t, url)
	caps, _ := c.initialize()
	require.True(t, hasSkillsExtension(caps))
	list := c.call(nativeMethodList, map[string]any{})
	require.Nil(t, list.err, string(list.body))
	assert.JSONEq(t, `{"skills":[]}`, string(list.result), "a changed skill is not listed")
	read := c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/scripts/x.py"})
	require.NotNil(t, read.err)
	assert.Equal(t, nativeNotFoundCode, read.err.Code)
	assert.NotContains(t, string(read.body), "CHANGED")

	g.approve(t, "pdf")
	read = c.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/scripts/x.py"})
	require.Nil(t, read.err, string(read.body))
	assert.Contains(t, string(read.result), "CHANGED", "served again once the change is approved")

	exclude := false
	g.commitServer(t, func(sc *config.ServerConfig) { sc.ExposeSkills = &exclude })
	c2 := newRawMCP(t, url)
	caps, _ = c2.initialize()
	assert.False(t, hasSkillsExtension(caps), "an excluded server does not announce the extension")
	r := c2.call(nativeMethodRead, map[string]any{"uri": "skill://pdf/scripts/x.py"})
	require.NotNil(t, r.err)
	assert.Equal(t, mcp.METHOD_NOT_FOUND, r.err.Code)
}

// Native answers are never rewritten: under a policy that strips control
// characters, a skill whose decoded frontmatter carries one is withheld from
// the listing (the stored JSON escapes it, so the check must decode), and a
// file the policy would change is refused rather than served changed.
func TestNativeSkills_PolicyWithholdsInsteadOfRewriting(t *testing.T) {
	strip := &config.OutputSanitisationConfig{ResponseAction: "spotlight", StripControlChars: true,
		StripClasses: []string{"ansi", "c0c1"}, MaxRedactions: 100}

	t.Run("frontmatter is checked decoded", func(t *testing.T) {
		fx := newSkillFixture(t)
		fx.proxy.config.OutputSanitisation = strip
		clean := fx.proxy.skillWordCleaner(fx.proxy.skillPolicy())
		sk := &runtime.AvailableSkill{
			URI: "skill://a/SKILL.md", Path: "a",
			Frontmatter: json.RawMessage(`{"name":"a","note":"\u001b[31mRED\u001b[0m"}`),
		}
		assert.Nil(t, nativeEntryOf(clean, sk), "an escaped ESC in a frontmatter value withholds the skill")
		sk.Frontmatter = json.RawMessage(`{"name":"a","\u001bkey":"v"}`)
		assert.Nil(t, nativeEntryOf(clean, sk), "and in a key")
		sk.Frontmatter = json.RawMessage(`{"name":"a","tags":["ok","\u001b[1m"]}`)
		assert.Nil(t, nativeEntryOf(clean, sk), "and in a list")
		sk.Frontmatter = json.RawMessage(`{"name":"a","description":"plain"}`)
		assert.NotNil(t, nativeEntryOf(clean, sk), "clean frontmatter is listed")
	})

	t.Run("a file the policy would change is refused", func(t *testing.T) {
		g := newRealSkills(t, func(fx *skillstest.Fixture) {
			fx.AddSkill(skillstest.Skill{Path: "esc", Files: map[string][]byte{
				"SKILL.md": realSkillMD("esc", "has escapes", "plain"),
				"ref.txt":  []byte("before \x1b[31mRED\x1b[0m after\n"),
			}})
		}, nil)
		g.approve(t, "esc")
		g.commitGlobal(t, func(c *config.Config) { c.OutputSanitisation = strip })
		c := newRawMCP(t, g.singleURL())
		c.initialize()
		read := c.call(nativeMethodRead, map[string]any{"uri": "skill://esc/ref.txt"})
		require.NotNil(t, read.err, string(read.body))
		assert.Contains(t, read.err.Message, "blocked by the output sanitisation policy")
		assert.NotContains(t, string(read.body), "RED")
		ok := c.call(nativeMethodRead, map[string]any{"uri": "skill://esc/SKILL.md"})
		require.Nil(t, ok.err, "a file the policy leaves alone is served: %s", ok.body)
	})
}
