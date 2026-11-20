package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Spec 125 PR-5 (plan D14, FR-029, FR-029a, FR-030): the skills extension,
// natively, on /mcp/s/<server> only.
//
// A client that speaks io.modelcontextprotocol/skills connects to the
// single-server endpoint of one upstream and is told the extension is there.
// It lists that server's AVAILABLE skills with the addresses the upstream
// uses, gets one entry, and reads files with resources/read. Everything comes
// from the approved copy, byte for byte: the client verifies the digests
// itself, so nothing is rewritten and no header is added. What the output
// policy would change is withheld instead of being served changed.
//
// mcp-go has no hook for a method it does not know, so the three methods are
// answered by an interceptor in front of the instance's transport. Before it
// answers, the request must pass every check the transport applies to a
// tools/call: the interceptor sends the same request, with its method
// replaced by "ping", through the transport itself and answers only when the
// transport accepted it. A refusal (wrong content type, missing, foreign or
// stale session id, unsupported protocol version, bad JSON-RPC version) is
// the transport's own answer, passed on unchanged. The checks are therefore
// the transport's, not a copy of them that could drift.

const (
	nativeMethodList = "skills/list"
	nativeMethodGet  = "skills/get"
	nativeMethodRead = "resources/read"

	// nativeNotFoundCode is the code the extension's servers answer a missing
	// skill or file with (the resource-not-found code).
	nativeNotFoundCode = -32002
	// maxNativeRequestBytes bounds the body the interceptor reads. The three
	// requests carry one URI or cursor; anything larger is not one of them and
	// is left to the transport.
	maxNativeRequestBytes = 64 << 10
)

// nativeSkillsServerOptions are the options a single-server instance with the
// native surface is built with: the extension and the resources capability
// it requires.
func nativeSkillsServerOptions() []mcpserver.ServerOption {
	return []mcpserver.ServerOption{
		mcpserver.WithResourceCapabilities(false, false),
		mcpserver.WithExtensions(map[string]any{skills.ExtensionKey: map[string]any{}}),
	}
}

// singleServerWantsNative reports whether /mcp/s/<name> should carry the
// native surface now: the feature is on and the server is not excluded
// (expose_skills: false). Whether a skill is available is decided per
// request, so a quarantined or disabled server keeps the announcement and
// lists nothing.
func (p *MCPProxyServer) singleServerWantsNative(name string) bool {
	cfg := p.currentConfig()
	if cfg == nil || !cfg.EnableSkills || p.skills() == nil {
		return false
	}
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc.ExposeSkills == nil || *sc.ExposeSkills
		}
	}
	return false
}

// nativeSkillsRequest is the part of a JSON-RPC request the interceptor reads.
type nativeSkillsRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// parseNativeSkillsRequest returns the request when body is one JSON-RPC
// request (not a notification, not a batch) for one of the three methods.
func parseNativeSkillsRequest(body []byte) (*nativeSkillsRequest, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var req nativeSkillsRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return nil, false
	}
	switch req.Method {
	case nativeMethodList, nativeMethodGet, nativeMethodRead:
	default:
		return nil, false
	}
	if id := bytes.TrimSpace(req.ID); len(id) == 0 || bytes.Equal(id, []byte("null")) {
		return nil, false
	}
	return &req, true
}

// pingProbeBody is body with its method replaced by "ping"; everything else
// (jsonrpc, id, params with _meta) is kept, so the transport decides the era
// and the checks exactly as it would for the real request.
func pingProbeBody(body []byte) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj["method"] = json.RawMessage(`"` + string(mcp.MethodPing) + `"`)
	return json.Marshal(obj)
}

// bufferedResponse records the transport's answer to the probe.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: http.Header{}}
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

// accepted reports whether the transport answered the probe with a ping
// result: every check passed.
func (b *bufferedResponse) accepted() bool {
	if b.status != http.StatusOK {
		return false
	}
	var msg struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b.body.Bytes(), &msg); err != nil {
		return false
	}
	return len(msg.Result) > 0 && len(msg.Error) == 0
}

// replay writes the recorded answer unchanged.
func (b *bufferedResponse) replay(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(b.body.Bytes())
}

// nativeSkillsInterceptor answers skills/list, skills/get and resources/read
// on a single-server endpoint built with the native surface, after the
// transport (next) has accepted the request's ping probe. Every other request
// goes to next untouched.
func (p *MCPProxyServer) nativeSkillsInterceptor(ep *singleServerEndpoint, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxNativeRequestBytes+1))
		if err != nil || len(body) > maxNativeRequestBytes {
			// Not one of ours (or unreadable): hand the transport the whole body.
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		req, ok := parseNativeSkillsRequest(body)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		probe, err := pingProbeBody(body)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		pr := r.Clone(r.Context())
		pr.Body = io.NopCloser(bytes.NewReader(probe))
		pr.ContentLength = int64(len(probe))
		pr.Header = r.Header.Clone()
		// A modern request must name its method in Mcp-Method; keep a header
		// that agreed with the body agreeing with the probe, and leave a
		// disagreeing one as it is so the transport refuses it.
		if pr.Header.Get(mcp.HeaderMethod) == req.Method {
			pr.Header.Set(mcp.HeaderMethod, string(mcp.MethodPing))
		}
		rec := newBufferedResponse()
		next.ServeHTTP(rec, pr)
		if !rec.accepted() {
			rec.replay(w)
			return
		}
		for k, v := range rec.header {
			if k == "Content-Length" || k == "Content-Type" {
				continue
			}
			w.Header()[k] = v
		}
		p.answerNativeSkills(w, r, ep, req)
	})
}

// nativeAnswer is one JSON-RPC answer: a result or an error.
type nativeAnswer struct {
	result any
	code   int
	msg    string
}

func writeNativeAnswer(w http.ResponseWriter, id json.RawMessage, a nativeAnswer) {
	out := map[string]any{"jsonrpc": mcp.JSONRPC_VERSION, "id": id}
	if a.code != 0 {
		out["error"] = map[string]any{"code": a.code, "message": a.msg}
	} else {
		out["result"] = a.result
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

// nativeSkillEntry is one entry of the extension's listing.
type nativeSkillEntry struct {
	URI         string           `json:"uri"`
	Frontmatter json.RawMessage  `json:"frontmatter"`
	Resources   []skills.FileRef `json:"resources"`
}

// nativeEntryOf renders an available skill as the extension lists it, or nil
// when the output policy would change any upstream-written part of it (the
// address, a file address, the frontmatter): native answers are never
// rewritten, so such a skill is withheld. Only stored text files are listed,
// because only those are served.
func nativeEntryOf(clean func(string) string, sk *runtime.AvailableSkill) *nativeSkillEntry {
	if sk == nil || sk.URI == "" || clean(sk.URI) != sk.URI {
		return nil
	}
	fm := sk.Frontmatter
	if len(bytes.TrimSpace(fm)) == 0 {
		fm = json.RawMessage(`{}`)
	}
	if clean(string(fm)) != string(fm) {
		return nil
	}
	e := &nativeSkillEntry{URI: sk.URI, Frontmatter: fm, Resources: []skills.FileRef{}}
	for _, f := range sk.Files {
		if !f.Text || !f.Stored || f.URI == "" {
			continue
		}
		if clean(f.URI) != f.URI {
			return nil
		}
		e.Resources = append(e.Resources, skills.FileRef{URI: f.URI, Digest: f.Digest, Size: f.Size})
	}
	return e
}

// answerNativeSkills answers one of the three methods for the endpoint's
// upstream and records one skill_access row, whatever the outcome.
func (p *MCPProxyServer) answerNativeSkills(w http.ResponseWriter, r *http.Request, ep *singleServerEndpoint, req *nativeSkillsRequest) {
	ctx := r.Context()
	p.recordMCPSurface()
	start := time.Now()
	sessionID := r.Header.Get(mcpserver.HeaderKeySessionID)
	requestID := mintCorrelationID(req.Method)

	act := runtime.SkillAccessActivity{
		Source: string(storage.ActivitySourceMCP), Tool: req.Method, Server: ep.name,
		SessionID: sessionID, RequestID: requestID,
	}
	switch req.Method {
	case nativeMethodList:
		act.Action = runtime.SkillActionList
	case nativeMethodGet:
		act.Action = runtime.SkillActionLoad
	default:
		act.Action = runtime.SkillActionRead
	}

	a := p.nativeSkillsAnswer(ctx, ep, req, &act)
	act.DurationMs = time.Since(start).Milliseconds()
	p.emitSkillAccess(ctx, act)
	writeNativeAnswer(w, req.ID, a)
}

func (p *MCPProxyServer) nativeSkillsAnswer(ctx context.Context, ep *singleServerEndpoint, req *nativeSkillsRequest, act *runtime.SkillAccessActivity) nativeAnswer {
	var params struct {
		Cursor *string `json:"cursor"`
		URI    *string `json:"uri"`
	}
	if len(bytes.TrimSpace(req.Params)) > 0 && !bytes.Equal(bytes.TrimSpace(req.Params), []byte("null")) {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			act.Outcome = runtime.SkillAccessInvalid
			return nativeAnswer{code: mcp.INVALID_PARAMS, msg: "invalid params"}
		}
	}
	uri := ""
	if req.Method != nativeMethodList {
		if params.URI == nil || *params.URI == "" {
			act.Outcome = runtime.SkillAccessInvalid
			return nativeAnswer{code: mcp.INVALID_PARAMS, msg: "missing required parameter: uri"}
		}
		uri = *params.URI
		act.SkillID = cutRunes(echoSkillID(uri), skillIDEchoMax)
	} else if params.Cursor != nil && *params.Cursor != "" {
		// The whole listing is one page; no cursor was ever issued.
		act.Outcome = runtime.SkillAccessInvalid
		return nativeAnswer{code: mcp.INVALID_PARAMS, msg: "invalid cursor"}
	}

	notFound := func() nativeAnswer {
		act.Outcome = runtime.SkillAccessNotFound
		what := "skill"
		if req.Method == nativeMethodRead {
			what = "resource"
		}
		return nativeAnswer{code: nativeNotFoundCode, msg: what + " not found: " + echoSkillID(uri)}
	}

	// The door admitted the caller; the answer is decided on the live state.
	cat := p.skills()
	if cat == nil || !p.skillsEnabled() || !p.singleServerAdmits(ctx, ep.name) {
		if req.Method == nativeMethodList {
			act.Outcome = runtime.SkillAccessSuccess
			return nativeAnswer{result: map[string]any{"skills": []any{}}}
		}
		return notFound()
	}
	list, err := cat.ListAvailableSkills(ep.name)
	if err != nil {
		p.logger.Warn("native skills listing failed", zap.String("server", ep.name), zap.Error(err))
		act.Outcome = runtime.SkillAccessError
		return nativeAnswer{code: mcp.INTERNAL_ERROR, msg: "skills are unavailable"}
	}
	pol := p.skillPolicy()
	clean := p.skillWordCleaner(pol)

	switch req.Method {
	case nativeMethodList:
		entries := make([]*nativeSkillEntry, 0, len(list))
		for _, sk := range list {
			if e := nativeEntryOf(clean, sk); e != nil {
				entries = append(entries, e)
			}
		}
		act.Outcome = runtime.SkillAccessSuccess
		act.Results = len(entries)
		act.Matches = len(entries)
		return nativeAnswer{result: map[string]any{"skills": entries}}

	case nativeMethodGet:
		for _, sk := range list {
			if sk.URI != uri {
				continue
			}
			e := nativeEntryOf(clean, sk)
			if e == nil {
				break
			}
			act.SkillID = sk.ID
			act.SkillName = oneLine(sk.Name, skillNameMax)
			act.Hash = sk.ApprovedHash
			act.TotalSize = sk.TotalSize
			act.Outcome = runtime.SkillAccessSuccess
			return nativeAnswer{result: map[string]any{"skill": e}}
		}
		return notFound()
	}

	// resources/read: the address must be a stored text file of an available
	// skill of this server, listed under this exact address.
	for _, sk := range list {
		for _, f := range sk.Files {
			if f.URI != uri {
				continue
			}
			if nativeEntryOf(clean, sk) == nil {
				return notFound()
			}
			act.SkillID = sk.ID
			act.SkillName = oneLine(sk.Name, skillNameMax)
			act.File = cutRunes(echoSkillID(f.RelPath), skillFileNameMax)
			if !f.Text || !f.Stored {
				act.Outcome = runtime.SkillAccessBinary
				return nativeAnswer{code: nativeNotFoundCode, msg: "binary files are not served: " + echoSkillID(uri)}
			}
			return p.nativeReadFile(ctx, ep, cat, pol, sk, f, uri, act, notFound)
		}
	}
	return notFound()
}

// nativeReadFile reads one approved file and answers it byte for byte.
func (p *MCPProxyServer) nativeReadFile(ctx context.Context, ep *singleServerEndpoint, cat skillsCatalog, pol skillOutputPolicy,
	sk *runtime.AvailableSkill, f storage.SkillFile, uri string, act *runtime.SkillAccessActivity, notFound func() nativeAnswer) nativeAnswer {
	data, meta, got, err := cat.AvailableSkillFile(ep.name, sk.Path, f.RelPath)
	switch {
	case err == nil && got != nil:
	case errors.Is(err, runtime.ErrSkillFileBinary):
		act.Outcome = runtime.SkillAccessBinary
		return nativeAnswer{code: nativeNotFoundCode, msg: "binary files are not served: " + echoSkillID(uri)}
	case errors.Is(err, runtime.ErrSkillStoredCopyDamaged):
		ans := notFound()
		act.Outcome = runtime.SkillAccessDamaged
		return ans
	default:
		if err != nil && !errors.Is(err, runtime.ErrSkillNotFound) {
			p.logger.Warn("native skill read failed", zap.String("server", ep.name), zap.Error(err))
		}
		return notFound()
	}
	// The listing the client holds names this digest; a skill approved again
	// in between has other files, and the old address answers not found.
	if meta.Digest != "" && meta.Digest != f.Digest {
		return notFound()
	}
	act.Hash = got.ApprovedHash
	act.TotalSize = int64(len(data))

	// Native reads are never rewritten (FR-029a): what the policy would block
	// or change is withheld.
	served := p.servedSkillText(pol, ep.name, meta.Digest, data)
	if served.blocked || served.text != nil {
		reason := served.reason
		if reason == "" {
			reason = "native skill reads are served unchanged or not at all"
		}
		p.emitActivityPolicyDecision(ctx, ep.name, nativeMethodRead, act.SessionID, act.RequestID,
			"blocked", reason, telemetry.BlockReasonOutputSanitisation)
		act.Outcome = runtime.SkillAccessBlocked
		return nativeAnswer{code: mcp.INTERNAL_ERROR, msg: "skill output blocked by the output sanitisation policy"}
	}

	text := string(data)
	act.Bytes = int64(len(data))
	// Sensitive-data detection records findings on what was delivered; the
	// bytes are not altered.
	act.DetectionText = text
	act.Outcome = runtime.SkillAccessSuccess
	item := map[string]any{"uri": uri, "text": text}
	if mt := nativeMimeType(f.RelPath); mt != "" {
		item["mimeType"] = mt
	}
	return nativeAnswer{result: map[string]any{"contents": []any{item}}}
}

// nativeMimeType is the media type answered for a text file, by extension.
func nativeMimeType(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".md"):
		return "text/markdown"
	default:
		return "text/plain"
	}
}
