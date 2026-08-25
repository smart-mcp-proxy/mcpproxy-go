package connect

import (
	"os"
	"path/filepath"
	"testing"
)

// writeClientConfig writes raw config bytes at the client's canonical path.
func writeClientConfig(t *testing.T, homeDir, clientID, contents string) {
	t.Helper()
	cfgPath := ConfigPath(clientID, homeDir)
	if cfgPath == "" {
		t.Fatalf("no config path for %s", clientID)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGetStatus_EndpointMatchThis covers the honest-connected case: the entry's
// URL really is this instance.
func TestGetStatus_EndpointMatchThis(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://127.0.0.1:8080/mcp"}}}`)

	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected {
		t.Fatalf("expected connected")
	}
	if st.EndpointMatch != EndpointMatchThis {
		t.Errorf("endpoint_match = %q, want %q", st.EndpointMatch, EndpointMatchThis)
	}
	if st.RegisteredURL != "http://127.0.0.1:8080/mcp" {
		t.Errorf("registered_url = %q", st.RegisteredURL)
	}
	if st.ProxyURL != "http://127.0.0.1:8080/mcp" {
		t.Errorf("proxy_url = %q", st.ProxyURL)
	}
}

// TestGetStatus_EndpointMatchOther is audit F18's core defect: an entry named
// "mcpproxy" that points at a DIFFERENT instance was reported as a plain
// "Connected", so the UI claimed a link this instance does not have.
func TestGetStatus_EndpointMatchOther(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://127.0.0.1:18412/mcp"}}}`)

	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected {
		t.Fatalf("expected connected (an mcpproxy entry IS present)")
	}
	if st.EndpointMatch != EndpointMatchOther {
		t.Errorf("endpoint_match = %q, want %q", st.EndpointMatch, EndpointMatchOther)
	}
	if st.RegisteredURL != "http://127.0.0.1:18412/mcp" {
		t.Errorf("registered_url = %q, want the other instance's endpoint", st.RegisteredURL)
	}
}

// TestGetStatus_EndpointMatchStripsCredential asserts a legacy ?apikey= entry
// never leaks its query into the status payload.
func TestGetStatus_EndpointMatchStripsCredential(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://127.0.0.1:8080/mcp?apikey=supersecret"}}}`)

	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if st.EndpointMatch != EndpointMatchThis {
		t.Errorf("endpoint_match = %q, want %q (a ?apikey= variant still points here)", st.EndpointMatch, EndpointMatchThis)
	}
	if st.RegisteredURL != "http://127.0.0.1:8080/mcp" {
		t.Errorf("registered_url = %q — the query must be stripped", st.RegisteredURL)
	}
}

// TestGetStatus_EndpointMatchUnknown: a name-only match with no URL-shaped value
// (a stdio command entry) is honestly reported as indeterminate rather than
// silently claimed for this instance.
func TestGetStatus_EndpointMatchUnknown(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"command":"mcpproxy","args":["serve"]}}}`)

	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected {
		t.Fatalf("expected connected")
	}
	if st.EndpointMatch != EndpointMatchUnknown {
		t.Errorf("endpoint_match = %q, want %q", st.EndpointMatch, EndpointMatchUnknown)
	}
	if st.RegisteredURL != "" {
		t.Errorf("registered_url = %q, want empty", st.RegisteredURL)
	}
}

// TestGetStatus_RealMatchBeatsNameOnlyMatch: a config holding BOTH a stale
// name-only entry and a sibling that genuinely points here must resolve to the
// real one, regardless of Go's randomized map iteration order.
func TestGetStatus_RealMatchBeatsNameOnlyMatch(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{
		"mcpproxy":{"url":"http://127.0.0.1:18412/mcp"},
		"mcpproxy-local":{"url":"http://127.0.0.1:8080/mcp"}
	}}`)

	for i := 0; i < 20; i++ {
		st, err := svc.GetStatus("cursor")
		if err != nil {
			t.Fatal(err)
		}
		if st.EndpointMatch != EndpointMatchThis {
			t.Fatalf("iteration %d: endpoint_match = %q, want %q", i, st.EndpointMatch, EndpointMatchThis)
		}
		if st.ServerName != "mcpproxy-local" {
			t.Fatalf("iteration %d: server_name = %q, want mcpproxy-local", i, st.ServerName)
		}
	}
}

// TestGetAllStatus_CarriesProxyURL: the stat-only listing can still name this
// instance's endpoint, because it comes from config, not from a file read.
func TestGetAllStatus_CarriesProxyURL(t *testing.T) {
	svc, _ := testService(t)

	for _, st := range svc.GetAllStatus() {
		if st.ProxyURL != "http://127.0.0.1:8080/mcp" {
			t.Fatalf("%s: proxy_url = %q", st.ID, st.ProxyURL)
		}
		if st.RegisteredURL != "" || st.EndpointMatch != "" {
			t.Fatalf("%s: content-derived fields must stay empty without a read", st.ID)
		}
	}
}
