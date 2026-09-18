package scanner

import "testing"

// TestSelectOwnedContainerRejectsPrefixCollision reproduces the container
// hijack bug: `docker ps --filter name=mcpproxy-a-` is a substring match, so
// it returns the container for server "a-b" (name "mcpproxy-a-b-<suffix>")
// when scanning server "a" ever asked for it, because "mcpproxy-a-" is a
// prefix of "mcpproxy-a-b-<suffix>". The com.mcpproxy.server label carries the
// exact, unsanitized server name, so selecting on label equality must reject
// the colliding candidate instead of returning its container ID.
func TestSelectOwnedContainerRejectsPrefixCollision(t *testing.T) {
	psOutput := "deadbeef01\ta-b\n"
	if got := selectOwnedContainer(psOutput, "a"); got != "" {
		t.Errorf("selectOwnedContainer(%q, %q) = %q, want \"\" (must not select colliding container)", psOutput, "a", got)
	}
}

func TestSelectOwnedContainerMatchesExactOwner(t *testing.T) {
	psOutput := "abc123\ta\ndeadbeef01\ta-b\n"
	if got := selectOwnedContainer(psOutput, "a"); got != "abc123" {
		t.Errorf("selectOwnedContainer(...) = %q, want %q", got, "abc123")
	}
}

// TestSelectOwnedContainerMatchesUnderscoreAndSlashVariants covers the other
// two name-sanitization collisions named in MCP report: "a_b" and "a/b" both
// sanitize toward a token that "a"'s own container name is a prefix of
// ("a-b" for the slash case; the underscore case is a substring match too
// since docker ps --filter name= matches raw container names, not sanitized
// tokens, against the sanitized filter pattern).
func TestSelectOwnedContainerMatchesUnderscoreAndSlashVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
	}{
		{"underscore", "a_b"},
		{"slash", "a/b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			psOutput := "cid1\t" + tc.owner + "\n"
			if got := selectOwnedContainer(psOutput, "a"); got != "" {
				t.Errorf("selectOwnedContainer matched wrong owner: got %q, want \"\"", got)
			}
			if got := selectOwnedContainer(psOutput, tc.owner); got != "cid1" {
				t.Errorf("selectOwnedContainer failed to match true owner %q: got %q, want %q", tc.owner, got, "cid1")
			}
		})
	}
}

func TestSelectOwnedContainerNoCandidates(t *testing.T) {
	if got := selectOwnedContainer("", "a"); got != "" {
		t.Errorf("selectOwnedContainer(\"\", %q) = %q, want \"\"", "a", got)
	}
}

func TestSelectOwnedContainerSkipsMalformedLines(t *testing.T) {
	psOutput := "no-tab-here\ncid2\ta\n"
	if got := selectOwnedContainer(psOutput, "a"); got != "cid2" {
		t.Errorf("selectOwnedContainer(...) = %q, want %q", got, "cid2")
	}
}
