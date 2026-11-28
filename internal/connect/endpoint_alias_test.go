package connect

import "testing"

// localhost, 127.0.0.1 and [::1] name the same machine: a client entry that
// differs from this instance's address only by a loopback alias points here.
func TestEndpointURLMatches(t *testing.T) {
	const base = "http://127.0.0.1:8080/mcp"
	cases := []struct {
		name, u, base string
		want          bool
	}{
		{"exact", "http://127.0.0.1:8080/mcp", base, true},
		{"exact with legacy query", "http://127.0.0.1:8080/mcp?apikey=k", base, true},
		{"localhost alias", "http://localhost:8080/mcp", base, true},
		{"localhost alias with query", "http://localhost:8080/mcp?apikey=k", base, true},
		{"localhost any case", "http://LocalHost:8080/mcp", base, true},
		{"ipv6 loopback alias", "http://[::1]:8080/mcp", base, true},
		{"other loopback address", "http://127.0.0.2:8080/mcp", base, true},
		{"base is localhost, entry is the address", "http://127.0.0.1:8080/mcp", "http://localhost:8080/mcp", true},

		{"other port", "http://localhost:9090/mcp", base, false},
		{"other scheme", "https://localhost:8080/mcp", base, false},
		{"other path", "http://localhost:8080/mcp/s/github", base, false},
		{"trailing slash", "http://localhost:8080/mcp/", base, false},
		{"no path", "http://localhost:8080", base, false},
		{"userinfo", "http://user@localhost:8080/mcp", base, false},
		{"fragment", "http://localhost:8080/mcp#x", base, false},
		{"lookalike host", "http://localhost.example.com:8080/mcp", base, false},
		{"suffix lookalike", "http://notlocalhost:8080/mcp", base, false},
		{"lan address", "http://192.168.1.10:8080/mcp", base, false},
		{"no port", "http://localhost/mcp", base, false},
		{"not a url", "localhost:8080/mcp", base, false},
		{"empty", "", base, false},
		{"base is not loopback", "http://localhost:8080/mcp", "http://192.168.1.10:8080/mcp", false},
		{"base is a wildcard", "http://localhost:8080/mcp", "http://0.0.0.0:8080/mcp", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endpointURLMatches(tc.u, tc.base); got != tc.want {
				t.Fatalf("endpointURLMatches(%q, %q) = %v, want %v", tc.u, tc.base, got, tc.want)
			}
		})
	}
}

// Every place that decides "this entry points at this instance" accepts the alias.
func TestLoopbackAliasIsRecognisedEverywhere(t *testing.T) {
	const base = "http://127.0.0.1:8080/mcp"
	s := NewService("127.0.0.1:8080", "k")
	if !s.urlPointsHere("http://localhost:8080/mcp", base, "gemini") {
		t.Error("urlPointsHere: localhost entry not recognised")
	}
	bridge := map[string]interface{}{"command": "npx", "args": []interface{}{"-y", "mcp-remote", "http://localhost:8080/mcp"}}
	if !entryPointsToBridge(bridge, base) {
		t.Error("entryPointsToBridge: localhost bridge not recognised")
	}
	servers := map[string]interface{}{"mine": map[string]interface{}{"httpUrl": "http://localhost:8080/mcp"}}
	if name, ok := findEquivalentJSONServerName(servers, base, "mcpproxy"); !ok || name != "mine" {
		t.Errorf("findEquivalentJSONServerName = %q, %v; want mine, true", name, ok)
	}
}

// The status row of a client whose entry says localhost reads "this instance".
func TestGetStatus_LoopbackAliasIsThisInstance(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://localhost:8080/mcp"}}}`)
	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected || st.EndpointMatch != EndpointMatchThis {
		t.Fatalf("connected = %v, endpoint_match = %q; want true, %q", st.Connected, st.EndpointMatch, EndpointMatchThis)
	}
	// Another port on localhost is still another instance.
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://localhost:9090/mcp"}}}`)
	st, err = svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if st.EndpointMatch != EndpointMatchOther {
		t.Fatalf("endpoint_match = %q, want %q", st.EndpointMatch, EndpointMatchOther)
	}
}
