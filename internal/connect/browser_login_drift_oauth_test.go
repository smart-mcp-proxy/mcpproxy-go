//go:build !server

package connect

import "testing"

// After a connect, a change of listen address or tls.enabled:
//   - Status recognises a browser-login entry exactly as it recognises an API-key
//     entry (lost under a custom name after a port change, name-only under the
//     default one): pinned against the key path, which is the reference;
//   - Undo of a browser-login connect trusts the digest of the file recorded at
//     write time, so it still restores an untouched file. The key path keeps its
//     original replay and is deliberately not changed (see
//     TestKeyPath_ListenerChangeAfterConnect).
func TestOAuthPath_ListenerChangeMatchesTheKeyPathInStatus(t *testing.T) {
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
			if oauth.undo != "restored" {
				t.Errorf("%s/%s: an untouched oauth connect must be restored, got %+v", cname, name, oauth)
			}
			if cname == "wildcard" {
				// The key entry named 127.0.0.1 and the listener is now 0.0.0.0, so the key
				// path loses it. The oauth address is the same 127.0.0.1 endpoint, still
				// valid and still recognised: intentionally better than the key path.
				if !oauth.connected || oauth.match != EndpointMatchThis {
					t.Errorf("wildcard/%s: the oauth entry must stay recognised: %+v", name, oauth)
				}
				continue
			}
			if key.connected != oauth.connected || key.match != oauth.match {
				t.Errorf("%s/%s: Status diverges from the key path: key=%+v oauth=%+v", cname, name, key, oauth)
			}
		}
	}
}
