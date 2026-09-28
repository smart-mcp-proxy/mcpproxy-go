package secretlike

import "testing"

func TestLooksSecret(t *testing.T) {
	cases := map[string]bool{
		"GITHUB_TOKEN":    true,
		"API_KEY":         true,
		"api-key":         true,
		"PASSWORD":        true,
		"PASSWD":          true,
		"AUTH_TOKEN":      true,
		"CREDENTIAL":      true,
		"PRIVATE_KEY":     true,
		"SIGNING_KEY":     true, // "-key$" suffix rule
		"Authorization":   true, // "auth" substring
		"PORT":            false,
		"WORKDIR":         false,
		"PATH":            false,
		"REGION":          false,
		"":                false,
		"KEYBOARD_LAYOUT": false, // "key" not at end, no separator before "key" boundary -> still matches "key" via [_-]key$? no, boundary check
	}
	for name, want := range cases {
		if got := LooksSecret(name); got != want {
			t.Errorf("LooksSecret(%q) = %v, want %v", name, got, want)
		}
	}
}
