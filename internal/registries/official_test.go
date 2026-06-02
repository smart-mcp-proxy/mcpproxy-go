package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// loadOfficialFixture reads the recorded golden server.json-list response.
func loadOfficialFixture(t *testing.T) interface{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "official_v0.1_servers.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var data interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return data
}

func findEntry(servers []ServerEntry, id string) *ServerEntry {
	for i := range servers {
		if servers[i].ID == id || servers[i].Name == id {
			return &servers[i]
		}
	}
	return nil
}

// TestParseOfficialPage_StatusFilter verifies wrapped {server,_meta} parsing and
// that deprecated/deleted/non-latest entries are skipped by default.
func TestParseOfficialPage_StatusFilter(t *testing.T) {
	data := loadOfficialFixture(t)

	servers, nextCursor := parseOfficialPage(data)
	if nextCursor != "" {
		t.Errorf("expected empty nextCursor, got %q", nextCursor)
	}

	// 6 items in fixture; deprecated + old-version + deleted must be skipped => 3.
	if len(servers) != 3 {
		t.Fatalf("expected 3 active+latest servers, got %d: %+v", len(servers), servers)
	}
	for _, bad := range []string{"io.github.example/deprecated", "io.github.example/old-version", "io.github.example/deleted"} {
		if findEntry(servers, bad) != nil {
			t.Errorf("server %q should have been filtered out", bad)
		}
	}
}

// TestParseOfficialPage_Classification is the #567 root-fix matrix: packages =>
// local/stdio (InstallCmd set, URL empty); remotes => remote (URL set); hybrid
// => prefer package for stdio while preserving the remote as ConnectURL.
func TestParseOfficialPage_Classification(t *testing.T) {
	data := loadOfficialFixture(t)
	servers, _ := parseOfficialPage(data)

	t.Run("packages-only => stdio", func(t *testing.T) {
		s := findEntry(servers, "io.github.example/local-npm")
		if s == nil {
			t.Fatal("local-npm not found")
		}
		if s.URL != "" {
			t.Errorf("packages-only server must leave URL empty, got %q", s.URL)
		}
		want := "npx -y @example/local-npm@1.2.3 --stdio"
		if s.InstallCmd != want {
			t.Errorf("InstallCmd = %q, want %q", s.InstallCmd, want)
		}
		if len(s.RequiredInputs) != 2 {
			t.Fatalf("expected 2 required inputs, got %d", len(s.RequiredInputs))
		}
		if s.RequiredInputs[0].Name != "EXAMPLE_TOKEN" || !s.RequiredInputs[0].Secret {
			t.Errorf("first required input mismatch: %+v", s.RequiredInputs[0])
		}
		if s.SourceCodeURL != "https://github.com/example/local-npm" {
			t.Errorf("SourceCodeURL = %q", s.SourceCodeURL)
		}
	})

	t.Run("remotes-only => remote", func(t *testing.T) {
		s := findEntry(servers, "io.github.example/remote-http")
		if s == nil {
			t.Fatal("remote-http not found")
		}
		if s.InstallCmd != "" {
			t.Errorf("remotes-only server must leave InstallCmd empty, got %q", s.InstallCmd)
		}
		if s.URL != "https://mcp.example.com/mcp" {
			t.Errorf("URL = %q", s.URL)
		}
		if len(s.RequiredInputs) != 1 || s.RequiredInputs[0].Name != "Authorization" {
			t.Errorf("expected Authorization header input, got %+v", s.RequiredInputs)
		}
	})

	t.Run("hybrid => prefer package, keep remote as ConnectURL", func(t *testing.T) {
		s := findEntry(servers, "io.github.example/hybrid")
		if s == nil {
			t.Fatal("hybrid not found")
		}
		if s.InstallCmd != "uvx example-hybrid" {
			t.Errorf("InstallCmd = %q, want %q", s.InstallCmd, "uvx example-hybrid")
		}
		if s.URL != "" {
			t.Errorf("hybrid must leave URL empty (package preferred), got %q", s.URL)
		}
		if s.ConnectURL != "https://hybrid.example.com/sse" {
			t.Errorf("ConnectURL = %q", s.ConnectURL)
		}
	})
}

// TestFetchOfficialServers_Pagination verifies the cursor follow-loop across
// multiple pages, bounded by the page cap.
func TestFetchOfficialServers_Pagination(t *testing.T) {
	var gotCursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		gotCursors = append(gotCursors, cursor)
		w.Header().Set("Content-Type", "application/json")
		switch cursor {
		case "":
			fmt.Fprint(w, `{"servers":[{"server":{"name":"a","packages":[{"registryType":"npm","identifier":"a","runtimeHint":"npx"}]},"_meta":{"io.modelcontextprotocol.registry/official":{"status":"active","isLatest":true}}}],"metadata":{"nextCursor":"page2"}}`)
		case "page2":
			fmt.Fprint(w, `{"servers":[{"server":{"name":"b","packages":[{"registryType":"npm","identifier":"b","runtimeHint":"npx"}]},"_meta":{"io.modelcontextprotocol.registry/official":{"status":"active","isLatest":true}}}],"metadata":{"nextCursor":""}}`)
		default:
			t.Errorf("unexpected cursor %q", cursor)
		}
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	servers, err := fetchOfficialServers(context.Background(), reg, nil, "")
	if err != nil {
		t.Fatalf("fetchOfficialServers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers across 2 pages, got %d", len(servers))
	}
	if len(gotCursors) != 2 || gotCursors[0] != "" || gotCursors[1] != "page2" {
		t.Errorf("cursor follow sequence = %v", gotCursors)
	}
}

// TestReferenceServers_BuiltinOffline verifies the curated reference set is
// available without any network access and classified as stdio.
func TestReferenceServers_BuiltinOffline(t *testing.T) {
	servers := referenceServers()
	if len(servers) < 7 {
		t.Fatalf("expected >=7 reference servers, got %d", len(servers))
	}
	for _, want := range []string{"filesystem", "fetch", "memory", "time", "git", "sequentialthinking", "everything"} {
		s := findEntry(servers, "reference/"+want)
		if s == nil {
			// fall back to name match
			var found bool
			for i := range servers {
				if servers[i].ID == want || servers[i].Name == want {
					found = true
					s = &servers[i]
					break
				}
			}
			if !found {
				t.Errorf("reference server %q missing", want)
				continue
			}
		}
		if s.InstallCmd == "" {
			t.Errorf("reference server %q must have an InstallCmd (stdio)", want)
		}
		if s.URL != "" {
			t.Errorf("reference server %q must leave URL empty, got %q", want, s.URL)
		}
	}
}
