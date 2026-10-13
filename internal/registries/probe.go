package registries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// GH discussion #783: every user-added source was persisted as
// "modelcontextprotocol/registry" with /v0.1/servers glued onto whatever URL was
// pasted, so MCPProxy fetched routes that only the official registry has. A
// pasted static JSON document 404'd, and the failure only surfaced later, at
// search time, as an opaque "registry query returned 404".
//
// ProbeRegistrySource replaces that assumption with a measurement: fetch the
// source once at ADD time and confirm it really does speak the official v0.1
// protocol — the only registry protocol MCPProxy implements. If it does not, say
// so now, with the reason.

var (
	// ErrRegistrySourceUnusable means the source answered but is not an official
	// v0.1 registry (404 on every candidate, HTML, non-JSON, or JSON that is not
	// a server list). This is a definitive verdict: the add is refused so the
	// user learns immediately, rather than at the next search.
	ErrRegistrySourceUnusable = errors.New("registry source is not a modelcontextprotocol/registry v0.1 endpoint")

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

// probeCandidateTimeout bounds ONE candidate URL. It is deliberately much
// tighter than registryRequestTimeout: an add is an interactive operation, and a
// registry too slow to answer within this window is simply added unverified
// (ErrRegistrySourceUnreachable is tolerated) rather than made to hang the CLI,
// the REST call, and the user.
const probeCandidateTimeout = 12 * time.Second

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
		// inconclusive records that at least one candidate could not be JUDGED —
		// it timed out or the connection failed. We must not refuse a source on a
		// check we never actually completed: the official registry's base URL
		// serves an HTML docs page (a definitive "not a registry") while its real
		// /v0.1/servers collection can take ~20s to answer, so judging on the
		// first candidate alone would refuse the official registry itself.
		inconclusive bool
	)
	for _, candidate := range candidates {
		// Bound each candidate separately, so one slow endpoint cannot eat the whole
		// probe budget and starve the next candidate.
		candidateCtx, cancel := context.WithTimeout(ctx, probeCandidateTimeout)
		// Pin the fetch to the candidate itself so validateRegistryURL's host check
		// and the SSRF dial guard both apply to the probe exactly as they do to a
		// real registry fetch.
		body, err := registryGet(candidateCtx, &RegistryEntry{URL: candidate, ServersURL: candidate}, candidate)
		cancel()

		if err != nil {
			var statusErr *registryStatusError
			switch {
			case errors.As(err, &statusErr),
				errors.Is(err, ErrBlockedRegistryHost),
				errors.Is(err, ErrRegistryRedirectRefused):
				// The host ANSWERED — with a non-200, from a blocked range, or with a
				// redirect we refuse to follow. A verdict, not a transient failure.
				reachable = true
			default:
				// Timeout / connection failure: we learned nothing about this source.
				inconclusive = true
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

	// Refuse ONLY when every candidate gave a definitive answer and none was a
	// registry. If any candidate was inconclusive, report the source as
	// unreachable so the add path tolerates it rather than rejecting a registry we
	// never managed to check.
	if inconclusive {
		return nil, fmt.Errorf("%w (%s)", ErrRegistrySourceUnreachable, strings.Join(reasons, "; "))
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

// classifyRegistryPayload verifies that a body is an official v0.1 registry
// page, and returns the protocol to store.
//
// MCPProxy speaks exactly one registry protocol: modelcontextprotocol/registry
// v0.1. A URL that answers with something else — an HTML page, an arbitrary JSON
// document, a bespoke app-store catalog — is not a registry we can browse, and
// the user learns that HERE, at add time, with the reason. Previously we assumed
// every added source spoke the official protocol and only failed later, at
// search time, with an opaque 404 (GH #783).
func classifyRegistryPayload(body []byte) (string, error) {
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("response is not JSON: %w", err)
	}

	if !isOfficialPayload(data) {
		return "", errors.New("response is not a modelcontextprotocol/registry v0.1 server list")
	}
	return protocolOfficial, nil
}

// isOfficialPayload reports whether a body is an official v0.1 registry page.
//
// The gate is defined BY THE PARSER, not by a second opinion about what official
// JSON looks like: a payload qualifies when parseOfficialPage — the very function
// that will read this registry on every later search — gets at least one server
// out of it with a usable transport. Any other rule risks drifting from the
// parser in one of two bad directions:
//
//   - stricter than the parser ⇒ we refuse to add a registry that would have
//     worked. parseOfficialPage tolerates a bare array of wrapped items and the
//     alternative "data" envelope, and an earlier version of this check rejected
//     both.
//   - looser than the parser ⇒ we accept a bespoke catalog (a list of
//     {name, config:{runtime,args}} app entries) whose items the official parser
//     turns into servers with no package and no remote — visible in search,
//     impossible to add.
//
// An EMPTY official page has no items to judge, so it is recognised structurally.
func isOfficialPayload(data interface{}) bool {
	servers, _ := parseOfficialPage(data)
	for i := range servers {
		if servers[i].InstallCmd != "" || servers[i].URL != "" || servers[i].ConnectURL != "" {
			return true
		}
	}
	return isEmptyOfficialEnvelope(data)
}

// isEmptyOfficialEnvelope recognises a registry that is genuinely empty (or whose
// first page is), which carries no items to sniff: a "servers"/"data" list that
// is present and EMPTY.
//
// The emptiness is the whole point. An earlier cut also accepted any payload
// carrying a "metadata" object, which was a loophole: a bespoke catalog that
// happens to include metadata (say {"servers":[…app entries…],"metadata":{"total":1}})
// sailed through even though its items parse to servers with no runnable
// transport. If a list is present and non-empty, it must earn acceptance through
// the parser (isOfficialPayload) — never through a sibling key.
func isEmptyOfficialEnvelope(data interface{}) bool {
	root, ok := data.(map[string]interface{})
	if !ok {
		return false
	}
	for _, key := range []string{"servers", "data"} {
		if items, ok := root[key].([]interface{}); ok && len(items) == 0 {
			return true
		}
	}
	return false
}
