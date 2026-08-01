package connect

import (
	"net/url"
	"sort"
	"strings"
)

// EntrySummary is the sanitized, display-only description of the client-config
// entry a connect would replace (Spec 091 FR-003).
//
// It exists because the preview must tell the user WHAT is being overwritten
// without ever echoing config content: the pending entry is masked at
// construction (entryParams substitutes the mask constant), which cannot
// sanitize arbitrary user-authored content in an EXISTING entry. So the preview
// renders no existing content at all — only this fixed set of parsed
// projections, built by construction from a whitelist:
//
//	entry name | transport type | endpoint (query, userinfo and fragment
//	stripped) | command | header NAMES | env NAMES
//
// Nothing else from the entry can reach the response: unknown fields are not
// copied, values of headers/env are never read, and a URL-shaped field is
// emitted only after being reparsed into scheme://host/path. Secrecy therefore
// holds structurally rather than by masking heuristics (research D2).
//
// The summary is display-only: it is NEVER used for drift comparison — that is
// the precondition token's job over the raw value (FR-005).
type EntrySummary struct {
	// EntryName is the key the entry actually lives under, which may differ
	// from the requested server_name when the write adopts an equivalent entry.
	EntryName   string   `json:"entry_name"`
	Type        string   `json:"type,omitempty"`
	Endpoint    string   `json:"endpoint,omitempty"`
	Command     string   `json:"command,omitempty"`
	HeaderNames []string `json:"header_names"`
	EnvNames    []string `json:"env_names"`
}

// bridgeHeaderArgs are the argv flags whose following token is a
// "Name: value" HTTP header (mcp-remote's --header). Only the NAME half is ever
// projected.
var bridgeHeaderArgs = map[string]bool{"--header": true, "-H": true}

// buildEntrySummary projects a parsed config entry onto EntrySummary. entry may
// be nil (an existing entry that is not an object): the summary then carries the
// name alone, which is still the useful disclosure ("this key is replaced").
func buildEntrySummary(entryName string, entry map[string]interface{}) *EntrySummary {
	summary := &EntrySummary{
		EntryName:   entryName,
		HeaderNames: []string{},
		EnvNames:    []string{},
	}
	if entry == nil {
		return summary
	}

	if v, ok := entry["type"].(string); ok {
		summary.Type = v
	}
	if v, ok := entry["command"].(string); ok {
		summary.Command = v
	}
	// Endpoint carriers, in the same precedence the entry matcher uses.
	for _, field := range []string{"url", "serverUrl", "httpUrl"} {
		if v, ok := entry[field].(string); ok {
			if sanitized := sanitizeEndpoint(v); sanitized != "" {
				summary.Endpoint = sanitized
				break
			}
		}
	}
	summary.HeaderNames = mapKeyNames(entry["headers"])
	summary.EnvNames = mapKeyNames(entry["env"])

	// Stdio-bridge entries carry the endpoint and the credential in argv. Only
	// the URL-shaped arg (sanitized) and the header NAMES are projected; no arg
	// value ever is.
	if args, ok := entry["args"].([]interface{}); ok {
		names := append([]string{}, summary.HeaderNames...)
		expectHeader := false
		for _, raw := range args {
			arg, ok := raw.(string)
			if !ok {
				continue
			}
			switch {
			case expectHeader:
				expectHeader = false
				if name := headerArgName(arg); name != "" {
					names = append(names, name)
				}
			case bridgeHeaderArgs[arg]:
				expectHeader = true
			case strings.HasPrefix(arg, "--header="):
				if name := headerArgName(strings.TrimPrefix(arg, "--header=")); name != "" {
					names = append(names, name)
				}
			case summary.Endpoint == "":
				if sanitized := sanitizeEndpoint(arg); sanitized != "" {
					summary.Endpoint = sanitized
				}
			}
		}
		summary.HeaderNames = dedupeSorted(names)
	}

	return summary
}

// headerArgName extracts the header NAME from a "Name:value" argv token. An
// empty result means the token had no name half and is dropped entirely.
func headerArgName(arg string) string {
	name, _, found := strings.Cut(arg, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(name)
}

// mapKeyNames returns the sorted KEYS of a JSON object field (headers, env).
// Values are never touched. A non-object field yields an empty slice.
func mapKeyNames(v interface{}) []string {
	obj, ok := v.(map[string]interface{})
	if !ok {
		return []string{}
	}
	names := make([]string, 0, len(obj))
	for name := range obj {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func dedupeSorted(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// sanitizeEndpoint reparses a URL-shaped value into a disclosure-safe endpoint:
// scheme, host and path only — query (the ?apikey= credential carrier),
// userinfo (user:pass@) and fragment are dropped. A value that is not a real
// absolute URL is NOT projected at all (empty result): only a scheme://host
// shape is known-safe, so an arbitrary string in a url field can never be
// echoed back.
func sanitizeEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	clean := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}
	return clean.String()
}
