package registries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// GH discussion #783: every user-added source was persisted as
// "modelcontextprotocol/registry" with /v0.1/servers glued onto whatever URL was
// pasted, so MCPProxy fetched routes that only the official registry has. A
// static JSON registry (Fleur's apps.json) 404'd, and the failure only surfaced
// later, at search time, as an opaque "registry query returned 404".
//
// ProbeRegistrySource replaces that assumption with a measurement: fetch the
// source once at ADD time, and let the payload decide both the URL to store and
// the protocol to speak.

var (
	// ErrRegistrySourceUnusable means the source answered but is not an MCP
	// server list (404 on every candidate, HTML, non-JSON, or JSON with no
	// recognizable server list). This is a definitive verdict: the add is
	// refused so the user learns immediately, rather than at the next search.
	ErrRegistrySourceUnusable = errors.New("registry source did not return a list of MCP servers")

	// ErrRegistrySourceUnreachable means the source could not be contacted at all
	// (DNS failure, connection refused, timeout). This says nothing about whether
	// the URL is a valid registry, so callers may still persist the source.
	ErrRegistrySourceUnreachable = errors.New("registry source could not be reached")
)

// SourceProbe is the outcome of a successful probe: the exact URL to fetch and
// the protocol to parse it with.
type SourceProbe struct {
	ServersURL string
	Protocol   string
}

// ProbeRegistrySource fetches a candidate registry URL and classifies it. The
// URL the user pasted is always tried FIRST and, if it works, stored verbatim.
// Only a base URL that turns out not to serve a list itself falls back to the
// official /v0.1/servers collection.
func ProbeRegistrySource(ctx context.Context, rawURL string) (*SourceProbe, error) {
	candidates := probeCandidates(rawURL)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %q is not a valid URL", ErrRegistrySourceUnusable, rawURL)
	}

	var (
		reasons   []string
		reachable bool
	)
	for _, candidate := range candidates {
		// Pin the fetch to the candidate itself so validateRegistryURL's host check
		// and the SSRF dial guard both apply to the probe exactly as they do to a
		// real registry fetch.
		body, err := registryGet(ctx, &RegistryEntry{URL: candidate, ServersURL: candidate}, candidate)
		if err != nil {
			var statusErr *registryStatusError
			if errors.As(err, &statusErr) {
				// The host answered — a definitive verdict about this candidate.
				reachable = true
			}
			reasons = append(reasons, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		reachable = true

		protocol, err := classifyRegistryPayload(body)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		return &SourceProbe{ServersURL: candidate, Protocol: protocol}, nil
	}

	sentinel := ErrRegistrySourceUnusable
	if !reachable {
		sentinel = ErrRegistrySourceUnreachable
	}
	return nil, fmt.Errorf("%w (%s)", sentinel, strings.Join(reasons, "; "))
}

// probeCandidates lists the URLs to try, in order. The pasted URL is never
// mangled: it is always candidate #0. A URL that does not already look like a
// servers collection additionally gets the official /v0.1/servers route as a
// fallback, so pasting the base URL of a real official registry keeps working.
func probeCandidates(rawURL string) []string {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil
	}

	candidates := []string{rawURL}
	if !strings.Contains(u.Path, "/servers") && !looksLikeDocument(u.Path) {
		withRoute := *u
		withRoute.Path = strings.TrimSuffix(u.Path, "/") + "/v0.1/servers"
		candidates = append(candidates, withRoute.String())
	}
	return candidates
}

// looksLikeDocument reports whether a path names a concrete file (apps.json,
// registry.yaml, …). Appending a route to such a path can only 404.
func looksLikeDocument(path string) bool {
	last := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		last = path[i+1:]
	}
	return strings.Contains(last, ".")
}

// classifyRegistryPayload decides which protocol a body speaks. An official v0.1
// response (the { "servers": [...], "metadata": {...} } envelope) is fetched with
// the paginating official fetcher; anything else that still holds a recognizable
// list of servers is treated as a static JSON document and fetched verbatim.
func classifyRegistryPayload(body []byte) (string, error) {
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("response is not JSON: %w", err)
	}

	if root, ok := data.(map[string]interface{}); ok {
		// The official envelope is the only shape that paginates, and it is the
		// only one whose query parameters (version/limit/search/cursor) are safe to
		// send. Recognize it structurally: a "servers" list, optionally alongside
		// the cursor metadata block.
		if _, hasServers := root["servers"].([]interface{}); hasServers {
			return protocolOfficial, nil
		}
	}

	// Anything else must yield at least one usable server to count as a registry;
	// otherwise the URL is some other JSON document (or an HTML page) and the user
	// should hear about it now.
	if len(parseGenericJSON(data)) == 0 {
		return "", errors.New("no MCP servers found in the response")
	}
	return protocolGenericJSON, nil
}
