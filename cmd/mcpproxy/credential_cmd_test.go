//go:build server

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

func sampleCredentials() []cliclient.CredentialStatus {
	exp := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	return []cliclient.CredentialStatus{
		{Server: "github", Mode: "oauth_connect", Status: "connected", TokenType: "Bearer", Scopes: []string{"repo"}, ObtainedVia: "connect_flow", ExpiresAt: &exp},
		{Server: "jira", Mode: "oauth_connect", Status: "not_connected", ConnectPath: "/api/v1/user/credentials/jira/connect"},
	}
}

func TestCredentialCommandTree(t *testing.T) {
	cmd := newCredentialCommand()
	want := map[string]bool{"list": false, "status": false, "connect": false, "rm": false}
	for _, sub := range cmd.Commands() {
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("credential command missing subcommand %q", name)
		}
	}
}

func TestRenderCredentialsTable(t *testing.T) {
	out := renderCredentialsTable(sampleCredentials())
	for _, want := range []string{"SERVER", "github", "connected", "not_connected", "oauth_connect"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
	// The connectable marker + hint must surface for not_connected upstreams.
	if !strings.Contains(out, "connectable") {
		t.Errorf("expected connect hint in table:\n%s", out)
	}
}

func TestRenderCredentialsTable_Empty(t *testing.T) {
	if got := renderCredentialsTable(nil); !strings.Contains(got, "No brokered upstreams") {
		t.Errorf("unexpected empty render: %q", got)
	}
}

// FR-026: the rendered detail/table must never contain token-like field names
// or values. The render functions only read non-secret fields, so this is a
// regression guard on the struct contract.
func TestRenderCredential_NoSecretLeak(t *testing.T) {
	out := renderCredentialDetail(sampleCredentials()[0]) + renderCredentialsTable(sampleCredentials())
	for _, banned := range []string{"access_token", "refresh_token", "AccessToken", "RefreshToken"} {
		if strings.Contains(out, banned) {
			t.Errorf("rendered output leaked secret-bearing field %q:\n%s", banned, out)
		}
	}
}

func TestRenderCredentialDetail(t *testing.T) {
	out := renderCredentialDetail(sampleCredentials()[0])
	for _, want := range []string{"Server:", "github", "Status:", "connected", "Scopes:", "repo", "Expires:"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q\n%s", want, out)
		}
	}
}

func TestFindCredential_CaseInsensitive(t *testing.T) {
	creds := sampleCredentials()
	got, ok := findCredential(creds, "GitHub")
	if !ok || got.Server != "github" {
		t.Errorf("expected case-insensitive match for github, got ok=%v server=%q", ok, got.Server)
	}
	if _, ok := findCredential(creds, "nope"); ok {
		t.Errorf("expected no match for unknown server")
	}
}

func TestCredentialConnectURL_EscapesServer(t *testing.T) {
	got := credentialConnectURL("http://localhost:8080/", "ns/name")
	want := "http://localhost:8080/api/v1/user/credentials/ns%2Fname/connect"
	if got != want {
		t.Errorf("connect URL = %q, want %q", got, want)
	}
}

func TestResolveCredentialBaseURL_FlagAndEnv(t *testing.T) {
	t.Setenv("MCPPROXY_SERVER_URL", "https://env.example.com/")
	credServerURL = ""
	if got := resolveCredentialBaseURL(); got != "https://env.example.com" {
		t.Errorf("env base URL = %q", got)
	}
	credServerURL = "https://flag.example.com/"
	defer func() { credServerURL = "" }()
	if got := resolveCredentialBaseURL(); got != "https://flag.example.com" {
		t.Errorf("flag base URL = %q (flag should win)", got)
	}
}

func TestResolveCredentialToken(t *testing.T) {
	credToken = ""
	t.Setenv("MCPPROXY_TOKEN", "env-tok")
	if got := resolveCredentialToken(); got != "env-tok" {
		t.Errorf("env token = %q", got)
	}
	credToken = "flag-tok"
	defer func() { credToken = "" }()
	if got := resolveCredentialToken(); got != "flag-tok" {
		t.Errorf("flag token = %q (flag should win)", got)
	}
}
