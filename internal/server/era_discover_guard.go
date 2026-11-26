package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
)

// maxDiscoverProbeBytes bounds the body clientFacingEraGuard reads to recognise
// a server/discover probe. A probe is a few hundred bytes.
const maxDiscoverProbeBytes = 64 << 10

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
		// Whatever is passed on reads the bytes already taken, then the rest.
		r.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(raw), orig), Closer: orig}
		if err != nil || len(raw) > maxDiscoverProbeBytes {
			// Too large to be a probe, or unreadable: let mcp-go answer it
			// from the bytes already read (an oversized body is refused there).
			next.ServeHTTP(w, r)
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Method != string(mcp.MethodServerDiscover) ||
			len(msg.ID) == 0 || string(msg.ID) == "null" {
			next.ServeHTTP(w, r)
			return
		}
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": mcp.JSONRPC_VERSION,
			"id":      msg.ID,
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

type readCloser struct {
	io.Reader
	io.Closer
}
