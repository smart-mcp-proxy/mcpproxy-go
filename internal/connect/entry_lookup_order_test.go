package connect

import "testing"

// Two entries pointing at this instance (a legacy ?apikey= one under a custom
// key beside the current "mcpproxy" one) must resolve to the same entry on
// every lookup, preferring the default key.
func TestFindEntry_TwoEntriesPointingHere_Deterministic(t *testing.T) {
	svc := NewServiceWithHome("127.0.0.1:8080", "", t.TempDir())

	jsonRaw := []byte(`{"mcpServers": {
		"mcpproxy": {"url": "http://127.0.0.1:8080/mcp"},
		"old-proxy": {"url": "http://127.0.0.1:8080/mcp?apikey=OLD"},
		"another": {"url": "http://127.0.0.1:8080/mcp?apikey=OTHER"}
	}}`)
	tomlRaw := []byte(`
[mcp_servers.mcpproxy]
url = "http://127.0.0.1:8080/mcp"

[mcp_servers.old-proxy]
url = "http://127.0.0.1:8080/mcp?apikey=OLD"

[mcp_servers.another]
url = "http://127.0.0.1:8080/mcp?apikey=OTHER"
`)

	cases := []struct {
		client string
		raw    []byte
	}{
		{"cursor", jsonRaw},
		{"codex", tomlRaw},
	}
	for _, tc := range cases {
		client := FindClient(tc.client)
		if client == nil {
			t.Fatalf("client %q not found", tc.client)
		}
		for i := 0; i < 50; i++ {
			loc, found, parsedOK := svc.findEntryFromBytes(*client, tc.raw)
			if !parsedOK || !found {
				t.Fatalf("%s: found=%v parsedOK=%v", tc.client, found, parsedOK)
			}
			if loc.Name != defaultServerName || !loc.PointsHere {
				t.Fatalf("%s lookup %d: got entry %q (pointsHere=%v), want %q", tc.client, i, loc.Name, loc.PointsHere, defaultServerName)
			}
		}
	}
}
