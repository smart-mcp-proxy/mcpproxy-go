//go:build server

package connect

import (
	"strings"
	"testing"
)

// A server-edition binary never offers browser login, whatever the config.
func TestConnectAuthOAuth_RefusedInServerEdition(t *testing.T) {
	svc, home := testServiceWithKey(t)
	svc.WithMCPOAuthEnabled(func() bool { return true })
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err == nil || !strings.Contains(err.Error(), "server edition") {
		t.Fatalf("want a server-edition refusal, got %v", err)
	}
	if _, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{Auth: AuthOAuth}); err == nil || !strings.Contains(err.Error(), "server edition") {
		t.Fatalf("preview: want a server-edition refusal, got %v", err)
	}
	if st, _ := svc.GetStatus("claude-code"); st.BrowserLoginAvailable {
		t.Fatal("never available in the server edition")
	}
}
