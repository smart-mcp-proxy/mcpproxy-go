package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestVerifyFile(t *testing.T) {
	content := []byte("hello")
	ref := FileRef{URI: "skill://a/x", Digest: digestOf(content), Size: int64(len(content))}

	assert.Nil(t, VerifyFile(ref, content))

	ue := VerifyFile(ref, []byte("hello!"))
	require.NotNil(t, ue)
	assert.Equal(t, ReasonSizeMismatch, ue.Code)

	ue = VerifyFile(ref, []byte("jello"))
	require.NotNil(t, ue)
	assert.Equal(t, ReasonDigestMismatch, ue.Code)

	bad := ref
	bad.Digest = "md5:" + strings.Repeat("a", 32)
	ue = VerifyFile(bad, content)
	require.NotNil(t, ue)
	assert.Equal(t, ReasonUnsupportedDigest, ue.Code)

	bad.Digest = "sha256:" + strings.Repeat("A", 64)
	ue = VerifyFile(bad, content)
	require.NotNil(t, ue)
	assert.Equal(t, ReasonUnsupportedDigest, ue.Code)

	empty := FileRef{Digest: digestOf(nil), Size: 0}
	assert.Nil(t, VerifyFile(empty, nil))
}

func TestParseFrontmatter(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		fm, err := ParseFrontmatter([]byte("---\nname: pdf\ndescription: Work with PDFs\nversion: 2\nmeta:\n  k: v\n---\n# Body\n"))
		require.NoError(t, err)
		assert.Equal(t, "pdf", fm["name"])
		assert.Equal(t, 2, fm["version"])
	})
	t.Run("crlf and BOM", func(t *testing.T) {
		fm, err := ParseFrontmatter([]byte("\xef\xbb\xbf---\r\nname: pdf\r\n---\r\nbody"))
		require.NoError(t, err)
		assert.Equal(t, "pdf", fm["name"])
	})
	t.Run("closing marker at end of file", func(t *testing.T) {
		fm, err := ParseFrontmatter([]byte("---\nname: pdf\n---"))
		require.NoError(t, err)
		assert.Equal(t, "pdf", fm["name"])
	})
	for name, in := range map[string]string{
		"no frontmatter":     "# Title\n",
		"empty":              "",
		"not closed":         "---\nname: pdf\n",
		"not a mapping":      "---\n- a\n- b\n---\n",
		"scalar":             "---\nhello\n---\n",
		"empty block":        "---\n---\nbody",
		"invalid yaml":       "---\nname: [unclosed\n---\n",
		"non-string key":     "---\n1: a\n---\n",
		"marker not on line": "--- name: x\n---\n",
	} {
		t.Run("error/"+name, func(t *testing.T) {
			_, err := ParseFrontmatter([]byte(in))
			require.Error(t, err)
		})
	}
	t.Run("over 64 KiB", func(t *testing.T) {
		in := "---\nname: pdf\ndescription: " + strings.Repeat("x", MaxFrontmatterBytes) + "\n---\n"
		_, err := ParseFrontmatter([]byte(in))
		require.Error(t, err)
	})
	t.Run("unclosed huge file", func(t *testing.T) {
		in := "---\n" + strings.Repeat("a: b\n", 100000)
		_, err := ParseFrontmatter([]byte(in))
		require.Error(t, err)
	})
}

func TestFrontmatterEqual(t *testing.T) {
	parsed, err := ParseFrontmatter([]byte("---\nname: pdf\ncount: 3\nratio: 1.5\nnested:\n  a: [1, 2]\n  b: true\n---\n"))
	require.NoError(t, err)
	listed := map[string]any{
		"name": "pdf", "count": float64(3), "ratio": 1.5,
		"nested": map[string]any{"a": []any{float64(1), float64(2)}, "b": true},
	}
	assert.True(t, FrontmatterEqual(listed, parsed))

	listed["nested"] = map[string]any{"a": []any{float64(1), float64(3)}, "b": true}
	assert.False(t, FrontmatterEqual(listed, parsed))

	assert.False(t, FrontmatterEqual(map[string]any{"name": "pdf"}, map[string]any{"name": "pdf", "x": 1}))
	assert.True(t, FrontmatterEqual(nil, map[string]any{}))
	assert.True(t, FrontmatterEqual(map[string]any{"a": 1}, map[string]any{"a": int64(1)}))
	assert.False(t, FrontmatterEqual(map[string]any{"a": "1"}, map[string]any{"a": 1}))
	assert.False(t, FrontmatterEqual(map[string]any{"a": func() {}}, map[string]any{"a": 1}), "unmarshalable values never compare equal")
}
