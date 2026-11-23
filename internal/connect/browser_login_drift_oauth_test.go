//go:build !server

package connect

import "testing"

// After a connect, a change of listen address or tls.enabled behaves for a
// browser-login entry exactly as it does for an API-key entry today: not
// recognised under a custom name, name-only ("other") under the default one,
// and Undo reports a conflict rather than touching the file. Pinned, not a
// feature: the key path is the reference.
func TestOAuthPath_ListenerChangeMatchesTheKeyPath(t *testing.T) {
	type outcome struct {
		connected bool
		match     string
		undo      string
	}
	run := func(t *testing.T, auth, name string, change func(*Service)) outcome {
		svc, home := testService(t)
		oauthOn(svc)
		withFakeMinter(svc)
		seedClientConfig(t, home, "claude-code")
		res, err := svc.ConnectWithOptions("claude-code", name, ConnectOptions{Auth: auth})
		if err != nil || !res.Success {
			t.Fatalf("connect: %v %+v", err, res)
		}
		change(svc)
		st, _ := svc.GetStatus("claude-code")
		undone, err := svc.Undo("claude-code", name, baseName(res.BackupPath))
		if err != nil {
			t.Fatal(err)
		}
		return outcome{st.Connected, st.EndpointMatch, undone.Action}
	}
	changes := map[string]func(*Service){
		"port":     func(s *Service) { s.listenAddr = "127.0.0.1:9999" },
		"tls on":   func(s *Service) { s.WithTLSEnabled(func() bool { return true }) },
		"wildcard": func(s *Service) { s.listenAddr = "0.0.0.0:8080" },
	}
	for cname, change := range changes {
		for _, name := range []string{"my-proxy", "mcpproxy"} {
			key := run(t, AuthKey, name, change)
			oauth := run(t, AuthOAuth, name, change)
			t.Logf("%s/%s: key=%+v oauth=%+v", cname, name, key, oauth)
			if cname == "wildcard" {
				// The key entry named 127.0.0.1 and the listener is now 0.0.0.0, so the key
				// path loses it. The oauth address is the same 127.0.0.1 endpoint, still
				// valid and still recognised: intentionally better than the key path.
				if !oauth.connected || oauth.match != EndpointMatchThis || oauth.undo != "restored" {
					t.Errorf("wildcard/%s: the oauth entry must survive: %+v", name, oauth)
				}
				continue
			}
			if key.connected != oauth.connected || key.undo != oauth.undo {
				t.Errorf("%s/%s: oauth path diverges from the key path: key=%+v oauth=%+v", cname, name, key, oauth)
			}
		}
	}
}
