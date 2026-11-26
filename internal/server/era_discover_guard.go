package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// maxDiscoverProbeBytes bounds the body clientFacingEraGuard reads to recognise
// a server/discover probe. A probe is a few hundred bytes; the bound is
// generous so whitespace padding does not hide one.
const maxDiscoverProbeBytes = 1 << 20

// clientFacingEraGuard makes a client-facing Streamable HTTP endpoint, pinned
// to the legacy protocol era (Spec 058 FR-028), answer a modern client's
// server/discover probe the way a legacy-only server does: JSON-RPC -32601
// "method not found". Every modern-first client then falls back to the
// legacy initialize handshake.
//
// Why (Spec 135 FR-019, research R1a): mcp-go serves the pin by refusing the
// probe with UnsupportedProtocolVersionError and the legacy versions it does
// serve. mcp-go >= v1.3 clients take that answer as "a modern server with no
// modern version in common" and fail instead of falling back, so no such
// client (mcpproxy's own tests, third-party Go clients) could connect. The pin
// itself is unchanged: only server/discover at a version the surface does not
// serve is answered here; everything else reaches mcp-go untouched.
func clientFacingEraGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := r.Header.Get(mcp.HeaderProtocolVersion)
		if r.Method != http.MethodPost || r.Body == nil || version == "" ||
			!mcp.IsModernProtocol(version) || slices.Contains(clientFacingProtocolVersions(), version) {
			next.ServeHTTP(w, r)
			return
		}
		orig := r.Body
		raw, err := io.ReadAll(io.LimitReader(orig, maxDiscoverProbeBytes+1))
		// Whatever is passed on reads the bytes already taken, then the rest,
		// and sees a read error exactly as the original body reported it.
		r.Body = replayBody(raw, err, orig)
		if err != nil || len(raw) > maxDiscoverProbeBytes {
			// Too large to be a probe, or unreadable: let mcp-go answer it
			// from the bytes already read (an oversized body is refused there).
			next.ServeHTTP(w, r)
			return
		}
		members, ok := exactTopLevelMembers(raw)
		if !ok || string(members["jsonrpc"]) != `"`+mcp.JSONRPC_VERSION+`"` ||
			string(members["method"]) != `"`+string(mcp.MethodServerDiscover)+`"` || !validRequestID(members["id"]) {
			// Anything that is not unambiguously a probe (duplicate or
			// case-variant members included) is mcp-go's to judge.
			next.ServeHTTP(w, r)
			return
		}
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": mcp.JSONRPC_VERSION,
			"id":      members["id"],
			"error": map[string]any{
				"code":    mcp.METHOD_NOT_FOUND,
				"message": "Method not found: " + string(mcp.MethodServerDiscover),
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// validRequestID reports whether raw is a JSON-RPC request id: a string or a
// number (null, objects and arrays are not).
func validRequestID(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if len(raw) == 0 || dec.Decode(&v) != nil {
		return false
	}
	switch v.(type) {
	case string, json.Number:
		return true
	}
	return false
}

// exactTopLevelMembers returns the members of a JSON object, keyed by their
// exact names. ok is false for anything but one object, and for an object
// with a member name repeated or differing from another only in case (which
// encoding/json would otherwise merge into one struct field).
func exactTopLevelMembers(raw []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	members := make(map[string]json.RawMessage)
	folded := make(map[string]bool)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		name, ok := tok.(string)
		if !ok {
			return nil, false
		}
		key := strings.ToLower(name)
		if folded[key] {
			return nil, false
		}
		folded[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		members[name] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false // trailing data
	}
	return members, true
}

type readCloser struct {
	io.Reader
	io.Closer
}

// errAfterReader returns err once its prefix has been read.
type errAfterReader struct {
	prefix *bytes.Reader
	err    error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	if e.prefix.Len() > 0 {
		return e.prefix.Read(p)
	}
	return 0, e.err
}

// replayBody rebuilds a request body after raw was read from orig: the same
// bytes, then the rest of orig; when the read failed with err, the same
// bytes, then err (not a plain EOF).
func replayBody(raw []byte, err error, orig io.ReadCloser) io.ReadCloser {
	if err != nil {
		return readCloser{Reader: &errAfterReader{prefix: bytes.NewReader(raw), err: err}, Closer: orig}
	}
	return readCloser{Reader: io.MultiReader(bytes.NewReader(raw), orig), Closer: orig}
}
