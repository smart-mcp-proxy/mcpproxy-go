package registries

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProbeRegistrySource_StaticJSONFileIsRefused is GH discussion #783: the
// user pastes a static JSON document (a bespoke app catalog). MCPProxy speaks
// only the official v0.1 protocol, so this cannot be browsed — but the user must
// be told THAT, at add time, instead of having the URL silently rewritten to
// ".../apps.json/v0.1/servers" and 404-ing on every later search.
func TestProbeRegistrySource_StaticJSONFileIsRefused(t *testing.T) {
	withFastRetries(t)

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if r.URL.Path != "/repo/apps.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"name":"Fetch","description":"d","config":{"runtime":"uvx","args":["mcp-server-fetch"]}}]`)
	}))
	defer srv.Close()

	_, err := ProbeRegistrySource(context.Background(), srv.URL+"/repo/apps.json")
	if !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable", err)
	}

	// Even while probing, the pasted URL is fetched EXACTLY as given: no invented
	// route, no official-protocol query params.
	if gotPath != "/repo/apps.json" {
		t.Errorf("requested path = %q, want the pasted path with nothing appended", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("requested query = %q, want none", gotQuery)
	}
}

// TestProbeRegistrySource_OfficialBaseURL: a bare base URL of a real official
// registry still resolves to its /v0.1/servers collection and the official
// protocol — the existing behaviour must not regress.
func TestProbeRegistrySource_OfficialBaseURL(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0.1/servers" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"server":{"name":"io.acme/x","description":"d"},"_meta":{}}],"metadata":{}}`)
	}))
	defer srv.Close()

	probe, err := ProbeRegistrySource(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if probe.ServersURL != srv.URL+"/v0.1/servers" {
		t.Errorf("ServersURL = %q, want the derived servers collection", probe.ServersURL)
	}
	if probe.Protocol != protocolOfficial {
		t.Errorf("Protocol = %q, want %q", probe.Protocol, protocolOfficial)
	}
}

// TestProbeRegistrySource_OfficialServersURL: pasting the full servers endpoint
// works too, and is used verbatim.
func TestProbeRegistrySource_OfficialServersURL(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[],"metadata":{"nextCursor":""}}`)
	}))
	defer srv.Close()

	probe, err := ProbeRegistrySource(context.Background(), srv.URL+"/v0.1/servers")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if probe.ServersURL != srv.URL+"/v0.1/servers" || probe.Protocol != protocolOfficial {
		t.Errorf("got %+v, want the pasted endpoint on the official protocol", probe)
	}
}

// TestProbeRegistrySource_NotARegistry: a URL that answers but is not a server
// list (an HTML page, e.g. a docs URL) fails the probe with a definitive,
// actionable error instead of being persisted as a broken registry that only
// fails later at search time.
func TestProbeRegistrySource_NotARegistry(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body>hello</body></html>`)
	}))
	defer srv.Close()

	_, err := ProbeRegistrySource(context.Background(), srv.URL+"/docs")
	if !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable", err)
	}
}

// TestProbeRegistrySource_NotFound: every candidate 404s => unusable, and the
// message names the status the user would otherwise only see at search time.
func TestProbeRegistrySource_NotFound(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := ProbeRegistrySource(context.Background(), srv.URL+"/nope.json")
	if !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q should name the 404 status", err)
	}
}

// TestProbeRegistrySource_Unreachable: a transport failure (DNS/connection) is
// NOT a definitive verdict about the source, so it surfaces as
// ErrRegistrySourceUnreachable — the add path tolerates it (offline add) rather
// than refusing the registry.
func TestProbeRegistrySource_Unreachable(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	closedURL := srv.URL
	srv.Close() // nothing is listening now

	_, err := ProbeRegistrySource(context.Background(), closedURL+"/apps.json")
	if !errors.Is(err, ErrRegistrySourceUnreachable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnreachable", err)
	}
}

// TestProbeCandidates documents the candidate order: the pasted URL is ALWAYS
// tried first (never mangled), and only a path-less base URL additionally falls
// back to the official /v0.1/servers collection.
func TestProbeCandidates(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"https://acme.example", []string{"https://acme.example", "https://acme.example/v0.1/servers"}},
		{"https://acme.example/", []string{"https://acme.example/", "https://acme.example/v0.1/servers"}},
		{"https://acme.example/v0.1/servers", []string{"https://acme.example/v0.1/servers"}},
		{"https://raw.githubusercontent.com/o/r/main/apps.json", []string{"https://raw.githubusercontent.com/o/r/main/apps.json"}},
		{"https://acme.example/registry", []string{"https://acme.example/registry", "https://acme.example/registry/v0.1/servers"}},
	}
	for _, tt := range tests {
		got := probeCandidates(tt.raw)
		if len(got) != len(tt.want) {
			t.Errorf("probeCandidates(%q) = %v, want %v", tt.raw, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("probeCandidates(%q)[%d] = %q, want %q", tt.raw, i, got[i], tt.want[i])
			}
		}
	}
}

// --- Codex review round 1 -----------------------------------------------------

// A static catalog that merely wraps its list under "servers" must not be
// mistaken for an official registry (it carries none of the official signals).
func TestProbeRegistrySource_StaticServersEnvelopeIsRefused(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"name":"Fetch","description":"d","config":{"runtime":"uvx","args":["mcp-server-fetch"]}}]}`)
	}))
	defer srv.Close()

	if _, err := ProbeRegistrySource(context.Background(), srv.URL+"/apps.json"); !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable — a static catalog is not the official protocol just because it says \"servers\"", err)
	}
}

// An official registry whose first page is EMPTY must still be recognised as
// official (there are no items to sniff, and it paginates).
func TestProbeRegistrySource_EmptyOfficialPageStaysOfficial(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[],"metadata":{"count":0}}`)
	}))
	defer srv.Close()

	probe, err := ProbeRegistrySource(context.Background(), srv.URL+"/v0.1/servers")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if probe.Protocol != protocolOfficial {
		t.Errorf("Protocol = %q, want official for an empty official page", probe.Protocol)
	}
}

// TestProbeRegistrySource_BlockedHostIsDefinitive: a source whose host resolves
// into a blocked (SSRF) range can never work, so it must be REFUSED — not
// waved through by the offline-tolerance path as if the network were merely
// down.
func TestProbeRegistrySource_BlockedHostIsDefinitive(t *testing.T) {
	withFastRetries(t)
	withGuardActive(t)

	_, err := ProbeRegistrySource(context.Background(), "https://127.0.0.1:9/apps.json")
	if !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable (a blocked host is a verdict, not a transient failure)", err)
	}
	if errors.Is(err, ErrRegistrySourceUnreachable) {
		t.Error("a blocked host must not be reported as merely unreachable — that would add the registry")
	}
}

// --- Codex review round 2 -----------------------------------------------------

// TestProbeRegistrySource_AcceptsEveryShapeTheParserReads: the probe must accept
// exactly what the official fetcher can actually read. parseOfficialPage
// tolerates a bare array of wrapped items and the alternative "data" envelope,
// so refusing those at add time would block a user from adding a registry that
// works perfectly well once added.
func TestProbeRegistrySource_AcceptsEveryShapeTheParserReads(t *testing.T) {
	withFastRetries(t)

	bodies := map[string]string{
		"bare array of wrapped items": `[{"server":{"name":"io.acme/x","description":"d","remotes":[{"url":"https://acme.example/mcp"}]},"_meta":{}}]`,
		"data envelope":               `{"data":[{"server":{"name":"io.acme/x","description":"d","packages":[{"registryType":"npm","identifier":"acme"}]},"_meta":{}}],"metadata":{}}`,
		"wrapped items under servers": `{"servers":[{"server":{"name":"io.acme/x","description":"d","packages":[{"registryType":"npm","identifier":"acme"}]},"_meta":{}}]}`,
		// The legacy flat shape parseOpenAPIRegistry reads: ServerEntry-shaped
		// objects with a url. (A flat item carrying `packages` is NOT accepted —
		// see TestProbeRegistrySource_RefusesPayloadTheParserCannotUse.)
		"legacy flat list": `{"servers":[{"id":"acme","name":"acme","url":"https://acme.example/mcp"}]}`,
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer srv.Close()

			probe, err := ProbeRegistrySource(context.Background(), srv.URL+"/v0.1/servers")
			if err != nil {
				t.Fatalf("probe refused a payload the official parser reads fine: %v", err)
			}
			if probe.Protocol != protocolOfficial {
				t.Errorf("Protocol = %q, want official", probe.Protocol)
			}
		})
	}
}

// TestProbeRegistrySource_RefusedRedirectIsDefinitive: a source that answers with
// a redirect our policy refuses (cross-host, or into a blocked range) ANSWERED —
// it is not "offline". Filing it as unreachable would wave it through the
// offline-tolerance path and persist a registry that can never work.
func TestProbeRegistrySource_RefusedRedirectIsDefinitive(t *testing.T) {
	withFastRetries(t)

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"servers":[],"metadata":{}}`)
	}))
	defer elsewhere.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v0.1/servers", http.StatusFound)
	}))
	defer srv.Close()

	_, err := ProbeRegistrySource(context.Background(), srv.URL+"/v0.1/servers")
	if !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable", err)
	}
	if errors.Is(err, ErrRegistrySourceUnreachable) {
		t.Error("a refused redirect must not be reported as merely unreachable — that would ADD the registry")
	}
}

// TestProbeRegistrySource_RefusesPayloadTheParserCannotUse pins the other half of
// the probe's contract: it accepts a payload only if the OFFICIAL PARSER can get
// a usable (installable or connectable) server out of it.
//
// A flat, unwrapped item carrying `packages` is the sharp case. parseOfficialPage
// routes an unwrapped list to the legacy parseOpenAPIRegistry, which unmarshals
// straight into ServerEntry — a struct with no `packages` field — so the install
// info is dropped and the entries come out with neither a command nor a URL.
// Adding such a source would put servers in the UI that can never be installed.
// Better to refuse it at add time than to ship that dead end.
func TestProbeRegistrySource_RefusesPayloadTheParserCannotUse(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"name":"io.acme/x","description":"d","packages":[{"registryType":"npm","identifier":"acme"}]}]}`)
	}))
	defer srv.Close()

	if _, err := ProbeRegistrySource(context.Background(), srv.URL+"/v0.1/servers"); !errors.Is(err, ErrRegistrySourceUnusable) {
		t.Fatalf("err = %v, want ErrRegistrySourceUnusable — the parser cannot get an installable server out of this", err)
	}
}
