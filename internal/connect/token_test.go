package connect

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The precondition token binds a rendered preview to the exact pre-write state
// it described (Spec 091 FR-005). It must be:
//
//   - KEYED (HMAC-SHA256 with a per-core-instance in-memory key), so it is not
//     an offline confirmation oracle for a masked or weak secret whose preimage
//     is otherwise fully known;
//   - computed over a CANONICAL, LENGTH-PREFIXED encoding, so no two distinct
//     states can encode to the same byte string;
//   - sensitive to every drift class: file existence, the resolved (possibly
//     adopted) entry's presence and raw value — including values the sanitized
//     summary deliberately hides — and the pending entry the proxy would write.

var (
	tokenKeyA = []byte("key-a-0123456789abcdef0123456789")
	tokenKeyB = []byte("key-b-0123456789abcdef0123456789")
)

func TestDerivePreconditionToken_DeterministicPerKey(t *testing.T) {
	raw := json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)
	pending := json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)

	first := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "mcpproxy", RawResolvedEntry: raw, PendingEntry: pending})
	second := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "mcpproxy", RawResolvedEntry: raw, PendingEntry: pending})
	if first != second {
		t.Fatalf("token not deterministic: %q vs %q", first, second)
	}
	if first == "" {
		t.Fatal("token must not be empty")
	}
	// HMAC-SHA256, hex-encoded.
	if len(first) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars (HMAC-SHA256)", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
}

func TestDerivePreconditionToken_DifferentKeyDifferentToken(t *testing.T) {
	raw := json.RawMessage(`{"url":"http://127.0.0.1:8080/mcp"}`)
	pending := json.RawMessage(`{"url":"http://127.0.0.1:8080/mcp"}`)

	a := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "mcpproxy", RawResolvedEntry: raw, PendingEntry: pending})
	b := DerivePreconditionToken(tokenKeyB, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "mcpproxy", RawResolvedEntry: raw, PendingEntry: pending})
	if a == b {
		t.Fatal("tokens must differ under different keys (the key is what makes the token non-forgeable)")
	}
}

func TestDerivePreconditionToken_DistinctPerDriftClass(t *testing.T) {
	base := func() string {
		return DerivePreconditionToken(tokenKeyA, PreconditionState{
			ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
			ResolvedEntryName: "mcpproxy", RawResolvedEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"old"}}`), PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)})
	}

	cases := []struct {
		name string
		got  string
	}{
		{
			name: "file existence flipped",
			got: DerivePreconditionToken(tokenKeyA, PreconditionState{
				ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: false,
				ResolvedEntryName: "mcpproxy", RawResolvedEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"old"}}`), PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)}),
		},
		{
			name: "resolved entry absent",
			got: DerivePreconditionToken(tokenKeyA, PreconditionState{
				ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
				ResolvedEntryName: "", RawResolvedEntry: nil, PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)}),
		},
		{
			name: "resolved entry lives under an adopted key",
			got: DerivePreconditionToken(tokenKeyA, PreconditionState{
				ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
				ResolvedEntryName: "proxy-alt", RawResolvedEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"old"}}`), PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)}),
		},
		{
			name: "masked credential value of the existing entry changed",
			got: DerivePreconditionToken(tokenKeyA, PreconditionState{
				ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
				ResolvedEntryName: "mcpproxy", RawResolvedEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"rotated"}}`), PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)}),
		},
		{
			name: "pending entry changed (proxy-side drift)",
			got: DerivePreconditionToken(tokenKeyA, PreconditionState{
				ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
				ResolvedEntryName: "mcpproxy", RawResolvedEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"old"}}`), PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:9090/mcp"}`)}),
		},
		{
			name: "config path changed",
			got: DerivePreconditionToken(tokenKeyA, PreconditionState{
				ConfigPath: "/other.json", Requested: "mcpproxy", FileExists: true,
				ResolvedEntryName: "mcpproxy", RawResolvedEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"old"}}`), PendingEntry: json.RawMessage(`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`)}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == base() {
				t.Fatalf("token must change when %s", tc.name)
			}
		})
	}
}

// TestDerivePreconditionToken_LengthPrefixedEncoding pins the canonical encoding:
// with a naive separator-free or delimiter-joined concatenation, shifting a byte
// from one field to the next produces the same preimage. Length prefixes make
// that impossible.
func TestDerivePreconditionToken_LengthPrefixedEncoding(t *testing.T) {
	pending := json.RawMessage(`{}`)
	a := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "ab", RawResolvedEntry: json.RawMessage(`"c"`), PendingEntry: pending})
	b := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "a", RawResolvedEntry: json.RawMessage(`b"c"`), PendingEntry: pending})
	if a == b {
		t.Fatal("field boundaries are ambiguous: encoding is not length-prefixed")
	}

	c := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: ".jsonmcpproxy", RawResolvedEntry: nil, PendingEntry: pending})
	d := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "mcpproxy", RawResolvedEntry: nil, PendingEntry: pending})
	if c == d {
		t.Fatal("path/name boundary is ambiguous: encoding is not length-prefixed")
	}
}

// TestDerivePreconditionToken_CarriesNoSecretSubstring: the token is a
// fixed-width HMAC digest, so no fragment of the hashed state — least of all a
// credential — can survive into it.
func TestDerivePreconditionToken_CarriesNoSecretSubstring(t *testing.T) {
	const secret = "SUPER-SECRET-CREDENTIAL"
	token := DerivePreconditionToken(tokenKeyA, PreconditionState{
		ConfigPath: "/cfg.json", Requested: "mcpproxy", FileExists: true,
		ResolvedEntryName: "mcpproxy",
		RawResolvedEntry:  json.RawMessage(`{"headers":{"X-API-Key":"` + secret + `"}}`),
		PendingEntry:      json.RawMessage(`{"headers":{"X-API-Key":"` + secret + `"}}`)})
	if strings.Contains(token, secret) {
		t.Fatalf("token leaked the secret: %s", token)
	}
	for _, frag := range []string{"SUPER", "SECRET", "CREDENTIAL", "X-API-Key"} {
		if strings.Contains(token, frag) {
			t.Fatalf("token leaked %q: %s", frag, token)
		}
	}
}

// --- Service-level wiring: the preview carries a token over its own state ---

func TestPreview_PreconditionToken_StableForUnchangedState(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	seedClientConfig(t, home, "claude-code")

	first := previewToken(t, svc, "claude-code")
	second := previewToken(t, svc, "claude-code")
	if first != second {
		t.Fatalf("token must be stable while nothing drifts: %q vs %q", first, second)
	}
	if first == "" {
		t.Fatal("preview must always carry a precondition token")
	}
}

func TestPreview_PreconditionToken_PerInstanceKey(t *testing.T) {
	// Tokens are single-session by design: a second core instance must not
	// accept a token minted by the first.
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	seedClientConfig(t, home, "claude-code")

	a := NewServiceWithHome("127.0.0.1:8080", "", home)
	b := NewServiceWithHome("127.0.0.1:8080", "", home)
	if previewToken(t, a, "claude-code") == previewToken(t, b, "claude-code") {
		t.Fatal("two service instances must derive tokens under different keys")
	}
}

func TestPreview_PreconditionToken_FileAndEntryDrift(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	cfgPath := ConfigPath("claude-code", home)

	absent := previewToken(t, svc, "claude-code")

	seedClientConfig(t, home, "claude-code")
	present := previewToken(t, svc, "claude-code")
	if present == absent {
		t.Fatal("token must change when the config file appears")
	}

	writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"first"}}}}`)
	withEntry := previewToken(t, svc, "claude-code")
	if withEntry == present {
		t.Fatal("token must change when the resolved entry appears")
	}

	// A change the sanitized summary deliberately hides (header VALUE) must
	// still invalidate the token — the summary is display-only.
	writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"second"}}}}`)
	if rotated := previewToken(t, svc, "claude-code"); rotated == withEntry {
		t.Fatal("token must change when a hidden (masked) value of the existing entry changes")
	}
}

func TestPreview_PreconditionToken_AdoptedEntryDrift(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	cfgPath := ConfigPath("opencode", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFileT(t, cfgPath, `{"mcp":{"proxy-alt":{"type":"remote","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"first"}}}}`)
	before := previewToken(t, svc, "opencode")

	// The adopted entry lives under a DIFFERENT key than the requested one; a
	// change to it is exactly the drift class an unresolved serversMap[name]
	// lookup would miss.
	writeFileT(t, cfgPath, `{"mcp":{"proxy-alt":{"type":"remote","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"second"}}}}`)
	if after := previewToken(t, svc, "opencode"); after == before {
		t.Fatal("token must change when the ADOPTED entry changes under its own key")
	}
}

func TestPreview_PreconditionToken_PendingEntryDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	seedClientConfig(t, home, "claude-code")

	listenAddr, apiKey, requireAuth := "127.0.0.1:8080", "key-one", false
	svc := NewServiceWithHome(listenAddr, apiKey, home).
		WithConfigProvider(func() (string, string, bool) { return listenAddr, apiKey, requireAuth })

	base := previewToken(t, svc, "claude-code")

	// require_mcp_auth toggled on: the pending entry gains the credential —
	// the FR-004 notice the user never saw. The token must invalidate.
	requireAuth = true
	authOn := previewToken(t, svc, "claude-code")
	if authOn == base {
		t.Fatal("token must change when require_mcp_auth flips the pending entry")
	}

	// Credential rotated while the preview was on screen.
	apiKey = "key-two"
	rotated := previewToken(t, svc, "claude-code")
	if rotated == authOn {
		t.Fatal("token must change when the API key rotates")
	}

	// Listen address changed: the entry would point somewhere else.
	listenAddr = "127.0.0.1:9090"
	if moved := previewToken(t, svc, "claude-code"); moved == rotated {
		t.Fatal("token must change when the listen address changes")
	}
}

// previewToken runs a preview and returns its precondition token.
func previewToken(t *testing.T, svc *Service, clientID string) string {
	t.Helper()
	preview, err := svc.Preview(clientID, "mcpproxy")
	if err != nil {
		t.Fatalf("Preview(%s): %v", clientID, err)
	}
	return preview.PreconditionToken
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPreview_PreconditionToken_NonObjectEntryDrift closes the drift class the
// map projection hid: a resolved entry whose value is NOT an object (a
// hand-edited string, number or array) used to canonicalize to the constant
// "null", so two entirely different values produced byte-identical tokens and
// the write proceeded over state the user never saw (FR-005).
func TestPreview_PreconditionToken_NonObjectEntryDrift(t *testing.T) {
	svc, home := serviceWithKey(t, "")
	cfgPath := ConfigPath("claude-code", home)

	values := []string{
		`"http://old-endpoint"`,
		`"http://new-endpoint"`,
		`["http://a"]`,
		`["http://b"]`,
		`42`,
		`null`,
		`{"type":"http","url":"http://127.0.0.1:8080/mcp"}`,
	}
	seen := map[string]string{}
	for _, value := range values {
		writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":`+value+`}}`)
		token := previewToken(t, svc, "claude-code")
		if other, clash := seen[token]; clash {
			t.Fatalf("entry values %s and %s produce the SAME token — drift between them is invisible", other, value)
		}
		seen[token] = value
	}

	// And an absent entry must still be distinguishable from a present one
	// whose value happens to be JSON null.
	writeFileT(t, cfgPath, `{"mcpServers":{}}`)
	absentEntry := previewToken(t, svc, "claude-code")
	writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":null}}`)
	if nullEntry := previewToken(t, svc, "claude-code"); nullEntry == absentEntry {
		t.Fatal("a present entry valued null must not hash like an absent one")
	}
}

// The write side of the same class: a valid token minted over a non-object
// entry must be refused once that value changes — force must not rescue it.
func TestConnectWithPrecondition_NonObjectEntryDriftRefuses(t *testing.T) {
	t.Run("array value changed", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("claude-code", home)
		writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":["http://a"]}}`)
		preview, err := svc.Preview("claude-code", "mcpproxy")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if !preview.EntryExists {
			t.Fatal("a present non-object value is still an existing entry")
		}

		const drifted = `{"mcpServers":{"mcpproxy":["http://totally-different"]}}`
		writeFileT(t, cfgPath, drifted)

		res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, preview.PreconditionToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertPreconditionRefusal(t, res, cfgPath, drifted)
	})

	t.Run("string value changed", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("claude-code", home)
		writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":"http://old-endpoint"}}`)
		preview, err := svc.Preview("claude-code", "mcpproxy")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}

		const drifted = `{"mcpServers":{"mcpproxy":"http://new-endpoint"}}`
		writeFileT(t, cfgPath, drifted)

		res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, preview.PreconditionToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertPreconditionRefusal(t, res, cfgPath, drifted)
	})

	t.Run("an unchanged non-object entry still writes", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("claude-code", home)
		writeFileT(t, cfgPath, `{"mcpServers":{"mcpproxy":"http://old-endpoint"}}`)
		preview, err := svc.Preview("claude-code", "mcpproxy")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, preview.PreconditionToken)
		if err != nil {
			t.Fatalf("ConnectWithPrecondition: %v", err)
		}
		if !res.Success {
			t.Fatalf("an unchanged config must still write, got %+v", res)
		}
	})
}

// TestPreview_NonObjectServersSection_IsMalformed closes a DIFFERENT drift
// class than TestPreview_PreconditionToken_NonObjectEntryDrift above: that one
// covers the individual ENTRY (data["mcpServers"]["mcpproxy"]) holding a
// non-object value. This one covers the SERVERS SECTION ITSELF
// (data["mcpServers"], or data["mcp_servers"] for Codex/TOML) holding a
// non-object value — a string, number, array or bool from a hand-edited
// config.
//
// resolveExistingEntry used to type-assert data[client.ServerKey].(map[string
// ]interface{}) and, on failure, report "no servers section" — indistinguishable
// from the key being absent entirely. That collapsed every non-object section
// value into the same "create" classification, so the precondition token (which
// only hashes the RESOLVED ENTRY, never the section's own raw value/type) could
// not detect the section's value changing between preview and write, and the
// write silently replaced it with a fresh map. The fix treats "key present but
// not an object" as malformed — a distinct, refusable state — rather than
// falling through to "create".
func TestPreview_NonObjectServersSection_IsMalformed(t *testing.T) {
	t.Run("JSON client (claude-code)", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("claude-code", home)
		writeFileT(t, cfgPath, `{"mcpServers":"old"}`)

		preview, err := svc.Preview("claude-code", "mcpproxy")
		if err != nil {
			t.Fatalf("Preview should not hard-error on a non-object servers section: %v", err)
		}
		if preview.AccessState != accessMalformed {
			t.Fatalf("expected access_state=%q for a non-object servers section, got %q", accessMalformed, preview.AccessState)
		}
	})

	t.Run("TOML client (codex)", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("codex", home)
		writeFileT(t, cfgPath, `mcp_servers = "old"`+"\n")

		preview, err := svc.Preview("codex", "mcpproxy")
		if err != nil {
			t.Fatalf("Preview should not hard-error on a non-object servers section: %v", err)
		}
		if preview.AccessState != accessMalformed {
			t.Fatalf("expected access_state=%q for a non-object servers section, got %q", accessMalformed, preview.AccessState)
		}
	})
}

// TestConnectWithPrecondition_NonObjectServersSection_RefusesDrift is the exact
// repro from the cross-model review of PR #1339: write a config whose servers
// section is a non-object value, preview it (which used to report "no existing
// entry, this will create one" and mint a token blind to the section's value),
// change the section's value externally, then submit connect with the stale
// token. Before the fix this SUCCEEDED and silently destroyed the "new" value;
// it must now refuse.
func TestConnectWithPrecondition_NonObjectServersSection_RefusesDrift(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	writeFileT(t, cfgPath, `{"mcpServers":"old"}`)

	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.EntryExists {
		t.Fatal("a malformed section must not report an entry that would be overwritten")
	}

	const drifted = `{"mcpServers":"new"}`
	writeFileT(t, cfgPath, drifted)

	res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, preview.PreconditionToken)
	if err == nil {
		t.Fatalf("expected a refusal error, got res=%+v err=nil", res)
	}
	if res != nil {
		t.Fatalf("expected a nil result alongside the refusal error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "mcpServers") || !strings.Contains(err.Error(), "not a JSON object") {
		t.Fatalf("expected the refusal to name the section and explain why, got: %v", err)
	}
	if got := readConfigT(t, cfgPath); got != drifted {
		t.Fatalf("config must be untouched after a refusal:\n got:  %s\n want: %s", got, drifted)
	}
	if n := backupCount(t, cfgPath); n != 0 {
		t.Fatalf("a refused write must not create a backup, found %d", n)
	}
}

// TestConnectWithPrecondition_NonObjectServersSection_RaceIsClosedAtTheWriterRead
// proves the guard is not a single upstream check that a concurrent external
// edit could slip past (the TOCTOU a naive "check preWriteState, then write"
// design would have): the file is OBJECT-shaped on the FIRST read (matching
// what preWriteState/the precondition resolution sees) and is mutated to a
// non-object section before the SECOND, independent read the writer itself
// performs. The write must still refuse and must not touch the file — the
// authoritative check has to live at the actual point of mutation, using
// whatever was read there, not a value resolved earlier in the call.
func TestConnectWithPrecondition_NonObjectServersSection_RaceIsClosedAtTheWriterRead(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	const objectShaped = `{"mcpServers":{}}`
	const racedNonObject = `{"mcpServers":"raced-in-between-reads"}`
	writeFileT(t, cfgPath, objectShaped)

	reads := 0
	svc.setReadFile(func(path string) ([]byte, error) {
		reads++
		if reads == 1 {
			// The read inside ConnectWithPrecondition's own preWriteState call:
			// still object-shaped, so no upstream check (if one existed) would fire.
			return []byte(objectShaped), nil
		}
		// Every subsequent read — including the writer's own — observes the file
		// AFTER the simulated concurrent edit.
		return []byte(racedNonObject), nil
	})

	res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, "")
	// Exactly 2 reads for this client/path: preWriteState's (no jsonc guard for
	// claude-code, no precondition token to additionally resolve) and
	// connectJSON's own readOrCreateJSON. A count outside this range would mean
	// the test's own premise — "read #1 sees the pre-race state, read #2+ sees
	// the raced-in one" — no longer matches what actually ran.
	if reads != 2 {
		t.Fatalf("expected exactly 2 reads (preWriteState + the writer's own), got %d", reads)
	}
	if err == nil {
		t.Fatalf("expected the writer's own read to catch the raced-in non-object section, got res=%+v err=nil", res)
	}
	if res != nil {
		t.Fatalf("expected a nil result alongside the refusal error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "mcpServers") || !strings.Contains(err.Error(), "not a JSON object") {
		t.Fatalf("expected the refusal to name the section and explain why, got: %v", err)
	}
	if got := readConfigT(t, cfgPath); got != objectShaped {
		t.Fatalf("the ON-DISK file (never touched by the mocked reads) must be untouched after a refusal:\n got:  %s\n want: %s", got, objectShaped)
	}
	if n := backupCount(t, cfgPath); n != 0 {
		t.Fatalf("a refused write must not create a backup, found %d", n)
	}
}

// TestConnectWithPrecondition_NonObjectServersSection_RaceIsClosedAtThePreBackupCheck
// closes the must-fix round-3 cross-model review found: connectJSON/
// connectTOML's OWN read (the "writer's own read" the previous test proves is
// authoritative) is STILL not adjacent to the actual write — backupFile
// performs real file I/O in between, widening the window in which an
// external process can replace the servers section with a non-object value
// AFTER the writer already decided it was safe to proceed. Repro: both
// preWriteState's read and connectJSON's readOrCreateJSON read see an
// OBJECT-shaped section (so the earlier, top-of-function check passes
// cleanly); only the pre-backup check's read observes the section having
// been replaced with a non-object value in between. The write must still
// refuse, before any backup is created.
func TestConnectWithPrecondition_NonObjectServersSection_RaceIsClosedAtThePreBackupCheck(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	const objectShaped = `{"mcpServers":{}}`
	const racedNonObject = `{"mcpServers":"raced-in-before-backup"}`
	writeFileT(t, cfgPath, objectShaped)

	reads := 0
	svc.setReadFile(func(path string) ([]byte, error) {
		reads++
		if reads <= 2 {
			// preWriteState's read (#1) and connectJSON's own readOrCreateJSON
			// read (#2): both still object-shaped, so the top-of-function check
			// passes and the function proceeds toward the pre-backup check.
			return []byte(objectShaped), nil
		}
		// The THIRD read — refuseIfServersSectionRaced, immediately before
		// backupFile — observes the file AFTER the simulated concurrent edit.
		return []byte(racedNonObject), nil
	})

	res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, "")
	if reads != 3 {
		t.Fatalf("expected exactly 3 reads (preWriteState + the writer's own + the pre-backup check), got %d", reads)
	}
	if err == nil {
		t.Fatalf("expected the pre-backup check to catch the raced-in non-object section, got res=%+v err=nil", res)
	}
	if res != nil {
		t.Fatalf("expected a nil result alongside the refusal error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "mcpServers") {
		t.Fatalf("expected the refusal to name the section, got: %v", err)
	}
	if got := readConfigT(t, cfgPath); got != objectShaped {
		t.Fatalf("the ON-DISK file (never touched by the mocked reads) must be untouched after a refusal:\n got:  %s\n want: %s", got, objectShaped)
	}
	if n := backupCount(t, cfgPath); n != 0 {
		t.Fatalf("a refused write must not create a backup — this check runs BEFORE backupFile, found %d", n)
	}
}

// TestConnectWithPrecondition_NonObjectServersSection_RaceIsClosedAfterBackup
// closes the must-fix round-4 cross-model review found in the fix above:
// backupFile performs real Stat/Open/copy I/O — genuinely slow enough to
// race in practice — so a change landing DURING that backup (i.e. AFTER the
// pre-backup check already passed) was still able to slip through to
// atomicWriteFile undetected. Repro: preWriteState's read, connectJSON's own
// read, AND the pre-backup check's read all see an OBJECT-shaped section (so
// backupFile actually runs and a backup file IS created — that's expected
// and consistent with how a later atomicWriteFile failure already behaves in
// this codebase); only the THIRD, post-backup/pre-write check's read
// observes the section having been replaced with a non-object value. The
// write must still refuse, and the on-disk config must be untouched (the
// backup file's existence does not imply the config itself was mutated).
func TestConnectWithPrecondition_NonObjectServersSection_RaceIsClosedAfterBackup(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	const objectShaped = `{"mcpServers":{}}`
	const racedNonObject = `{"mcpServers":"raced-in-during-backup-io"}`
	writeFileT(t, cfgPath, objectShaped)

	reads := 0
	svc.setReadFile(func(path string) ([]byte, error) {
		reads++
		if reads <= 3 {
			// preWriteState (#1), connectJSON's own read (#2), and the
			// pre-backup check (#3): all still object-shaped, so backupFile
			// actually runs.
			return []byte(objectShaped), nil
		}
		// The FOURTH read — refuseIfServersSectionRaced, immediately before
		// atomicWriteFile — observes the file AFTER the simulated edit landing
		// during backupFile's own I/O.
		return []byte(racedNonObject), nil
	})

	res, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, "")
	if reads != 4 {
		t.Fatalf("expected exactly 4 reads (preWriteState + the writer's own + the pre-backup check + the post-backup check), got %d", reads)
	}
	if err == nil {
		t.Fatalf("expected the post-backup check to catch the raced-in non-object section, got res=%+v err=nil", res)
	}
	if res != nil {
		t.Fatalf("expected a nil result alongside the refusal error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "mcpServers") {
		t.Fatalf("expected the refusal to name the section, got: %v", err)
	}
	if got := readConfigT(t, cfgPath); got != objectShaped {
		t.Fatalf("the ON-DISK config file (never touched by the mocked reads) must be untouched after a refusal:\n got:  %s\n want: %s", got, objectShaped)
	}
	// backupFile runs BEFORE this refusal, so — unlike the pre-backup-check
	// test above — a backup IS expected here; its existence must not be
	// confused with the config itself having been mutated (asserted above).
	if n := backupCount(t, cfgPath); n != 1 {
		t.Fatalf("expected exactly 1 backup (created before the post-backup check refused), got %d", n)
	}
}

// TestConnect_NonObjectServersSection_RefusesWithoutToken proves the guard does
// not depend on the precondition-token flow: a tokenless Connect() call (the
// Web UI / CLI's existing behavior, Spec 091 contracts §2) must also refuse
// rather than silently discarding the section's value, for both JSON and TOML
// clients.
func TestConnect_NonObjectServersSection_RefusesWithoutToken(t *testing.T) {
	t.Run("JSON client (vscode)", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("vscode", home)
		const original = `{"servers":["not","an","object"]}`
		writeFileT(t, cfgPath, original)

		res, err := svc.Connect("vscode", "mcpproxy", true)
		if err == nil {
			t.Fatalf("expected a refusal error, got res=%+v err=nil", res)
		}
		if res != nil {
			t.Fatalf("expected a nil result alongside the refusal error, got %+v", res)
		}
		if got := readConfigT(t, cfgPath); got != original {
			t.Fatalf("config must be untouched after a refusal:\n got:  %s\n want: %s", got, original)
		}
		if n := backupCount(t, cfgPath); n != 0 {
			t.Fatalf("a refused write must not create a backup, found %d", n)
		}
	})

	t.Run("TOML client (codex)", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("codex", home)
		const original = "mcp_servers = 42\n"
		writeFileT(t, cfgPath, original)

		res, err := svc.Connect("codex", "mcpproxy", true)
		if err == nil {
			t.Fatalf("expected a refusal error, got res=%+v err=nil", res)
		}
		if res != nil {
			t.Fatalf("expected a nil result alongside the refusal error, got %+v", res)
		}
		if got := readConfigT(t, cfgPath); got != original {
			t.Fatalf("config must be untouched after a refusal:\n got:  %s\n want: %s", got, original)
		}
		if n := backupCount(t, cfgPath); n != 0 {
			t.Fatalf("a refused write must not create a backup, found %d", n)
		}
	})
}

// TestConnect_NullTopLevelDocument_DoesNotPanic pins a crash the cross-model
// review of this fix surfaced: a config file containing exactly the JSON
// literal `null` (or a TOML document that otherwise decodes to a nil map)
// decodes successfully with a NIL top-level map — encoding/json leaves the
// unmarshal target untouched for a JSON null, it is not an error. Before the
// fix, readOrCreateJSON/readOrCreateTOML returned that nil map unchanged, and
// connectJSON/connectTOML's final `data[serversKey] = serversMap` assignment
// panicked with "assignment to entry in nil map" — a DoS reachable through the
// plain tokenless Connect() path with no precondition token involved at all.
func TestConnect_NullTopLevelDocument_DoesNotPanic(t *testing.T) {
	t.Run("JSON client (claude-code)", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("claude-code", home)
		writeFileT(t, cfgPath, "null")

		res, err := svc.Connect("claude-code", "mcpproxy", false)
		if err != nil {
			t.Fatalf("Connect must not error on a null top-level document, got: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected a null document to be treated as an empty config, got %+v", res)
		}
	})

	t.Run("TOML client (codex)", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("codex", home)
		// TOML has no top-level null literal; an empty file is the closest
		// equivalent and already exercised by TestConnect_Codex_NewFile, but
		// pin it here too as a defense-in-depth regression guard alongside the
		// JSON case above.
		writeFileT(t, cfgPath, "")

		res, err := svc.Connect("codex", "mcpproxy", false)
		if err != nil {
			t.Fatalf("Connect must not error on an empty TOML document, got: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected an empty document to be treated as an empty config, got %+v", res)
		}
	})
}

// TestUndo_NullBackup_DoesNotPanic pins a SIBLING crash of
// TestConnect_NullTopLevelDocument_DoesNotPanic that round-2 cross-model
// review found: replayConnectWrite (internal/connect/undo.go) has its own
// independent JSON parse, and a backup file containing exactly `null`
// resets its pre-initialized `data` map back to nil the same way — but this
// path panicked at `data[client.ServerKey] = serversMap` instead. Repro:
// connect against a null-content config (which backs up the null bytes
// verbatim), then Undo with the returned backup name.
func TestUndo_NullBackup_DoesNotPanic(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	writeFileT(t, cfgPath, "null")

	connectRes, err := svc.Connect("claude-code", "mcpproxy", false)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	undoRes, err := svc.Undo("claude-code", "mcpproxy", filepath.Base(connectRes.BackupPath))
	if err != nil {
		t.Fatalf("Undo must not error on a null-content backup: %v", err)
	}
	if !undoRes.Success {
		t.Fatalf("expected Undo to succeed, got %+v", undoRes)
	}
}

// TestGetStatus_NonObjectServersSection_IsMalformed closes the should-fix the
// cross-model review flagged: GetStatus used to disagree with Preview/Connect
// for the exact same config — findEntryJSONBytes/findEntryTOMLBytes collapsed
// "servers key present but not an object" into the same parsedOK=true,
// found=false outcome as a genuinely absent section, so the status API
// reported a plain "not connected" for a config Preview/Connect now refuse to
// touch. Both must report the same malformed classification.
func TestGetStatus_NonObjectServersSection_IsMalformed(t *testing.T) {
	t.Run("JSON client (claude-code)", func(t *testing.T) {
		svc, home := testService(t)
		writeFileT(t, ConfigPath("claude-code", home), `{"mcpServers":"old"}`)

		status, err := svc.GetStatus("claude-code")
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status.AccessState != accessMalformed {
			t.Fatalf("expected access_state=%q, got %q (status=%+v)", accessMalformed, status.AccessState, status)
		}
		if status.Connected {
			t.Fatalf("a malformed section must not report Connected=true, got %+v", status)
		}
	})

	t.Run("TOML client (codex)", func(t *testing.T) {
		svc, home := testService(t)
		writeFileT(t, ConfigPath("codex", home), `mcp_servers = "old"`+"\n")

		status, err := svc.GetStatus("codex")
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status.AccessState != accessMalformed {
			t.Fatalf("expected access_state=%q, got %q (status=%+v)", accessMalformed, status.AccessState, status)
		}
		if status.Connected {
			t.Fatalf("a malformed section must not report Connected=true, got %+v", status)
		}
	})
}

// TestConnectWithPrecondition_GenuineIOErrorIsNotMaskedAsNonObjectSection
// closes the should-fix the cross-model review flagged against an earlier
// version of this fix: an upstream check that fires on the general
// accessMalformed classification cannot distinguish "section present but
// wrong type" from other malformed causes (a stat/read I/O error, e.g.
// syscall.EIO), so a blanket refusal message there would hide the real cause.
// The fix instead lets a genuine read error propagate through its own
// existing path unchanged (connectJSON/connectTOML's readOrCreateJSON/TOML
// error wrapping), while the NEW, section-specific check only ever fires
// after a successful parse. Prove the injected I/O error's own message
// survives, rather than being replaced by the generic "not an object" text.
func TestConnectWithPrecondition_GenuineIOErrorIsNotMaskedAsNonObjectSection(t *testing.T) {
	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	writeFileT(t, cfgPath, `{"mcpServers":{}}`)

	injected := errors.New("injected-io-failure: input/output error")
	reads := 0
	svc.setReadFile(func(path string) ([]byte, error) {
		reads++
		return nil, injected
	})

	_, err := svc.ConnectWithPrecondition("claude-code", "mcpproxy", true, "")
	// preWriteState's read fails first (classified accessMalformed, not
	// propagated as an error there by design), so the writer's own read is what
	// actually surfaces this error — pinning that this test exercises
	// readOrCreateJSON's wrapped-error path, not a check that short-circuits
	// before ever reaching it.
	if reads != 2 {
		t.Fatalf("expected exactly 2 reads (preWriteState + the writer's own), got %d", reads)
	}
	if err == nil {
		t.Fatal("expected an error for an unreadable config")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected the genuine I/O error to survive via %%w-wrapping, not be masked as a non-object section: %v", err)
	}
	if strings.Contains(err.Error(), "is not a JSON object") {
		t.Fatalf("a genuine I/O error must not be reported as a non-object servers section: %v", err)
	}
}

// The token binds a preview to the entry the write would produce, so the
// REQUESTED name is part of that binding. It used to be absent from the
// preimage: only the RESOLVED name was hashed, and that is the empty string
// whenever the target entry does not exist yet. Two previews for two different
// names over the same config therefore produced the same token, and one could
// be replayed to create a key the user never previewed.
func TestPreconditionToken_BoundToTheRequestedEntryName(t *testing.T) {
	t.Run("two absent targets do not share a token", func(t *testing.T) {
		svc, home := serviceWithKey(t, "")
		writeFileT(t, ConfigPath("claude-code", home), `{"mcpServers":{}}`)

		alpha, err := svc.Preview("claude-code", "alpha")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		beta, err := svc.Preview("claude-code", "beta")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if alpha.PreconditionToken == beta.PreconditionToken {
			t.Fatal("previews for different entry names must not share a token")
		}
	})

	t.Run("two requested names adopting the same entry do not share a token", func(t *testing.T) {
		svc, home := serviceWithKey(t, "")
		writeFileT(t, ConfigPath("opencode", home),
			`{"mcp":{"legacy":{"type":"remote","url":"http://127.0.0.1:8080/mcp"}}}`)

		alpha, err := svc.Preview("opencode", "alpha")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		beta, err := svc.Preview("opencode", "beta")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if alpha.ExistingEntrySummary == nil || alpha.ExistingEntrySummary.EntryName != "legacy" {
			t.Fatalf("precondition: both previews must adopt the same entry, got %+v", alpha.ExistingEntrySummary)
		}
		if alpha.PreconditionToken == beta.PreconditionToken {
			t.Fatal("previews that would write DIFFERENT keys must not share a token")
		}
	})

	t.Run("the client is part of the binding", func(t *testing.T) {
		state := PreconditionState{
			ConfigPath:   "/cfg.json",
			Requested:    "mcpproxy",
			FileExists:   true,
			PendingEntry: json.RawMessage(`{"type":"http"}`),
		}
		other := state
		other.ClientID = "cursor"
		if DerivePreconditionToken(tokenKeyA, state) == DerivePreconditionToken(tokenKeyA, other) {
			t.Fatal("a token minted for one client must not validate for another")
		}
	})
}

// The end of the same story: a token minted for one entry name must not
// authorize a write under another. Nothing is written, and the refusal is the
// ordinary discriminated conflict.
func TestConnectWithPrecondition_TokenIsNotTransferableToAnotherName(t *testing.T) {
	t.Run("absent target", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("claude-code", home)
		const original = `{"mcpServers":{}}`
		writeFileT(t, cfgPath, original)

		alpha, err := svc.Preview("claude-code", "alpha")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}

		res, err := svc.ConnectWithPrecondition("claude-code", "beta", false, alpha.PreconditionToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertPreconditionRefusal(t, res, cfgPath, original)
		if strings.Contains(readConfigT(t, cfgPath), "beta") {
			t.Fatal("a replayed token must not create an entry the user never previewed")
		}
	})

	t.Run("adopted target", func(t *testing.T) {
		svc, home := testService(t)
		cfgPath := ConfigPath("opencode", home)
		const original = `{"mcp":{"legacy":{"type":"remote","url":"http://127.0.0.1:8080/mcp"}}}`
		writeFileT(t, cfgPath, original)

		alpha, err := svc.Preview("opencode", "alpha")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}

		// force rides with the token, and must not rescue a name substitution.
		res, err := svc.ConnectWithPrecondition("opencode", "beta", true, alpha.PreconditionToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertPreconditionRefusal(t, res, cfgPath, original)
	})

	t.Run("the previewed name still writes", func(t *testing.T) {
		svc, home := testService(t)
		writeFileT(t, ConfigPath("claude-code", home), `{"mcpServers":{}}`)

		alpha, err := svc.Preview("claude-code", "alpha")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		res, err := svc.ConnectWithPrecondition("claude-code", "alpha", false, alpha.PreconditionToken)
		if err != nil {
			t.Fatalf("ConnectWithPrecondition: %v", err)
		}
		if !res.Success {
			t.Fatalf("the previewed name must still write, got %+v", res)
		}
	})
}
