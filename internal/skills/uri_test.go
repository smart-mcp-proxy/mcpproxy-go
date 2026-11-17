package skills

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSkillURI(t *testing.T) {
	tests := []struct {
		name                  string
		uri                   string
		root, path, skillName string
		wantErr               bool
	}{
		{name: "single segment", uri: "skill://hf-cli/SKILL.md", root: "skill://hf-cli", path: "hf-cli", skillName: "hf-cli"},
		{name: "nested path", uri: "skill://acme/billing/refunds/SKILL.md", root: "skill://acme/billing/refunds", path: "acme/billing/refunds", skillName: "refunds"},
		{name: "other scheme", uri: "https://example.com/pdf/SKILL.md", root: "https://example.com/pdf", path: "example.com/pdf", skillName: "pdf"},
		{name: "no SKILL.md suffix", uri: "skill://pdf/README.md", wantErr: true},
		{name: "wrong case suffix", uri: "skill://pdf/skill.md", wantErr: true},
		{name: "bare SKILL.md", uri: "skill:///SKILL.md", wantErr: true},
		{name: "no scheme", uri: "pdf/SKILL.md", wantErr: true},
		{name: "empty", uri: "", wantErr: true},
		{name: "query", uri: "skill://pdf/SKILL.md?x=1", wantErr: true},
		{name: "fragment", uri: "skill://pdf/SKILL.md#a", wantErr: true},
		{name: "query in path", uri: "skill://p?df/SKILL.md", wantErr: true},
		{name: "dot dot", uri: "skill://a/../SKILL.md", wantErr: true},
		{name: "dot segment", uri: "skill://a/./b/SKILL.md", wantErr: true},
		{name: "empty segment", uri: "skill://a//b/SKILL.md", wantErr: true},
		{name: "control character", uri: "skill://a\x00b/SKILL.md", wantErr: true},
		{name: "newline", uri: "skill://a\nb/SKILL.md", wantErr: true},
		{name: "encoded separator", uri: "skill://a%2Fb/SKILL.md", wantErr: true},
		{name: "too long", uri: "skill://" + strings.Repeat("a", MaxURIBytes) + "/SKILL.md", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, path, name, err := ParseSkillURI(tc.uri)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.root, root)
			assert.Equal(t, tc.path, path)
			assert.Equal(t, tc.skillName, name)
		})
	}
}

func TestRelPath(t *testing.T) {
	const root = "skill://acme/billing"
	tests := []struct {
		name    string
		file    string
		want    string
		wantErr bool
	}{
		{name: "skill file", file: root + "/SKILL.md", want: "SKILL.md"},
		{name: "nested", file: root + "/references/guide.md", want: "references/guide.md"},
		{name: "encoded space", file: root + "/my%20file.md", want: "my file.md"},
		{name: "other scheme", file: "https://acme/billing/SKILL.md", wantErr: true},
		{name: "other host", file: "skill://evil/billing/SKILL.md", wantErr: true},
		{name: "sibling with same prefix", file: "skill://acme/billing2/SKILL.md", wantErr: true},
		{name: "root itself", file: root, wantErr: true},
		{name: "root with slash", file: root + "/", wantErr: true},
		{name: "query", file: root + "/a.md?x=1", wantErr: true},
		{name: "fragment", file: root + "/a.md#x", wantErr: true},
		{name: "encoded slash", file: root + "/a%2Fb.md", wantErr: true},
		{name: "encoded slash lower", file: root + "/a%2fb.md", wantErr: true},
		{name: "encoded backslash", file: root + "/a%5Cb.md", wantErr: true},
		{name: "backslash", file: root + "/a\\b.md", wantErr: true},
		{name: "encoded dot dot", file: root + "/%2e%2e/x", wantErr: true},
		{name: "encoded dot", file: root + "/%2E/x", wantErr: true},
		{name: "dot dot", file: root + "/../x", wantErr: true},
		{name: "dot dot inside", file: root + "/a/../b", wantErr: true},
		{name: "dot dot escaping", file: root + "/../../etc/passwd", wantErr: true},
		{name: "dot segment", file: root + "/./a", wantErr: true},
		{name: "absolute", file: "/etc/passwd", wantErr: true},
		{name: "empty segment", file: root + "//a", wantErr: true},
		{name: "empty segment inside", file: root + "/a//b", wantErr: true},
		{name: "control character", file: root + "/a\x01b", wantErr: true},
		{name: "encoded control character", file: root + "/a%00b", wantErr: true},
		{name: "bad escape", file: root + "/a%zzb", wantErr: true},
		{name: "empty", file: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RelPath(root, tc.file)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
