//go:build !server

package connect

import (
	"os"
	"strings"
	"testing"
)

// Spec 132 PR-3: Connect can write the per-client browser-login address
// instead of an API-key entry.

func oauthOn(svc *Service) *Service { return svc.WithMCPOAuthEnabled(func() bool { return true }) }

func readConfig(t *testing.T, home, clientID string) string {
	t.Helper()
	raw, err := os.ReadFile(ConfigPath(clientID, home))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func strPtr(s string) *string { return &s }

// A client is marked only after a recorded real-client check: today that is
// Claude Code alone.
func TestSupportsBrowserLogin_OnlyClaudeCode(t *testing.T) {
	for _, c := range GetAllClients() {
		want := c.ID == "claude-code"
		if c.SupportsBrowserLogin != want {
			t.Errorf("%s: SupportsBrowserLogin=%v, want %v", c.ID, c.SupportsBrowserLogin, want)
		}
	}
	svc, _ := testService(t)
	for _, st := range svc.GetAllStatus() {
		if st.SupportsBrowserLogin != (st.ID == "claude-code") {
			t.Errorf("status %s: SupportsBrowserLogin=%v", st.ID, st.SupportsBrowserLogin)
		}
		if st.BrowserLoginAvailable {
			t.Errorf("status %s: available while the feature is off", st.ID)
		}
	}
	oauthOn(svc)
	for _, st := range svc.GetAllStatus() {
		if st.BrowserLoginAvailable != (st.ID == "claude-code") {
			t.Errorf("status %s: BrowserLoginAvailable=%v with the feature on", st.ID, st.BrowserLoginAvailable)
		}
	}
	st, err := svc.GetStatus("claude-code")
	if err != nil || !st.SupportsBrowserLogin || !st.BrowserLoginAvailable {
		t.Fatalf("GetStatus claude-code: %v %+v", err, st)
	}
}

func TestConnectAuthOAuth_WritesOnlyTheAddress(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc).WithRequireMCPAuth(true)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")

	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
	if err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	if len(minter.issued) != 0 || minter.commits != 0 || minter.aborts != 0 {
		t.Fatalf("the minter must not be called: issued=%d commits=%d aborts=%d", len(minter.issued), minter.commits, minter.aborts)
	}
	cfg := readConfig(t, home, "claude-code")
	if !strings.Contains(cfg, `"url": "http://127.0.0.1:8080/mcp/oauth/claude-code"`) {
		t.Fatalf("address missing:\n%s", cfg)
	}
	for _, bad := range []string{"headers", "X-API-Key", "mcp_cli_", "mcp_oat_", "test-key-123", "apikey"} {
		if strings.Contains(cfg, bad) {
			t.Fatalf("config must not contain %q:\n%s", bad, cfg)
		}
	}
	if res.Credential != "" || res.TokenName != "" || res.Keyless || res.Auth != AuthOAuth {
		t.Fatalf("result must carry no credential: %+v", res)
	}
	if !strings.Contains(res.AuthNote, "not revoke") {
		t.Fatalf("result must say what happens to an existing API key: %+v", res)
	}
}

// Every config format builds the address with no header, bridge argument or
// header table, for every client in the registry (the builder, not the gate).
func TestBuildServerEntry_OAuthAddressForEveryFormat(t *testing.T) {
	svc, _ := testService(t)
	for _, c := range GetAllClients() {
		c := c
		if !c.Supported {
			continue
		}
		entry := buildServerEntry(c.ID, svc.entryParamsFor(c.ID, true, "mcp_cli_must_not_appear"))
		raw := marshalEntry(t, entry)
		if !strings.Contains(raw, "http://127.0.0.1:8080/mcp/oauth/"+c.ID) {
			t.Errorf("%s: address missing: %s", c.ID, raw)
		}
		for _, bad := range []string{"headers", "http_headers", "--header", "X-API-Key", "mcp_cli_"} {
			if strings.Contains(raw, bad) {
				t.Errorf("%s: entry must not contain %q: %s", c.ID, bad, raw)
			}
		}
		if _, err := renderEntrySnippet(&c, "mcpproxy", entry); err != nil {
			t.Errorf("%s: render: %v", c.ID, err)
		}
	}
}

func TestPreviewAuthOAuth_MasksNothingAndShowsNoCredential(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc).WithRequireMCPAuth(true)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")

	p, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	if p.Credential != "" || p.Keyless || p.ContainsAPIKey || p.Auth != AuthOAuth {
		t.Fatalf("preview must show no credential: %+v", p)
	}
	if strings.Contains(p.EntryText, "mcp_cli_") || strings.Contains(p.EntryText, "headers") || !strings.Contains(p.EntryText, "/mcp/oauth/claude-code") {
		t.Fatalf("entry text: %s", p.EntryText)
	}
	if !strings.Contains(p.AuthNote, "not revoke") {
		t.Fatalf("preview must say the API key is not revoked: %q", p.AuthNote)
	}
	if len(minter.issued) != 0 {
		t.Fatal("preview must not mint")
	}
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Intent: CredentialIntent{Auth: AuthOAuth}, PreconditionToken: p.PreconditionToken})
	if err != nil || !res.Success {
		t.Fatalf("oauth write with its own token: %v %+v", err, res)
	}
}

// The token binds the choice: a key write cannot ride an oauth preview.
func TestConnectAuthOAuth_PreconditionTokenBindsTheChoice(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	p, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{PreconditionToken: p.PreconditionToken})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Action != "precondition_failed" {
		t.Fatalf("a key write must not ride an oauth preview token: %+v", res)
	}
}

func TestConnectAuthOAuth_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*Service)
		client string
		intent CredentialIntent
		want   string
	}{
		{"feature off", func(s *Service) {}, "claude-code", CredentialIntent{Auth: AuthOAuth}, "mcp_oauth_enabled"},
		{"feature off explicit", func(s *Service) { s.WithMCPOAuthEnabled(func() bool { return false }) }, "claude-code", CredentialIntent{Auth: AuthOAuth}, "mcp_oauth_enabled"},
		{"unsupported client", func(s *Service) { oauthOn(s) }, "cursor", CredentialIntent{Auth: AuthOAuth}, "does not support"},
		{"keyless", func(s *Service) { oauthOn(s) }, "claude-code", CredentialIntent{Auth: AuthOAuth, Keyless: true}, "keyless"},
		{"profile", func(s *Service) { oauthOn(s) }, "claude-code", CredentialIntent{Auth: AuthOAuth, Profile: strPtr("ro")}, "consent"},
		{"unknown auth", func(s *Service) { oauthOn(s) }, "claude-code", CredentialIntent{Auth: "magic"}, "unknown auth"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc, home := testServiceWithKey(t)
			tc.setup(svc)
			minter := withFakeMinter(svc)
			seedClientConfig(t, home, tc.client)
			before := readConfig(t, home, tc.client)

			if _, err := svc.ConnectWithOptions(tc.client, "", ConnectOptions{Intent: tc.intent}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("connect: want error containing %q, got %v", tc.want, err)
			}
			if _, err := svc.PreviewWithIntent(tc.client, "", tc.intent); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("preview: want error containing %q, got %v", tc.want, err)
			}
			if readConfig(t, home, tc.client) != before || len(minter.issued) != 0 {
				t.Fatal("a refusal must write and mint nothing")
			}
		})
	}
}

// Disconnect recognises and removes an entry written this way, and the status
// read reports it as this instance's, signed in with the browser.
func TestDisconnect_RemovesAnOAuthEntry(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	if res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	st, err := svc.GetStatus("claude-code")
	if err != nil || !st.Connected || st.EndpointMatch != EndpointMatchThis || st.Auth != AuthOAuth {
		t.Fatalf("status: %v %+v", err, st)
	}
	res, err := svc.Disconnect("claude-code", "")
	if err != nil || !res.Success || res.Action != "removed" {
		t.Fatalf("disconnect: %v %+v", err, res)
	}
	if strings.Contains(readConfig(t, home, "claude-code"), "oauth") {
		t.Fatal("entry still present")
	}
}

// Found by its address, not by the name it was written under.
func TestStatus_FindsAnOAuthEntryUnderAnotherName(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	if res, err := svc.ConnectWithOptions("claude-code", "my-proxy", ConnectOptions{Auth: AuthOAuth}); err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	st, _ := svc.GetStatus("claude-code")
	if !st.Connected || st.ServerName != "my-proxy" || st.EndpointMatch != EndpointMatchThis {
		t.Fatalf("status: %+v", st)
	}
}

// key -> oauth and oauth -> key replace the one entry; neither duplicates it,
// and neither revokes the API key.
func TestReconnect_SwitchingAuthReplacesTheEntry(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")

	if res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{}); err != nil || !res.Success {
		t.Fatalf("key connect: %v %+v", err, res)
	}
	if res, _ := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); res.Success || res.Action != "already_exists" {
		t.Fatalf("expected already_exists, got %+v", res)
	}
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Force: true, Auth: AuthOAuth})
	if err != nil || !res.Success || res.Action != "updated" {
		t.Fatalf("key->oauth: %v %+v", err, res)
	}
	cfg := readConfig(t, home, "claude-code")
	if strings.Count(cfg, `"url"`) != 1 || strings.Contains(cfg, "headers") || !strings.Contains(cfg, "/mcp/oauth/claude-code") {
		t.Fatalf("key->oauth must leave one oauth entry:\n%s", cfg)
	}
	if minter.aborts != 0 || len(minter.forgetArgs) != 0 {
		t.Fatalf("Connect must not revoke the API key: aborts=%d forget=%v", minter.aborts, minter.forgetArgs)
	}

	res, err = svc.ConnectWithOptions("claude-code", "", ConnectOptions{Force: true, Auth: AuthKey})
	if err != nil || !res.Success || res.Action != "updated" {
		t.Fatalf("oauth->key: %v %+v", err, res)
	}
	cfg = readConfig(t, home, "claude-code")
	if strings.Count(cfg, `"url"`) != 1 || strings.Contains(cfg, "/mcp/oauth") || !strings.Contains(cfg, "X-API-Key") {
		t.Fatalf("oauth->key must leave one key entry:\n%s", cfg)
	}
}

// The default and an explicit key choice write what they always wrote.
func TestConnectAuthKey_IsTheDefault(t *testing.T) {
	for _, a := range []string{"", AuthKey} {
		svc, home := testServiceWithKey(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: a})
		if err != nil || !res.Success || res.Credential == "" || res.AuthNote != "" || res.Auth != "" {
			t.Fatalf("auth=%q: %v %+v", a, err, res)
		}
	}
}

// Feature off: connect and its payloads carry nothing new.
func TestFeatureOff_KeyConnectIsUnchanged(t *testing.T) {
	svc, home := testServiceWithKey(t)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	p, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{})
	if err != nil || p.Auth != "" || p.AuthNote != "" {
		t.Fatalf("preview: %v %+v", err, p)
	}
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{})
	if err != nil || !res.Success || res.Auth != "" || res.AuthNote != "" {
		t.Fatalf("connect: %v %+v", err, res)
	}
}
