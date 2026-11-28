package connect

import "testing"

// The name localhost is an alias for the loopback address: a client entry that
// differs from this instance's address only by that name points here.
func TestLoopbackNameAlias(t *testing.T) {
	const base = "http://127.0.0.1:8080/mcp"
	cases := []struct {
		name, u, base string
		want          bool
	}{
		// The exact address is not an alias of itself: the finders match it first.
		{"the address itself", "http://127.0.0.1:8080/mcp", base, false},
		{"localhost alias", "http://localhost:8080/mcp", base, true},
		{"localhost alias with query", "http://localhost:8080/mcp?apikey=k", base, true},
		{"localhost any case", "http://LocalHost:8080/mcp", base, true},
		{"base is localhost, entry is the address", "http://127.0.0.1:8080/mcp", "http://localhost:8080/mcp", true},
		{"base is localhost, entry is the ipv6 address", "http://[::1]:8080/mcp", "http://localhost:8080/mcp", true},
		{"base is ipv6 loopback, entry is localhost", "http://localhost:8080/mcp", "http://[::1]:8080/mcp", true},

		// Two loopback addresses can carry two listeners: only the name is an alias.
		{"ipv6 loopback is not 127.0.0.1", "http://[::1]:8080/mcp", base, false},
		{"other loopback address", "http://127.0.0.2:8080/mcp", base, false},
		{"localhost is not 127.0.0.2", "http://localhost:8080/mcp", "http://127.0.0.2:8080/mcp", false},
		{"bracketed name", "http://[localhost]:8080/mcp", base, false},
		{"bracketed ipv4", "http://[127.0.0.1]:8080/mcp", "http://localhost:8080/mcp", false},
		{"port with a leading zero", "http://localhost:08080/mcp", base, false},
		{"ipv6 without brackets", "http://::1:8080/mcp", "http://localhost:8080/mcp", false},
		{"trailing dot", "http://localhost.:8080/mcp", base, false},
		{"backslash authority", "http://localhost:8080\\@evil.example/mcp", base, false},

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
			if got := loopbackNameAlias(tc.u, tc.base); got != tc.want {
				t.Fatalf("loopbackNameAlias(%q, %q) = %v, want %v", tc.u, tc.base, got, tc.want)
			}
		})
	}
}

// The bridge form: npx runs mcp-remote against the alias.
func TestBridgeDestinationAlias(t *testing.T) {
	const base = "http://127.0.0.1:8080/mcp"
	args := func(a ...interface{}) map[string]interface{} {
		return map[string]interface{}{"command": "npx", "args": a}
	}
	with := func(command string, a ...interface{}) map[string]interface{} {
		return map[string]interface{}{"command": command, "args": a}
	}
	yes := []map[string]interface{}{
		args("-y", "mcp-remote", "http://localhost:8080/mcp"),
		args("mcp-remote", "http://localhost:8080/mcp"),
		args("--yes", "mcp-remote@0.1.30", "http://localhost:8080/mcp", "--header", "X-API-Key: k"),
		args("--yes", "--package", "mcp-remote", "--", "mcp-remote", "http://localhost:8080/mcp"),
		args("-p", "mcp-remote@latest", "mcp-remote", "http://localhost:8080/mcp"),
		with("/opt/homebrew/bin/npx", "-y", "mcp-remote", "http://localhost:8080/mcp"),
		with(`C:\\Program Files\\nodejs\\npx.cmd`, "-y", "mcp-remote", "http://localhost:8080/mcp"),
		with("bunx", "mcp-remote", "http://localhost:8080/mcp"),
	}
	for _, e := range yes {
		if !bridgeDestinationIsAlias(e, base) {
			t.Errorf("not recognised: %v", e["args"])
		}
	}
	no := []map[string]interface{}{
		args("-y", "cowsay", "mcp-remote", "http://localhost:8080/mcp"),                                // another package runs
		args("-y", "mcp-remote", "https://other.example/mcp", "--header", "http://localhost:8080/mcp"), // bound elsewhere
		args("-y", "mcp-remote", "--debug", "http://localhost:8080/mcp"),                               // not the destination position
		args("-y", "mcp-remote-evil", "http://localhost:8080/mcp"),
		args("-y", "mcp-remote"),
		with("echo", "mcp-remote", "http://localhost:8080/mcp"),      // prints its arguments, starts no bridge
		with("", "-y", "mcp-remote", "http://localhost:8080/mcp"),    // no command
		with("node", "mcp-remote", "http://localhost:8080/mcp"),      // runs a file named mcp-remote, not the package
		args("--package", "mcp-remote", "http://localhost:8080/mcp"), // the URL is the executable here, not a destination
		args("--", "cowsay", "mcp-remote", "http://localhost:8080/mcp"),
		args("-y", "cowsay", "hello", "--", "mcp-remote", "http://localhost:8080/mcp"), // "--" belongs to cowsay
		args("-y", "--"),
		args("-p"),
		args("some-server", "http://localhost:8080/mcp"),
		args("-y", "mcp-remote", "http://localhost:9090/mcp"),
		args(),
	}
	for _, e := range no {
		if bridgeDestinationIsAlias(e, base) {
			t.Errorf("recognised: %v", e["args"])
		}
	}
	// The exact matcher is what it was: it does not accept the alias at all.
	if entryPointsToBridge(args("-y", "mcp-remote", "http://localhost:8080/mcp"), base) {
		t.Error("entryPointsToBridge accepted the alias")
	}
}

// Which entry a Connect adopts is unchanged: exact matches only. An entry
// written with localhost under another name is left alone, and an exact match
// is still found whatever sorts before it. (Undo replays this lookup, so it
// must answer the way it did when an earlier Connect ran.)
func TestAdoptionStaysExact(t *testing.T) {
	const base = "http://127.0.0.1:8080/mcp"
	alias := map[string]interface{}{"mine": map[string]interface{}{"url": "http://localhost:8080/mcp"}}
	if name, ok := findEquivalentJSONServerName(alias, base, "mcpproxy"); ok {
		t.Errorf("adopted %q through the localhost alias", name)
	}
	both := map[string]interface{}{
		"a-custom":   map[string]interface{}{"url": "http://localhost:8080/mcp"},
		"z-existing": map[string]interface{}{"url": base},
	}
	if name, ok := findEquivalentJSONServerName(both, base, "mcpproxy"); !ok || name != "z-existing" {
		t.Errorf("findEquivalentJSONServerName = %q, %v; want z-existing, true", name, ok)
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

// An exact entry anywhere in the file wins over an alias, even one under the
// default name that the scan visits first: status, the credential it reads and
// Disconnect all keep pointing at the entry they pointed at before.
func TestGetStatus_ExactEntryWinsOverAlias(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://localhost:8080/mcp"},"z-current":{"url":"http://127.0.0.1:8080/mcp"}}}`)
	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerName != "z-current" || st.EndpointMatch != EndpointMatchThis {
		t.Fatalf("server_name = %q, endpoint_match = %q; want z-current, %q", st.ServerName, st.EndpointMatch, EndpointMatchThis)
	}
}

// With no exact entry, an alias under a custom name beats a default-named
// entry that points somewhere else.
func TestGetStatus_AliasBeatsNameOnly(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mcpproxy":{"url":"http://localhost:9090/mcp"},"mine":{"url":"http://localhost:8080/mcp"}}}`)
	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerName != "mine" || st.EndpointMatch != EndpointMatchThis {
		t.Fatalf("server_name = %q, endpoint_match = %q; want mine, %q", st.ServerName, st.EndpointMatch, EndpointMatchThis)
	}
}

// The legacy ?apikey= form of the exact address is still recognised by the
// finder itself (asserted through production code, not a comparison in the test).
func TestGetStatus_LegacyQueryFormIsThisInstance(t *testing.T) {
	svc, homeDir := testService(t)
	writeClientConfig(t, homeDir, "cursor", `{"mcpServers":{"mine":{"url":"http://127.0.0.1:8080/mcp?apikey=k"}}}`)
	st, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerName != "mine" || st.EndpointMatch != EndpointMatchThis {
		t.Fatalf("server_name = %q, endpoint_match = %q; want mine, %q", st.ServerName, st.EndpointMatch, EndpointMatchThis)
	}
}
