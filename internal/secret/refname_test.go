package secret

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRefName(t *testing.T) {
	notTaken := func(string) bool { return false }

	cases := []struct {
		name   string
		server string
		kind   string
		key    string
		taken  func(string) bool
		want   string
	}{
		{"env basic", "github", "env", "API_KEY", notTaken, "github-env-api-key"},
		{"header basic", "github", "header", "API_KEY", notTaken, "github-header-api-key"},
		{"header mixed case name", "github", "header", "Authorization", notTaken, "github-header-authorization"},
		{"normalizes punctuation", "My Server!", "env", "GITHUB_TOKEN", notTaken, "my-server-env-github-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RefName(tc.server, tc.kind, tc.key, tc.taken); got != tc.want {
				t.Errorf("RefName(%q,%q,%q) = %q, want %q", tc.server, tc.kind, tc.key, got, tc.want)
			}
		})
	}
}

// TestRefName_EnvHeaderSameNameDistinctRefs pins FR-065: an env var and a
// header with the same name must never share a keyring entry.
func TestRefName_EnvHeaderSameNameDistinctRefs(t *testing.T) {
	notTaken := func(string) bool { return false }
	env := RefName("github", "env", "API_KEY", notTaken)
	header := RefName("github", "header", "API_KEY", notTaken)
	if env == header {
		t.Fatalf("env and header refs must differ, both were %q", env)
	}
}

// TestRefName_TakenNameGetsSuffix pins FR-065/D28: an add never overwrites an
// existing secret — a computed name already in the keyring gets -2, -3, ...
func TestRefName_TakenNameGetsSuffix(t *testing.T) {
	taken := map[string]bool{
		"github-env-api-key":   true,
		"github-env-api-key-2": true,
	}
	got := RefName("github", "env", "API_KEY", func(n string) bool { return taken[n] })
	if got != "github-env-api-key-3" {
		t.Fatalf("RefName with two taken names = %q, want github-env-api-key-3", got)
	}
}

// TestRefName_LongNameTruncated ensures the 64-char cap holds even once a
// numeric suffix is appended for a collision.
func TestRefName_LongNameTruncated(t *testing.T) {
	longKey := "THIS_IS_A_VERY_VERY_VERY_VERY_VERY_VERY_VERY_LONG_ENV_VAR_NAME"
	got := RefName("some-really-long-server-name", "env", longKey, func(string) bool { return false })
	if len(got) > 64 {
		t.Fatalf("RefName length = %d, want <= 64: %q", len(got), got)
	}
}

// TestRefName_FixtureGolden decodes the shared fixture that Go, vitest and
// Swift all pin against (FR-065), and re-derives each case.
func TestRefName_FixtureGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/ref_names.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture struct {
		Cases []struct {
			Server string   `json:"server"`
			Kind   string   `json:"kind"`
			Key    string   `json:"key"`
			Taken  []string `json:"taken"`
			Want   string   `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}

	for _, c := range fixture.Cases {
		takenSet := make(map[string]bool, len(c.Taken))
		for _, n := range c.Taken {
			takenSet[n] = true
		}
		got := RefName(c.Server, c.Kind, c.Key, func(n string) bool { return takenSet[n] })
		if got != c.Want {
			t.Errorf("RefName(%q,%q,%q) taken=%v = %q, want %q", c.Server, c.Kind, c.Key, c.Taken, got, c.Want)
		}
	}
}
