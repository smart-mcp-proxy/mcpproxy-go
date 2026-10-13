package checks

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// globRepresentatives maps each WILDCARD glob from security.GetFilePathPatterns()
// to a realistic concrete path. Literal globs are used verbatim. A new wildcard
// glob without an entry here fails the test, forcing the author to (a) add a
// representative and (b) mirror the pattern into sensitiveFilePatterns.
var globRepresentatives = map[string]string{
	"~/.ssh/*_key":                           "~/.ssh/deploy_key",
	"*service_account*.json":                 "prod-service_account-key.json",
	"*.env":                                  "production.env",
	"*.pem":                                  "server.pem",
	"*.key":                                  "tls.key",
	"*.ppk":                                  "login.ppk",
	"*.p12":                                  "identity.p12",
	"*.pfx":                                  "cert.pfx",
	"*.kdbx":                                 "vault.kdbx",
	"*.pgpass":                               "prod.pgpass",
	"~/Library/Keychains/*":                  "~/Library/Keychains/login.keychain-db",
	"/Library/Keychains/*":                   "/Library/Keychains/System.keychain",
	`%LOCALAPPDATA%\Microsoft\Credentials\*`: `%LOCALAPPDATA%\Microsoft\Credentials\ABC123DEF456`,
	`%APPDATA%\Microsoft\Credentials\*`:      `%APPDATA%\Microsoft\Credentials\ABC123DEF456`,
}

// TestSensitiveFilePatternParity enforces the sync contract between the runtime
// sensitive-path detector (internal/security/paths.go, GetFilePathPatterns) and
// the baseline scanner's mirrored regex list (sensitiveFilePatterns in
// embedded_secret.go), which until issue #795 was comment-only ("keep the two in
// sync"): every glob the runtime detector knows must be matchable by at least
// one baseline regex, so an addition to paths.go fails here until mirrored.
// (The reverse direction is intentionally unchecked: sensitiveFilePatterns may
// carry extras — the baseline scanner is allowed to be a superset.)
func TestSensitiveFilePatternParity(t *testing.T) {
	patterns := security.GetFilePathPatterns()
	if len(patterns) == 0 {
		t.Fatal("GetFilePathPatterns returned nothing — parity test has no subject")
	}
	for _, p := range patterns {
		for _, glob := range p.Patterns {
			example := glob
			if strings.ContainsAny(glob, "*?") {
				rep, ok := globRepresentatives[glob]
				if !ok {
					t.Errorf("pattern %q: no representative example for new wildcard glob %q — add one to globRepresentatives AND mirror the glob in sensitiveFilePatterns (embedded_secret.go)", p.Name, glob)
					continue
				}
				example = rep
			}
			text := "reads the file " + example + " from disk"
			matched := false
			for _, re := range sensitiveFilePatterns {
				if re.MatchString(text) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("pattern %q: paths.go glob %q (example %q) matches no regex in sensitiveFilePatterns — mirror it in embedded_secret.go", p.Name, glob, example)
			}
		}
	}
}
