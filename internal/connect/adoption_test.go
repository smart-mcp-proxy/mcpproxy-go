package connect

import (
	"encoding/json"
	"strings"
	"testing"
)

// Adoption resolution must be DETERMINISTIC and resolved exactly ONCE per
// operation (Spec 091 FR-005). The precondition token promises it hashes "the
// entry the write would actually replace"; if the preview, the token check and
// the write each pick a different equivalent entry out of a randomly-ordered Go
// map, that promise is void — the token covers one entry while the write
// deletes another.

// TestFindEquivalentJSONServerName_Deterministic pins the tie-break: an exact
// name match wins, otherwise the lowest name in sorted order. Repeated over a
// map whose iteration order Go deliberately randomizes.
func TestFindEquivalentJSONServerName_Deterministic(t *testing.T) {
	const baseURL = "http://127.0.0.1:8080/mcp"

	t.Run("two url-equivalent entries resolve to the sorted-first name", func(t *testing.T) {
		for i := 0; i < 200; i++ {
			serversMap := map[string]interface{}{
				"b-legacy": map[string]interface{}{"url": baseURL + "?apikey=old"},
				"a-clean":  map[string]interface{}{"url": baseURL},
				"unrelated": map[string]interface{}{
					"url": "http://127.0.0.1:9999/mcp",
				},
			}
			got, found := findEquivalentJSONServerName(serversMap, baseURL, "mcpproxy")
			if !found {
				t.Fatal("expected a match")
			}
			if got != "a-clean" {
				t.Fatalf("iteration %d: resolved %q, want the deterministic sorted-first match %q", i, got, "a-clean")
			}
		}
	})

	t.Run("the requested name wins over a url-equivalent entry", func(t *testing.T) {
		for i := 0; i < 200; i++ {
			serversMap := map[string]interface{}{
				"mcpproxy": map[string]interface{}{"url": "http://127.0.0.1:9999/mcp"},
				"legacy":   map[string]interface{}{"url": baseURL},
			}
			got, found := findEquivalentJSONServerName(serversMap, baseURL, "mcpproxy")
			if !found {
				t.Fatal("expected a match")
			}
			if got != "mcpproxy" {
				t.Fatalf("iteration %d: resolved %q, want the exact requested name (it is what the token and the summary cover)", i, got)
			}
		}
	})
}

// TestConnect_OpenCodeAdoptionNeverDeletesAnUnnamedEntry is scenario B of the
// nondeterminism: the requested name already exists, so preview/token/summary
// cover exactly that key — yet the write's own independent adoption lookup used
// to reach a URL-equivalent entry under another key and silently delete it.
func TestConnect_OpenCodeAdoptionNeverDeletesAnUnnamedEntry(t *testing.T) {
	for i := 0; i < 40; i++ {
		svc, home := testService(t)
		cfgPath := ConfigPath("opencode", home)
		writeFileT(t, cfgPath, `{"mcp":{`+
			`"mcpproxy":{"type":"remote","url":"http://127.0.0.1:9999/mcp"},`+
			`"legacy":{"type":"remote","url":"http://127.0.0.1:8080/mcp?apikey=old"}}}`)

		preview, err := svc.Preview("opencode", "mcpproxy")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if preview.ExistingEntrySummary == nil || preview.ExistingEntrySummary.EntryName != "mcpproxy" {
			t.Fatalf("iteration %d: preview must name the exact-name entry, got %+v", i, preview.ExistingEntrySummary)
		}

		res, err := svc.ConnectWithPrecondition("opencode", "mcpproxy", true, preview.PreconditionToken)
		if err != nil {
			t.Fatalf("ConnectWithPrecondition: %v", err)
		}
		if !res.Success {
			t.Fatalf("iteration %d: expected success, got %+v", i, res)
		}
		if !strings.Contains(readConfigT(t, cfgPath), `"legacy"`) {
			t.Fatalf("iteration %d: the write deleted an entry the token never hashed and the summary never named:\n%s",
				i, readConfigT(t, cfgPath))
		}
	}
}

// TestConnect_OpenCodeAdoptionMatchesThePreviewedEntry is scenario A: two
// URL-equivalent entries and a requested name that matches neither. The write
// must adopt exactly the entry the preview resolved — never flip to the other
// one (which would be a spurious precondition_failed, or a silent delete of an
// entry the user was never shown).
func TestConnect_OpenCodeAdoptionMatchesThePreviewedEntry(t *testing.T) {
	for i := 0; i < 40; i++ {
		svc, home := testService(t)
		cfgPath := ConfigPath("opencode", home)
		writeFileT(t, cfgPath, `{"mcp":{`+
			`"a-clean":{"type":"remote","url":"http://127.0.0.1:8080/mcp"},`+
			`"b-legacy":{"type":"remote","url":"http://127.0.0.1:8080/mcp?apikey=old"}}}`)

		preview, err := svc.Preview("opencode", "proxy")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if preview.ExistingEntrySummary == nil {
			t.Fatalf("iteration %d: expected an adopted existing entry", i)
		}
		adopted := preview.ExistingEntrySummary.EntryName
		if adopted != "a-clean" {
			t.Fatalf("iteration %d: preview adopted %q, want the deterministic %q", i, adopted, "a-clean")
		}

		res, err := svc.ConnectWithPrecondition("opencode", "proxy", true, preview.PreconditionToken)
		if err != nil {
			t.Fatalf("ConnectWithPrecondition: %v", err)
		}
		if !res.Success {
			t.Fatalf("iteration %d: a token minted from an unchanged config must not fail: %+v", i, res)
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(readConfigT(t, cfgPath)), &data); err != nil {
			t.Fatal(err)
		}
		servers, _ := data["mcp"].(map[string]interface{})
		if _, gone := servers[adopted]; gone {
			t.Fatalf("iteration %d: the adopted entry %q should have been replaced", i, adopted)
		}
		if _, kept := servers["b-legacy"]; !kept {
			t.Fatalf("iteration %d: the write deleted %q, which the preview never named:\n%s",
				i, "b-legacy", readConfigT(t, cfgPath))
		}
		if _, written := servers["proxy"]; !written {
			t.Fatalf("iteration %d: expected the new entry under the requested name", i)
		}
	}
}

// TestUndoReplay_AdoptionIsDeterministic covers undo.go's use of the same
// matcher: the replay must reconstruct the same bytes the write produced, so it
// must reach the same adoption decision every time.
func TestUndoReplay_AdoptionIsDeterministic(t *testing.T) {
	svc, _ := testService(t)
	client := FindClient("opencode")
	backup := []byte(`{"mcp":{` +
		`"a-clean":{"type":"remote","url":"http://127.0.0.1:8080/mcp"},` +
		`"b-legacy":{"type":"remote","url":"http://127.0.0.1:8080/mcp?apikey=old"}}}`)

	first, err := svc.replayConnectWrite(client, "proxy", backup)
	if err != nil {
		t.Fatalf("replayConnectWrite: %v", err)
	}
	for i := 0; i < 100; i++ {
		again, err := svc.replayConnectWrite(client, "proxy", backup)
		if err != nil {
			t.Fatalf("replayConnectWrite: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("iteration %d: replay is nondeterministic, so undo would refuse a file it wrote itself:\n%s\n--- vs ---\n%s",
				i, first, again)
		}
	}
}
