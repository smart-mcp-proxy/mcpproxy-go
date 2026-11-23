//go:build !server

package connect

import (
	"strings"
	"testing"
)

func listenSvc(t *testing.T, listen string, tls bool) (*Service, string) {
	t.Helper()
	svc, home := testService(t)
	svc.listenAddr = listen
	oauthOn(svc)
	svc.WithTLSEnabled(func() bool { return tls })
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	return svc, home
}

// The sign-in address uses the scheme the listener really speaks and a host a
// client can dial; the key path keeps writing what it always wrote.
func TestOAuthAddress_FollowsTheListener(t *testing.T) {
	cases := []struct {
		name, listen string
		tls          bool
		wantOAuth    string
		wantKey      string
	}{
		{"plain loopback", "127.0.0.1:8080", false, "http://127.0.0.1:8080/mcp/oauth/claude-code", "http://127.0.0.1:8080/mcp"},
		{"tls", "127.0.0.1:8080", true, "https://127.0.0.1:8080/mcp/oauth/claude-code", "http://127.0.0.1:8080/mcp"},
		{"wildcard v4", "0.0.0.0:8080", false, "http://127.0.0.1:8080/mcp/oauth/claude-code", "http://0.0.0.0:8080/mcp"},
		{"wildcard v6", "[::]:8080", false, "http://127.0.0.1:8080/mcp/oauth/claude-code", "http://[::]:8080/mcp"},
		{"port only", ":8080", false, "http://127.0.0.1:8080/mcp/oauth/claude-code", "http://127.0.0.1:8080/mcp"},
		{"wildcard with tls", "0.0.0.0:8443", true, "https://127.0.0.1:8443/mcp/oauth/claude-code", "http://0.0.0.0:8443/mcp"},
		{"v6 loopback", "[::1]:8080", false, "http://[::1]:8080/mcp/oauth/claude-code", "http://[::1]:8080/mcp"},
		{"localhost", "localhost:8080", false, "http://localhost:8080/mcp/oauth/claude-code", "http://localhost:8080/mcp"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc, home := listenSvc(t, tc.listen, tc.tls)
			p, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{Auth: AuthOAuth})
			if err != nil || !strings.Contains(p.EntryText, tc.wantOAuth) {
				t.Fatalf("preview: %v %+v", err, p)
			}
			res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth, PreconditionToken: p.PreconditionToken})
			if err != nil || !res.Success {
				t.Fatalf("connect: %v %+v", err, res)
			}
			if cfg := readConfig(t, home, "claude-code"); !strings.Contains(cfg, `"`+tc.wantOAuth+`"`) {
				t.Fatalf("written:\n%s", cfg)
			}
			st, _ := svc.GetStatus("claude-code")
			if !st.Connected || st.Auth != AuthOAuth || st.EndpointMatch != EndpointMatchThis {
				t.Fatalf("status not recognised: %+v", st)
			}
			if res, err := svc.Disconnect("claude-code", ""); err != nil || res.Action != "removed" {
				t.Fatalf("disconnect: %v %+v", err, res)
			}
			// the key path is untouched by all of this
			kp, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{})
			if err != nil || !strings.Contains(kp.EntryText, `"`+tc.wantKey+`"`) {
				t.Fatalf("key entry changed: %v\n%s", err, kp.EntryText)
			}
		})
	}
}

func TestOAuthAddress_UndoOverTLSAndWildcard(t *testing.T) {
	svc, home := listenSvc(t, "0.0.0.0:8443", true)
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
	if err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
	if err != nil || undone.Action != "restored" {
		t.Fatalf("undo: %v %+v", err, undone)
	}
	_ = home
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// A specific non-loopback address cannot serve browser login: refuse, never
// write an address that cannot work.
func TestOAuthAddress_RefusedForANonLoopbackListener(t *testing.T) {
	svc, home := listenSvc(t, "192.168.1.5:8080", false)
	before := readConfig(t, home, "claude-code")
	if _, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{Auth: AuthOAuth}); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("preview: want a loopback refusal, got %v", err)
	}
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("connect: want a loopback refusal, got %v", err)
	}
	if readConfig(t, home, "claude-code") != before {
		t.Fatal("must write nothing")
	}
	if st, _ := svc.GetStatus("claude-code"); st.BrowserLoginAvailable {
		t.Fatal("not available on a non-loopback listener")
	}
	// the key path still works there
	if res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{}); err != nil || !res.Success {
		t.Fatalf("key connect: %v %+v", err, res)
	}
}
