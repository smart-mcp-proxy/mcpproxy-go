//go:build !server

package connect

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The exact addresses Undo and Status accept as "ours", per listener form. Any
// extra candidate (above all the raw wildcard host) fails this test.
func TestCanonicalOAuthURLs_ExactListPerListener(t *testing.T) {
	both := func(host string) []string {
		return []string{
			"http://" + host + "/mcp/oauth/claude-code",
			"https://" + host + "/mcp/oauth/claude-code",
		}
	}
	cases := []struct {
		listen string
		want   []string
	}{
		{"127.0.0.1:8080", both("127.0.0.1:8080")},
		{"localhost:8080", both("localhost:8080")},
		{"[::1]:8080", both("[::1]:8080")},
		{":8080", both("127.0.0.1:8080")},
		{"0.0.0.0:8080", both("127.0.0.1:8080")},
		{"[::]:8080", both("127.0.0.1:8080")},
		{"192.168.1.5:8080", nil},
	}
	for _, tc := range cases {
		for _, tls := range []bool{false, true} {
			svc, _ := testService(t)
			svc.listenAddr = tc.listen
			svc.WithTLSEnabled(func() bool { return tls })
			if got := svc.canonicalOAuthURLs("claude-code"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("listen %s tls=%v:\n got %v\nwant %v", tc.listen, tls, got, tc.want)
			}
			if got := svc.canonicalOAuthURLs("cursor"); got != nil {
				t.Errorf("listen %s: an unsupported client has no candidates, got %v", tc.listen, got)
			}
		}
	}
}

// A wildcard listener never makes the wildcard host an accepted address.
func TestUndo_WildcardHostEditIsAConflict(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:8080", "[::]:8080"} {
		for _, prior := range []bool{true, false} {
			svc, home := listenSvc(t, listen, false)
			if !prior {
				_ = os.Remove(ConfigPath("claude-code", home))
			}
			before := ""
			if prior {
				before = readConfig(t, home, "claude-code")
			}
			res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
			if err != nil || !res.Success {
				t.Fatalf("%s connect: %v %+v", listen, err, res)
			}
			written := readConfig(t, home, "claude-code")
			if !strings.Contains(written, "http://127.0.0.1:8080/mcp/oauth/claude-code") {
				t.Fatalf("%s: written address:\n%s", listen, written)
			}

			// untouched: still restored
			if prior {
				undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
				if err != nil || undone.Action != "restored" || readConfig(t, home, "claude-code") != before {
					t.Fatalf("%s untouched undo: %v %+v", listen, err, undone)
				}
				res, err = svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
				if err != nil || !res.Success {
					t.Fatalf("%s reconnect: %v %+v", listen, err, res)
				}
			}

			wild := strings.Split(listen, ":8080")[0]
			edit := strings.Replace(readConfig(t, home, "claude-code"), "http://127.0.0.1:8080/", "http://"+wild+":8080/", 1)
			path := ConfigPath("claude-code", home)
			if err := os.WriteFile(path, []byte(edit), 0o644); err != nil {
				t.Fatal(err)
			}
			undone, err := svc.Undo("claude-code", "", baseName(res.BackupPath))
			if err != nil || undone.Action != "conflict" {
				t.Fatalf("%s prior=%v edited undo: want conflict, got %v %+v", listen, prior, err, undone)
			}
			if after, _ := os.ReadFile(path); string(after) != edit {
				t.Fatalf("%s: the edited file must be left alone", listen)
			}
		}
	}
}
