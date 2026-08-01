package connect

import (
	"encoding/hex"
	"encoding/json"
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
