package oauth

import (
	"strings"
	"testing"
)

// UX-10: the review screen now shows argv, so positional secrets must be masked
// by the backend in every spelling before the payload leaves the core.
func TestLiveRedactionArgv_ReviewLaunchSecrets(t *testing.T) {
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	cases := map[string][]string{
		"flag and value as two entries": {"server.py", "--token", secret},
		"flag=value":                    {"server.py", "--token=" + secret},
		"bare token":                    {"server.py", secret},
		"api-key flag two entries":      {"-y", "pkg", "--api-key", secret},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			out := LiveRedaction.Argv(argv)
			if len(out) != len(argv) {
				t.Fatalf("length changed: %v", out)
			}
			for _, a := range out {
				if strings.Contains(a, secret) {
					t.Fatalf("secret survived redaction: %q", out)
				}
			}
			if out[0] != argv[0] {
				t.Fatalf("benign argument altered: %q", out)
			}
		})
	}
}
