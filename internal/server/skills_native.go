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
	"unicode"

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
// replaced by "ping" ("server/discover" for a modern request, where ping was
// removed), through the transport itself and answers only when the transport
// accepted it. The Mcp-Method and Mcp-Name headers are checked first against
// the original method and params with the SDK's own rule, and a mismatch is
// answered as the SDK answers it, after the transport's earlier checks. A refusal (wrong content type, missing, foreign or
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

	raw []byte // the whole body, as received
}

// parseNativeSkillsRequest returns the request when body is one JSON-RPC
// request (not a notification, not a batch) for one of the three methods.
// A body that repeats a member at the top level or directly in params is
// left to the transport: the SDK decodes repeats into struct fields (merging
// a repeated "params", stopping at a malformed "_meta" even when a later one
// is valid), while a raw read keeps only the last, so the two could disagree
// on the request's protocol era or params.
func parseNativeSkillsRequest(body []byte) (*nativeSkillsRequest, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || hasRepeatedTopLevelMember(trimmed) {
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
	if p := bytes.TrimSpace(req.Params); len(p) > 0 && p[0] == '{' && hasRepeatedTopLevelMember(p) {
		return nil, false
	}
	req.raw = trimmed
	return &req, true
}

// hasRepeatedTopLevelMember reports whether the JSON object obj names one of
// its members twice (or does not tokenize as an object). Names are compared
// the way encoding/json matches them to struct fields, case-insensitively, so
// "jsonrpc" and "JSONRPC" are the same member.
func hasRepeatedTopLevelMember(obj []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return true
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return true
		}
		key, ok := tok.(string)
		if !ok || seen[foldMemberName(key)] {
			return true
		}
		seen[foldMemberName(key)] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return true
		}
	}
	return false
}

// foldMemberName maps every rune to the smallest rune of its simple case
// folding orbit, so two names encoding/json would match to the same field
// (strings.EqualFold) map to the same key.
func foldMemberName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < least {
				least = f
			}
		}
		b.WriteRune(least)
	}
	return b.String()
}

// probeMeta is params._meta as the SDK decodes it (a struct field, matched
// case-insensitively, last occurrence wins because mcp.Meta replaces itself),
// when it decodes as mcp.Meta.
func probeMeta(params json.RawMessage) (json.RawMessage, bool) {
	if t := bytes.TrimSpace(params); len(t) == 0 || t[0] != '{' {
		return nil, false
	}
	var p struct {
		Meta json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	if t := bytes.TrimSpace(p.Meta); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil, false
	}
	if err := json.Unmarshal(p.Meta, new(mcp.Meta)); err != nil {
		return nil, false
	}
	return p.Meta, true
}

// probeMethod is the side-effect-free method the probe carries: ping for the
// legacy era, server/discover for a request whose _meta names a modern
// protocol version (2026-07-28 removed ping; the SDK refuses it there). The
// era is read the way the SDK reads it, from params._meta.
func probeMethod(req *nativeSkillsRequest) mcp.MCPMethod {
	var params struct {
		Meta *mcp.Meta `json:"_meta"`
	}
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &params) == nil &&
		mcp.IsModernProtocol(params.Meta.ProtocolVersion()) {
		return mcp.MethodServerDiscover
	}
	return mcp.MethodPing
}

// probeBody is body with its method replaced by method; everything else
// (jsonrpc, id, params with _meta) is kept, so the transport decides the era
// and the checks exactly as it would for the real request.
func probeBody(body []byte, method mcp.MCPMethod) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	// Members are matched as encoding/json matches them (no name repeats,
	// case-insensitively: parseNativeSkillsRequest refused any repeat).
	var params json.RawMessage
	for k, raw := range obj {
		switch {
		case strings.EqualFold(k, "method"):
			delete(obj, k)
		case strings.EqualFold(k, "params"):
			params = raw
			delete(obj, k)
		}
	}
	obj["method"] = json.RawMessage(`"` + string(method) + `"`)
	// The probe's params are the request's _meta and nothing else: _meta is
	// all the transport's checks read (era, protocol version, client
	// capabilities); the rest is the answer's to judge, not the probe
	// method's parser. A _meta the SDK cannot decode makes the SDK read the
	// request as legacy, as a probe without one is read.
	if meta, ok := probeMeta(params); ok {
		probeParams, err := json.Marshal(map[string]json.RawMessage{"_meta": meta})
		if err != nil {
			return nil, err
		}
		obj["params"] = probeParams
	}
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

// accepted reports whether the transport answered the probe with a result:
// every check passed.
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

// refusedWith reports whether the recorded answer is a JSON-RPC error with
// code and message.
func (b *bufferedResponse) refusedWith(code int, message string) bool {
	var msg struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b.body.Bytes(), &msg); err != nil || msg.Error == nil {
		return false
	}
	return msg.Error.Code == code && msg.Error.Message == message
}

// copyProbeHeaders passes on the headers the transport set on the probe's
// answer, except those of its body.
func copyProbeHeaders(w http.ResponseWriter, rec *bufferedResponse) {
	for k, v := range rec.header {
		if k == "Content-Length" || k == "Content-Type" {
			continue
		}
		w.Header()[k] = v
	}
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
// transport (next) has accepted the request's ping probe. Every other request,
// including a resources/read of an address that is not a skill's, goes to next
// untouched.
func (p *MCPProxyServer) nativeSkillsInterceptor(ep *singleServerEndpoint, next http.Handler) http.Handler {
	claims := func(req *nativeSkillsRequest) bool {
		return nativeClaims(req, func(uri string) bool { return p.nativeKnowsFileURI(ep.name, uri) })
	}
	answer := func(w http.ResponseWriter, r *http.Request, req *nativeSkillsRequest) {
		p.answerNativeSkills(w, r, ep, req)
	}
	return nativeProbeGate(next, claims, answer)
}

// nativeClaims reports whether the native surface answers req. skills/list
// and skills/get always (the SDK knows neither). A resources/read only when
// the SDK itself would parse it (mcp.ReadResourceRequest, so a request the
// SDK refuses as unparsable keeps the SDK's refusal) and its uri is a skill
// address (nativeReadIsSkillAddress).
func nativeClaims(req *nativeSkillsRequest, known func(string) bool) bool {
	if req.Method != nativeMethodRead {
		return true
	}
	var sdkReq mcp.ReadResourceRequest
	if err := json.Unmarshal(req.raw, &sdkReq); err != nil {
		return false
	}
	return nativeReadIsSkillAddress(sdkReq.Params.URI, known)
}

// nativeReadIsSkillAddress decides which resources/read requests the native
// surface answers: an address in the extension's skill:// namespace, or one a
// skill of this server lists under another scheme (known). Every other read,
// one without a uri included, is an ordinary resource read and stays the
// transport's, answered exactly as on an endpoint without the native surface.
func nativeReadIsSkillAddress(uri string, known func(string) bool) bool {
	if strings.HasPrefix(uri, skillScheme) {
		return true
	}
	return uri != "" && known != nil && known(uri)
}

// skillScheme is the extension's address namespace.
const skillScheme = "skill://"

// nativeKnowsFileURI reports whether an available skill of server is listed
// under uri or lists a file under it.
func (p *MCPProxyServer) nativeKnowsFileURI(server, uri string) bool {
	cat := p.skills()
	if cat == nil {
		return false
	}
	list, err := cat.ListAvailableSkills(server)
	if err != nil {
		return false
	}
	for _, sk := range list {
		if sk.URI == uri {
			return true
		}
		for _, f := range sk.Files {
			if f.URI == uri {
				return true
			}
		}
	}
	return false
}

// nativeStandardHeaderError checks the request's Mcp-Method and Mcp-Name
// headers against its ORIGINAL method and params with the SDK's own rule
// (mcp.ValidateStandardHeaders), under the same conditions the SDK applies
// it: an HTTP request stating Mcp-Protocol-Version whose _meta names a modern
// protocol version. It runs before the probe rewrites anything, because the
// probe's method (ping) needs no Mcp-Name and would hide a mismatch.
func nativeStandardHeaderError(h http.Header, req *nativeSkillsRequest) error {
	version, ok := modernHeaderVersion(h, req)
	if !ok {
		return nil
	}
	return mcp.ValidateStandardHeaders(h.Get, version, mcp.MCPMethod(req.Method), req.Params)
}

// modernHeaderVersion is the protocol version the SDK checks the standard
// headers under, and whether it checks them at all: an HTTP request stating
// Mcp-Protocol-Version whose params._meta names a modern version.
func modernHeaderVersion(h http.Header, req *nativeSkillsRequest) (string, bool) {
	if len(h) == 0 || h.Get(mcp.HeaderProtocolVersion) == "" {
		return "", false
	}
	var params struct {
		Meta *mcp.Meta `json:"_meta"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			// The SDK reads such a request as legacy.
			return "", false
		}
	}
	version := params.Meta.ProtocolVersion()
	return version, mcp.IsModernProtocol(version)
}

// writeNativeHeaderMismatch answers a header mismatch the way the SDK does:
// a JSON-RPC error with HEADER_MISMATCH and the error's text, HTTP 400 (the
// status protocol version 2026-07-28 prescribes; only modern requests carry
// the headers, so only they reach this).
func writeNativeHeaderMismatch(w http.ResponseWriter, id json.RawMessage, err error) {
	var rawID any
	_ = json.Unmarshal(id, &rawID)
	resp := mcp.JSONRPCError{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(rawID),
		Error:   mcp.NewJSONRPCErrorDetails(mcp.HEADER_MISMATCH, err.Error(), nil),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(resp)
}

// nativeProbeGate is the transport-parity part of the interceptor: it hands
// every request it does not claim to next untouched, and answers a claimed one
// only after the SDK's checks passed, in the SDK's order (transport checks,
// then the standard headers against the original method).
func nativeProbeGate(next http.Handler, claims func(*nativeSkillsRequest) bool,
	answer func(http.ResponseWriter, *http.Request, *nativeSkillsRequest)) http.Handler {
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
		if !ok || !claims(req) {
			next.ServeHTTP(w, r)
			return
		}
		// Decided on the original request, before either the body or the
		// headers are rewritten for the probe.
		headerErr := nativeStandardHeaderError(r.Header, req)
		method := probeMethod(req)
		probe, err := probeBody(body, method)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		pr := r.Clone(r.Context())
		pr.Body = io.NopCloser(bytes.NewReader(probe))
		pr.ContentLength = int64(len(probe))
		pr.Header = r.Header.Clone()
		// Mcp-Name, which neither probe method uses, is dropped. When the
		// original headers agree, a stated Mcp-Method names the probe's
		// method. When they do not, the probe is made to fail the SDK's same
		// header check, at the same point (after the transport's earlier
		// checks, before any hook runs): its Mcp-Method names the original
		// method, or stays absent when the original omitted it. That refusal
		// is then answered with the original request's own (headerErr).
		pr.Header.Del(mcp.HeaderName)
		var standIn string
		if headerErr == nil {
			if pr.Header.Get(mcp.HeaderMethod) != "" {
				pr.Header.Set(mcp.HeaderMethod, string(method))
			}
		} else {
			if pr.Header.Get(mcp.HeaderMethod) != "" {
				pr.Header.Set(mcp.HeaderMethod, req.Method)
			}
			if version, ok := modernHeaderVersion(r.Header, req); ok {
				if err := mcp.ValidateStandardHeaders(pr.Header.Get, version, method, nil); err != nil {
					standIn = err.Error()
				}
			}
		}
		rec := newBufferedResponse()
		next.ServeHTTP(rec, pr)
		if !rec.accepted() {
			if headerErr != nil && standIn != "" && rec.refusedWith(mcp.HEADER_MISMATCH, standIn) {
				copyProbeHeaders(w, rec)
				writeNativeHeaderMismatch(w, req.ID, headerErr)
				return
			}
			rec.replay(w)
			return
		}
		copyProbeHeaders(w, rec)
		if headerErr != nil {
			// Not reached while the SDK checks the probe's headers as it
			// checks the request's; refused all the same.
			writeNativeHeaderMismatch(w, req.ID, headerErr)
			return
		}
		answer(w, r, req)
	})
}

// nativeAnswer is one JSON-RPC answer: a result or an error.
type nativeAnswer struct {
	result any
	code   int
	msg    string
}

func writeNativeAnswer(w http.ResponseWriter, id json.RawMessage, modern bool, a nativeAnswer) {
	out := map[string]any{"jsonrpc": mcp.JSONRPC_VERSION, "id": id}
	if a.code != 0 {
		out["error"] = map[string]any{"code": a.code, "message": a.msg}
	} else {
		out["result"] = a.result
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(nativeAnswerStatus(modern, a.code))
	_ = json.NewEncoder(w).Encode(out)
}

// nativeAnswerStatus is the HTTP status the SDK's transport sends a JSON-RPC
// answer with: 200 for the legacy era; for a modern request (the probe passed,
// so header and _meta agree on the version) the status protocol version
// 2026-07-28 maps the error code to. The SDK's mapping is unexported; this
// copy is pinned to it by TestNativeProbeGate_AnswerStatusMatchesSDK.
func nativeAnswerStatus(modern bool, code int) int {
	if !modern {
		return http.StatusOK
	}
	switch code {
	case mcp.METHOD_NOT_FOUND:
		return http.StatusNotFound
	case mcp.HEADER_MISMATCH, mcp.MISSING_REQUIRED_CLIENT_CAPABILITY, mcp.UNSUPPORTED_PROTOCOL_VERSION,
		mcp.INVALID_REQUEST, mcp.INVALID_PARAMS, mcp.PARSE_ERROR:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
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
	if !frontmatterUnchangedBy(clean, fm) {
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

// frontmatterUnchangedBy reports whether the policy leaves every key and every
// string value of the frontmatter unchanged. The check runs on the decoded
// text: the stored JSON escapes control characters (an ESC is "\u001b" there),
// so checking the serialized form would let through what a client decodes.
// Frontmatter that does not decode is not served.
func frontmatterUnchangedBy(clean func(string) string, fm json.RawMessage) bool {
	var doc any
	if err := json.Unmarshal(fm, &doc); err != nil {
		return false
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch t := v.(type) {
		case string:
			return clean(t) == t
		case map[string]any:
			for k, val := range t {
				if clean(k) != k || !walk(val) {
					return false
				}
			}
		case []any:
			for _, val := range t {
				if !walk(val) {
					return false
				}
			}
		}
		return true
	}
	return walk(doc)
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
	writeNativeAnswer(w, req.ID, mcp.IsModernProtocol(r.Header.Get(mcp.HeaderProtocolVersion)), a)
}

// nativeParams decodes the params each method reads, and only those: a
// member another method uses is ignored, as the SDK ignores members its params
// type does not name. resources/read is decoded with the SDK's own type
// (nativeClaims admitted only reads that parse with it); skills/list reads
// cursor and skills/get reads uri, each with _meta decoded so a malformed one
// is invalid params. ok is false when params do not decode.
func nativeParams(req *nativeSkillsRequest) (cursor, uri string, ok bool) {
	t := bytes.TrimSpace(req.Params)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return "", "", true
	}
	switch req.Method {
	case nativeMethodRead:
		var p mcp.ReadResourceParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return "", "", false
		}
		return "", p.URI, true
	case nativeMethodGet:
		var p struct {
			URI  string    `json:"uri"`
			Meta *mcp.Meta `json:"_meta"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return "", "", false
		}
		return "", p.URI, true
	default:
		var p struct {
			Cursor string    `json:"cursor"`
			Meta   *mcp.Meta `json:"_meta"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return "", "", false
		}
		return p.Cursor, "", true
	}
}

func (p *MCPProxyServer) nativeSkillsAnswer(ctx context.Context, ep *singleServerEndpoint, req *nativeSkillsRequest, act *runtime.SkillAccessActivity) nativeAnswer {
	cursor, uri, ok := nativeParams(req)
	if !ok {
		act.Outcome = runtime.SkillAccessInvalid
		return nativeAnswer{code: mcp.INVALID_PARAMS, msg: "invalid params"}
	}
	if req.Method != nativeMethodList {
		if uri == "" {
			act.Outcome = runtime.SkillAccessInvalid
			return nativeAnswer{code: mcp.INVALID_PARAMS, msg: "missing required parameter: uri"}
		}
		act.SkillID = cutRunes(echoSkillID(uri), skillIDEchoMax)
	} else if cursor != "" {
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
