package connect

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

const adminKeyForTests = "test-key-123"

// allConnectClients lists every supported client, including the two the
// preview matrix does not seed (zcode) so every carrier is covered.
var allConnectClients = []string{
	"claude-code", "claude-desktop", "cursor", "windsurf",
	"vscode", "codex", "gemini", "opencode", "zcode",
}

// carrierValue reads the credential the written entry carries, per client.
func writtenCredential(t *testing.T, svc *Service, clientID string) string {
	t.Helper()
	client := FindClient(clientID)
	raw, err := os.ReadFile(ConfigPath(clientID, svc.homeDir))
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := entryCredentialFromRaw(client, raw, "mcpproxy")
	return secret
}

// TestConnectClientCredential_EveryCarrierWritesTheMintedSecret pins FR-024:
// every client carrier (X-API-Key header, mcp-remote --header, Codex ?apikey=)
// carries the minted mcp_cli_ secret, in both auth modes, and the admin API key
// is never written.
func TestConnectClientCredential_EveryCarrierWritesTheMintedSecret(t *testing.T) {
	for _, authOn := range []bool{false, true} {
		for _, clientID := range allConnectClients {
			clientID, authOn := clientID, authOn
			t.Run(clientID+map[bool]string{true: "/auth-on", false: "/auth-off"}[authOn], func(t *testing.T) {
				svc, home := testServiceWithKey(t)
				svc.WithRequireMCPAuth(authOn)
				minter := withFakeMinter(svc)
				seedClientConfig(t, home, clientID)

				res, err := svc.Connect(clientID, "mcpproxy", false)
				if err != nil || !res.Success {
					t.Fatalf("Connect: %v %+v", err, res)
				}
				secret := minter.lastSecret()
				if !strings.HasPrefix(secret, auth.ClientTokenPrefixStr) {
					t.Fatalf("minted secret must be mcp_cli_, got %q", secret)
				}
				raw, _ := os.ReadFile(ConfigPath(clientID, home))
				if bytes.Contains(raw, []byte(adminKeyForTests)) {
					t.Fatalf("the admin API key must never be written:\n%s", raw)
				}
				if got := writtenCredential(t, svc, clientID); got != secret {
					t.Fatalf("carrier holds %q, want the minted %q\n%s", got, secret, raw)
				}
				if minter.commits != 1 || minter.aborts != 0 {
					t.Fatalf("commits=%d aborts=%d", minter.commits, minter.aborts)
				}
			})
		}
	}
}

// Without a minter the admin key still has no write path: auth on is refused,
// auth off writes the legacy keyless entry.
func TestConnectClientCredential_NoMinterNeverFallsBackToAdminKey(t *testing.T) {
	for _, clientID := range allConnectClients {
		clientID := clientID
		t.Run(clientID, func(t *testing.T) {
			svc, home := testServiceWithKey(t)
			seedClientConfig(t, home, clientID)
			before, _ := os.ReadFile(ConfigPath(clientID, home))

			svc.WithRequireMCPAuth(true)
			if _, err := svc.Connect(clientID, "mcpproxy", false); !errors.Is(err, ErrNoCredentialMinter) {
				t.Fatalf("auth on with no minter must be refused, got %v", err)
			}
			after, _ := os.ReadFile(ConfigPath(clientID, home))
			if !bytes.Equal(before, after) {
				t.Fatal("a refused connect must not touch the config")
			}

			svc.WithRequireMCPAuth(false)
			res, err := svc.Connect(clientID, "mcpproxy", false)
			if err != nil || !res.Success {
				t.Fatalf("auth off: %v %+v", err, res)
			}
			raw, _ := os.ReadFile(ConfigPath(clientID, home))
			if bytes.Contains(raw, []byte(adminKeyForTests)) || writtenCredential(t, svc, clientID) != "" {
				t.Fatalf("legacy keyless entry expected, got:\n%s", raw)
			}
			if !res.Keyless {
				t.Fatal("result must say keyless")
			}
		})
	}
}

func TestConnectClientCredential_Keyless(t *testing.T) {
	svc, home := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "cursor")
	prof := "ro"

	// keyless + profile -> refused, nothing minted or written
	before, _ := os.ReadFile(ConfigPath("cursor", home))
	_, err := svc.ConnectWithOptions("cursor", "mcpproxy", ConnectOptions{Intent: CredentialIntent{Keyless: true, Profile: &prof}})
	if !errors.Is(err, ErrKeylessWithProfile) {
		t.Fatalf("got %v", err)
	}
	// keyless with auth on -> refused
	svc.WithRequireMCPAuth(true)
	_, err = svc.ConnectWithOptions("cursor", "mcpproxy", ConnectOptions{Intent: CredentialIntent{Keyless: true}})
	if !errors.Is(err, ErrKeylessRequiresAuthOff) {
		t.Fatalf("got %v", err)
	}
	after, _ := os.ReadFile(ConfigPath("cursor", home))
	if !bytes.Equal(before, after) || len(minter.issued) != 0 {
		t.Fatal("refused keyless connects must mint and write nothing")
	}

	// keyless with auth off -> credential-less entry, no mint
	svc.WithRequireMCPAuth(false)
	res, err := svc.ConnectWithOptions("cursor", "mcpproxy", ConnectOptions{Intent: CredentialIntent{Keyless: true}})
	if err != nil || !res.Success || !res.Keyless {
		t.Fatalf("%v %+v", err, res)
	}
	if writtenCredential(t, svc, "cursor") != "" || len(minter.issued) != 0 {
		t.Fatal("keyless writes no credential and mints nothing")
	}
}

// A token-name conflict (client-<id> held by a regular token) or a guard
// refusal comes back from the minter BEFORE any write: the file is
// byte-identical, no backup is taken and Commit is never called.
func TestConnectClientCredential_MinterRefusalWritesNothing(t *testing.T) {
	for name, issueErr := range map[string]error{
		"name conflict": errors.New("token name is held by a regular agent token"),
		"guard":         errors.New("a client bound to profile ro could escape it"),
	} {
		issueErr := issueErr
		t.Run(name, func(t *testing.T) {
			svc, home := testServiceWithKey(t)
			svc.WithRequireMCPAuth(true)
			minter := withFakeMinter(svc)
			minter.issueErr = issueErr
			seedClientConfig(t, home, "cursor")
			path := ConfigPath("cursor", home)
			before, _ := os.ReadFile(path)

			_, err := svc.Connect("cursor", "mcpproxy", false)
			if !errors.Is(err, issueErr) {
				t.Fatalf("the minter's refusal must surface unchanged, got %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("config must be byte-identical")
			}
			if entries, _ := filepath.Glob(path + ".bak*"); len(entries) != 0 {
				t.Fatalf("no backup may be taken for a refused mint, got %v", entries)
			}
			if minter.commits != 0 {
				t.Fatal("Commit must not be called")
			}
		})
	}
}

// An entry that already exists (no force) mints nothing: the credential is
// only issued once every credential-independent refusal has passed.
func TestConnectClientCredential_AlreadyExistsMintsNothing(t *testing.T) {
	svc, home := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "cursor")
	if _, err := svc.Connect("cursor", "mcpproxy", false); err != nil {
		t.Fatal(err)
	}
	issuedBefore := len(minter.issued)

	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Action != "already_exists" {
		t.Fatalf("got %+v", res)
	}
	if len(minter.issued) != issuedBefore {
		t.Fatal("an already_exists refusal must not mint or stage anything")
	}
}

// A failed write aborts the mint (fresh: forgotten; reconnect: rotation
// rolled back so the old secret keeps working).
func TestConnectClientCredential_WriteFailureAborts(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permission bits are not enforced here")
	}
	svc, home := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	minter.rotating = true
	seedClientConfig(t, home, "cursor")
	dir := filepath.Dir(ConfigPath("cursor", home))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err == nil && (res == nil || res.Success) {
		t.Fatalf("expected the write to fail, got %+v", res)
	}
	if minter.aborts != 1 || minter.commits != 0 {
		t.Fatalf("a failed write must Abort exactly once (aborts=%d commits=%d)", minter.aborts, minter.commits)
	}
}

func TestConnectClientCredential_ReportsRotation(t *testing.T) {
	svc, home := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	minter.rotating = true
	seedClientConfig(t, home, "cursor")
	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err != nil || !res.Success {
		t.Fatalf("%v %+v", err, res)
	}
	if res.Rotation != profile.RotationFinalized || res.Credential != maskClientCredential || res.TokenName != "client-cursor" {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(marshalEntry(t, res), fakeSecret(1)) {
		t.Fatal("the real secret must never appear in the result")
	}
}

// TestGetStatus_CredentialStateClassification pins plan D7: what a client's
// entry carries maps to client|admin_key|none|revoked|expired.
func TestGetStatus_CredentialStateClassification(t *testing.T) {
	active, expired, unknown := fakeSecret(101), fakeSecret(102), fakeSecret(103)
	agent := "mcp_agt_" + strings.Repeat("a", 64)
	nearMiss := adminKeyForTests + "x"

	cases := []struct {
		name  string
		entry string
		want  profile.CredentialState
	}{
		{"no credential", `{"type":"http","url":"http://127.0.0.1:8080/mcp"}`, profile.CredentialStateNone},
		{"admin key in header", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + adminKeyForTests + `"}}`, profile.CredentialStateAdminKey},
		{"admin key in lower-case header", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"x-api-key":"` + adminKeyForTests + `"}}`, profile.CredentialStateAdminKey},
		{"admin key in query", `{"type":"http","url":"http://127.0.0.1:8080/mcp?apikey=` + adminKeyForTests + `"}`, profile.CredentialStateAdminKey},
		{"near-miss admin key", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + nearMiss + `"}}`, profile.CredentialStateNone},
		{"active client credential", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + active + `"}}`, profile.CredentialStateClient},
		{"expired client credential", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + expired + `"}}`, profile.CredentialStateExpired},
		{"rotated-away or deleted secret", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + unknown + `"}}`, profile.CredentialStateRevoked},
		{"pasted agent token is not a client credential", `{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + agent + `"}}`, profile.CredentialStateNone},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc, home := testServiceWithKey(t)
			minter := withFakeMinter(svc)
			minter.classify[active] = profile.CredentialStateClient
			minter.classify[expired] = profile.CredentialStateExpired
			path := ConfigPath("claude-code", home)
			writeFileT(t, path, `{"mcpServers":{"mcpproxy":`+tc.entry+`}}`)

			st, err := svc.GetStatus("claude-code")
			if err != nil {
				t.Fatal(err)
			}
			if !st.Connected || st.CredentialState != string(tc.want) {
				t.Fatalf("credential_state = %q (connected=%v), want %q", st.CredentialState, st.Connected, tc.want)
			}
			if b, _ := marshalJSONIndent(st); bytes.Contains(b, []byte(adminKeyForTests)) || bytes.Contains(b, []byte(active)) {
				t.Fatalf("the status must never echo a credential: %s", b)
			}
		})
	}
}

// The stat-only listing never reads a config: connected-capable rows report
// "unknown", and absent configs report nothing.
func TestGetAllStatus_CredentialStateIsUnknownAndReadFree(t *testing.T) {
	svc, home := testServiceWithKey(t)
	withFakeMinter(svc)
	reads := 0
	svc.setReadFile(func(p string) ([]byte, error) { reads++; return os.ReadFile(p) })
	seedClientConfig(t, home, "cursor")

	var cursor, windsurf *ClientStatus
	statuses := svc.GetAllStatus()
	for i := range statuses {
		switch statuses[i].ID {
		case "cursor":
			cursor = &statuses[i]
		case "windsurf":
			windsurf = &statuses[i]
		}
	}
	if reads != 0 {
		t.Fatalf("the listing must not read any config, read %d", reads)
	}
	if cursor.CredentialState != string(profile.CredentialStateUnknown) {
		t.Fatalf("cursor credential_state = %q, want unknown", cursor.CredentialState)
	}
	if windsurf.CredentialState != "" {
		t.Fatalf("an absent config reports no credential_state, got %q", windsurf.CredentialState)
	}
}

func TestPreview_ClientCredentialIntent(t *testing.T) {
	svc, home := testServiceWithKey(t)
	svc.WithRequireMCPAuth(true)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "cursor")
	prof, mode := "ro", "locked"

	p, err := svc.PreviewWithIntent("cursor", "mcpproxy", CredentialIntent{Profile: &prof, Mode: &mode})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Credential, "mcp_cli_") || strings.HasPrefix(p.Credential, "mcp_agt_") {
		t.Fatalf("credential = %q", p.Credential)
	}
	if p.ContainsAPIKey || p.Profile != "ro" || p.Mode != "locked" || p.Keyless {
		t.Fatalf("preview = %+v", p)
	}
	if len(minter.issued) != 0 {
		t.Fatal("preview must not mint")
	}
	all, _ := svc.PreviewWithIntent("cursor", "mcpproxy", CredentialIntent{})
	if all.Profile != "" || all.Mode != "switchable" {
		t.Fatalf("default intent is All servers, switchable; got profile=%q mode=%q", all.Profile, all.Mode)
	}
	if _, err := svc.PreviewWithIntent("cursor", "mcpproxy", CredentialIntent{Keyless: true}); !errors.Is(err, ErrKeylessRequiresAuthOff) {
		t.Fatalf("got %v", err)
	}
}

// Undo is "as if the connect never happened" (plan D16): the drift check
// accepts the credential the file holds only if it is this client's own, and
// the result names the credential revoked.
func TestUndo_RevokesTheMintedCredential(t *testing.T) {
	svc, home := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	minter.forgot = "client-cursor"
	seedClientConfig(t, home, "cursor")
	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err != nil || !res.Success {
		t.Fatalf("%v %+v", err, res)
	}

	undone, err := svc.Undo("cursor", "mcpproxy", filepath.Base(res.BackupPath))
	if err != nil {
		t.Fatal(err)
	}
	if !undone.Success || undone.Action != "restored" {
		t.Fatalf("undo = %+v", undone)
	}
	if undone.CredentialRevoked != "client-cursor" {
		t.Fatalf("credential_revoked = %q", undone.CredentialRevoked)
	}
	if len(minter.forgetArgs) != 1 || strings.Contains(minter.forgetArgs[0], "|mcp_cli_") {
		t.Fatalf("the restored entry holds no credential, got %v", minter.forgetArgs)
	}
}

func TestUndo_RefusesACredentialThatIsNotTheClientsOwn(t *testing.T) {
	svc, home := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "cursor")
	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err != nil || !res.Success {
		t.Fatalf("%v %+v", err, res)
	}
	// Someone swapped in a different credential since the connect.
	delete(minter.held, minter.lastSecret())

	undone, err := svc.Undo("cursor", "mcpproxy", filepath.Base(res.BackupPath))
	if err != nil {
		t.Fatal(err)
	}
	if undone.Success || undone.Action != "conflict" {
		t.Fatalf("a foreign credential must be a drift conflict, got %+v", undone)
	}
}

func TestConnectResult_KeepsDisplayPathAndReloadHint(t *testing.T) {
	svc, home := testServiceWithKey(t)
	withFakeMinter(svc)
	seedClientConfig(t, home, "cursor")
	res, err := svc.Connect("cursor", "mcpproxy", false)
	if err != nil || !res.Success {
		t.Fatalf("%v %+v", err, res)
	}
	if res.DisplayPath == "" || res.ReloadHint == "" {
		t.Fatalf("Spec 109-b fields must survive: %+v", res)
	}
}
