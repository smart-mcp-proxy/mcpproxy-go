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

// TestProbeRegistrySource_StaticJSONFile is GH discussion #783: the user pastes
// a static JSON file URL (Fleur's apps.json). The probe must keep that URL
// VERBATIM (no /v0.1/servers suffix, which 404s) and classify it as custom/json
// so the fetcher does not append official-protocol query params to it either.
func TestProbeRegistrySource_StaticJSONFile(t *testing.T) {
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

	probe, err := ProbeRegistrySource(context.Background(), srv.URL+"/repo/apps.json")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if probe.ServersURL != srv.URL+"/repo/apps.json" {
		t.Errorf("ServersURL = %q, want the pasted URL verbatim", probe.ServersURL)
	}
	if probe.Protocol != protocolGenericJSON {
		t.Errorf("Protocol = %q, want %q", probe.Protocol, protocolGenericJSON)
	}
	if gotPath != "/repo/apps.json" {
		t.Errorf("requested path = %q, want the pasted path with nothing appended", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("requested query = %q, want none for a static JSON source", gotQuery)
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

// TestProbeRegistrySource_StaticServersEnvelopeIsGeneric: a STATIC document may
// also wrap its list in "servers". Classifying on that key alone marked such a
// file as the official protocol — after which we would append the official
// query params to a static file and parse it with the official parser, losing
// the {config:{runtime,args}} install info entirely (server shows up, cannot be
// added). Only a genuinely official payload may claim the official protocol.
func TestProbeRegistrySource_StaticServersEnvelopeIsGeneric(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"name":"Fetch","description":"d","config":{"runtime":"uvx","args":["mcp-server-fetch"]}}]}`)
	}))
	defer srv.Close()

	probe, err := ProbeRegistrySource(context.Background(), srv.URL+"/apps.json")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if probe.Protocol != protocolGenericJSON {
		t.Errorf("Protocol = %q, want %q — a static file is not the official protocol just because it says \"servers\"", probe.Protocol, protocolGenericJSON)
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
