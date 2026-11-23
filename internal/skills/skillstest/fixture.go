// Package skillstest is a fake upstream MCP server that serves skills over raw
// JSON-RPC on HTTP. It is not built on mcp-go's server because that server
// answers unknown methods with METHOD_NOT_FOUND and has no hook to add
// skills/list or skills/get.
package skillstest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
)

// Skill describes one skill the fixture serves. Path is the part of the skill
// address after "skill://", for example "pdf" or "acme/billing/refunds". Files
// maps a path inside the skill to its bytes; SKILL.md is generated when absent.
//
// The knobs below make the fixture lie in one specific way so a test can prove
// the consumer notices. They name files by their path inside the skill.
type Skill struct {
	Path  string
	Files map[string][]byte

	// ListedFrontmatter replaces the frontmatter in the listing; SKILL.md keeps
	// its own, so the two disagree.
	ListedFrontmatter map[string]any
	// Dynamic lists resources as the string "dynamic".
	Dynamic bool
	// WrongDigest and WrongSize list a digest or size that the served bytes do
	// not match.
	WrongDigest []string
	WrongSize   []string
	// Binary serves the file as a base64 blob instead of text.
	Binary []string
	// Oversize serves the file with this many extra bytes while the listing
	// keeps the original size.
	Oversize map[string]int
	// ListedSize lists this size for the file, whatever its real length.
	ListedSize map[string]int64
}

// Request is one JSON-RPC message the fixture received.
type Request struct {
	Method string
	// URI is params.uri when present.
	URI string
	// Cursor is params.cursor when present.
	Cursor string
}

// Option configures New.
type Option func(*Fixture)

// WithoutExtension leaves the skills extension out of the initialize result.
func WithoutExtension() Option { return func(f *Fixture) { f.extension = false } }

// WithoutResources leaves the resources capability out of the initialize result.
func WithoutResources() Option { return func(f *Fixture) { f.resources = false } }

// WithPageSize makes skills/list return at most n entries per page.
func WithPageSize(n int) Option { return func(f *Fixture) { f.pageSize = n } }

// Fixture is the fake upstream. It is safe for concurrent use.
type Fixture struct {
	srv *httptest.Server

	extension bool
	resources bool
	pageSize  int

	mu       sync.Mutex
	skills   []*Skill
	raw      []json.RawMessage
	requests []Request
	failList bool
	delay    time.Duration
	// instructions is sent in the initialize result when not empty.
	instructions string
}

// New starts the fixture and registers its shutdown with t.
func New(t testing.TB, opts ...Option) *Fixture {
	t.Helper()
	f := Start(opts...)
	t.Cleanup(f.Close)
	return f
}

// Start starts the fixture on an ephemeral port for use outside go test (the
// cmd/skillsfixture command). The caller owns Close.
func Start(opts ...Option) *Fixture {
	f := NewDetached(opts...)
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// NewDetached builds a fixture without starting a server. Serve it with Handler.
func NewDetached(opts ...Option) *Fixture {
	f := &Fixture{extension: true, resources: true}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Handler is the JSON-RPC endpoint, for serving the fixture on a listener the
// caller owns. It answers every path.
func (f *Fixture) Handler() http.Handler { return http.HandlerFunc(f.handle) }

// Close stops the server started by New or Start; it is a no-op for a fixture
// built with NewDetached.
func (f *Fixture) Close() {
	if f.srv != nil {
		f.srv.Close()
	}
}

// URL is the endpoint to point a streamable-HTTP upstream at.
func (f *Fixture) URL() string {
	if f.srv == nil {
		return ""
	}
	return f.srv.URL
}

// AddSkill registers a skill. Digests and sizes in the listing are computed
// from the current bytes each time, so they are correct unless a knob says
// otherwise.
// AddRawEntry appends one listing entry exactly as given, after the skills
// added with AddSkill. It is how a test lists something AddSkill cannot
// express: a malformed entry, or a second address for a path already listed.
// Nothing is served for it on skills/get or resources/read.
func (f *Fixture) AddRawEntry(entry json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw = append(f.raw, append(json.RawMessage(nil), entry...))
}

func (f *Fixture) AddSkill(s Skill) {
	cp := s
	cp.Files = make(map[string][]byte, len(s.Files)+1)
	for k, v := range s.Files {
		cp.Files[k] = append([]byte(nil), v...)
	}
	if _, ok := cp.Files["SKILL.md"]; !ok {
		name := s.Path[strings.LastIndex(s.Path, "/")+1:]
		cp.Files["SKILL.md"] = []byte("---\nname: " + name + "\ndescription: test skill " + name + "\n---\n# " + name + "\n")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skills = append(f.skills, &cp)
}

// Mutate replaces one file's bytes. The listing follows, so a later skills/list
// reports the new digest and size.
func (f *Fixture) Mutate(path, file string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.skills {
		if s.Path == path {
			s.Files[file] = append([]byte(nil), content...)
			return
		}
	}
	panic("skillstest: Mutate: no skill " + path)
}

// TryMutate is Mutate for callers that cannot panic: it reports an unknown
// skill as an error instead.
func (f *Fixture) TryMutate(path, file string, content []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.skills {
		if s.Path == path {
			s.Files[file] = append([]byte(nil), content...)
			return nil
		}
	}
	return fmt.Errorf("skillstest: no skill %q", path)
}

// SetListFailure makes skills/list answer with a JSON-RPC error.
func (f *Fixture) SetListFailure(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failList = fail
}

// SetInstructions makes the initialize result carry instructions. Set it
// before the consumer connects.
func (f *Fixture) SetInstructions(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instructions = text
}

// SetDelay makes every answer wait d first.
func (f *Fixture) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// Requests returns a copy of every JSON-RPC message received, in order.
func (f *Fixture) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Methods returns the method of every message received, in order.
func (f *Fixture) Methods() []string {
	reqs := f.Requests()
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Method
	}
	return out
}

// Count returns how many messages with this method were received.
func (f *Fixture) Count(method string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == method {
			n++
		}
	}
	return n
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (s *Skill) uri(rel string) string { return "skill://" + s.Path + "/" + rel }

// listedFrontmatter is called with f.mu held.
func (s *Skill) listedFrontmatter() map[string]any {
	if s.ListedFrontmatter != nil {
		return s.ListedFrontmatter
	}
	if fm, err := skills.ParseFrontmatter(s.Files["SKILL.md"]); err == nil {
		return fm
	}
	name := s.Path[strings.LastIndex(s.Path, "/")+1:]
	return map[string]any{"name": name, "description": "test skill " + name}
}

// entry is called with f.mu held.
func (s *Skill) entry() map[string]any {
	var resources any = "dynamic"
	if !s.Dynamic {
		rels := make([]string, 0, len(s.Files))
		for rel := range s.Files {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		refs := make([]map[string]any, 0, len(rels))
		for _, rel := range rels {
			b := s.Files[rel]
			d, size := digest(b), int64(len(b))
			if contains(s.WrongDigest, rel) {
				d = digest([]byte("not the content of " + rel))
			}
			if contains(s.WrongSize, rel) {
				size++
			}
			if v, ok := s.ListedSize[rel]; ok {
				size = v
			}
			refs = append(refs, map[string]any{"uri": s.uri(rel), "digest": d, "size": size})
		}
		resources = refs
	}
	return map[string]any{"uri": s.uri("SKILL.md"), "frontmatter": s.listedFrontmatter(), "resources": resources}
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		URI    string `json:"uri"`
		Cursor string `json:"cursor"`
	} `json:"params"`
}

func (f *Fixture) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	case http.MethodPost:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: req.Method, URI: req.Params.URI, Cursor: req.Params.Cursor})
	delay := f.delay
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}

	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		caps := map[string]any{"tools": map[string]any{}}
		if f.resources {
			caps["resources"] = map[string]any{}
		}
		if f.extension {
			caps["extensions"] = map[string]any{skills.ExtensionKey: map[string]any{}}
		}
		w.Header().Set("Mcp-Session-Id", "skillstest-session")
		result := map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "skillstest", "version": "1.0.0"},
		}
		f.mu.Lock()
		if f.instructions != "" {
			result["instructions"] = f.instructions
		}
		f.mu.Unlock()
		writeResult(w, req.ID, result)
	case "tools/list":
		writeResult(w, req.ID, map[string]any{"tools": []any{map[string]any{
			"name":        "echo",
			"description": "Echo",
			"inputSchema": map[string]any{"type": "object"},
		}}})
	case "skills/list":
		f.serveList(w, req)
	case "skills/get":
		f.serveGet(w, req)
	case "resources/read":
		f.serveRead(w, req)
	default:
		writeError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (f *Fixture) serveList(w http.ResponseWriter, req rpcRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList {
		writeError(w, req.ID, -32603, "skills/list failed on purpose")
		return
	}
	all := make([]any, 0, len(f.skills)+len(f.raw))
	for _, s := range f.skills {
		all = append(all, s.entry())
	}
	for _, r := range f.raw {
		all = append(all, r)
	}
	start := 0
	if req.Params.Cursor != "" {
		n, err := strconv.Atoi(req.Params.Cursor)
		if err != nil || n < 0 || n > len(all) {
			writeError(w, req.ID, -32602, "bad cursor")
			return
		}
		start = n
	}
	end := len(all)
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
	}
	result := map[string]any{"skills": all[start:end]}
	if end < len(all) {
		result["nextCursor"] = strconv.Itoa(end)
	}
	writeResult(w, req.ID, result)
}

func (f *Fixture) serveGet(w http.ResponseWriter, req rpcRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.skills {
		if s.uri("SKILL.md") == req.Params.URI {
			writeResult(w, req.ID, map[string]any{"skill": s.entry()})
			return
		}
	}
	writeError(w, req.ID, -32002, "skill not found: "+req.Params.URI)
}

func (f *Fixture) serveRead(w http.ResponseWriter, req rpcRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.skills {
		for rel, content := range s.Files {
			if s.uri(rel) != req.Params.URI {
				continue
			}
			if n := s.Oversize[rel]; n > 0 {
				content = append(append([]byte(nil), content...), []byte(strings.Repeat("x", n))...)
			}
			item := map[string]any{"uri": req.Params.URI, "mimeType": "text/plain"}
			if contains(s.Binary, rel) {
				item["mimeType"] = "application/octet-stream"
				item["blob"] = base64.StdEncoding.EncodeToString(content)
			} else {
				item["text"] = string(content)
			}
			writeResult(w, req.ID, map[string]any{"contents": []any{item}})
			return
		}
	}
	writeError(w, req.ID, -32002, "resource not found: "+req.Params.URI)
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}
