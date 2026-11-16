package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Numbers are compared exactly: a listing and a SKILL.md that differ only in a
// large integer are different, while 1 and 1.0 are the same number.
func TestFrontmatterNumbersAreComparedExactly(t *testing.T) {
	listed, err := DecodeEntry([]byte(`{"uri":"skill://a/SKILL.md","frontmatter":{"name":"a","description":"d","count":9007199254740992},"resources":[{"uri":"skill://a/SKILL.md","digest":"sha256:` + strings.Repeat("0", 64) + `","size":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFrontmatter([]byte("---\nname: a\ndescription: d\ncount: 9007199254740993\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if FrontmatterEqual(listed.Frontmatter, parsed) {
		t.Fatal("9007199254740992 and 9007199254740993 compared equal")
	}
	same, err := ParseFrontmatter([]byte("---\nname: a\ndescription: d\ncount: 9007199254740992\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !FrontmatterEqual(listed.Frontmatter, same) {
		t.Fatal("the same large integer compared different")
	}

	one := map[string]any{"v": json.Number("1")}
	for _, y := range []string{"v: 1", "v: 1.0", "v: 1e0"} {
		p, err := ParseFrontmatter([]byte("---\n" + y + "\n---\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !FrontmatterEqual(one, p) {
			t.Errorf("1 and %q compared different", y)
		}
	}
	if FrontmatterEqual(map[string]any{"v": json.Number("1")}, map[string]any{"v": "1"}) {
		t.Error("the number 1 and the string \"1\" compared equal")
	}
}

func TestContentHashSeesLargeIntegerDifference(t *testing.T) {
	mk := func(n string) Entry {
		e, err := DecodeEntry([]byte(`{"uri":"skill://a/SKILL.md","frontmatter":{"name":"a","description":"d","count":` + n + `},"resources":[{"uri":"skill://a/SKILL.md","digest":"sha256:` + strings.Repeat("0", 64) + `","size":1}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	h1, err1 := ContentHash(mk("9007199254740992"))
	h2, err2 := ContentHash(mk("9007199254740993"))
	h3, err3 := ContentHash(mk("9007199254740992.0"))
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatal(err1, err2, err3)
	}
	if h1 == h2 {
		t.Fatal("two different integers share a content hash")
	}
	if h1 != h3 {
		t.Fatal("one number written two ways has two content hashes")
	}
}

// Ordinary decimals written the same way in the listing and in SKILL.md are
// the same number, and every spelling of one value has one canonical form.
func TestFrontmatterDecimalsAndSpellings(t *testing.T) {
	yamlOf := func(lit string) map[string]any {
		p, err := ParseFrontmatter([]byte("---\nv: " + lit + "\n---\n"))
		if err != nil {
			t.Fatalf("yaml %q: %v", lit, err)
		}
		return p
	}
	jsonOf := func(lit string) map[string]any { return map[string]any{"v": json.Number(lit)} }

	equal := [][2]string{ // JSON literal, YAML literal
		{"0.1", "0.1"}, {"3.14", "3.14"}, {"0.10", "0.1"}, {"1E2", "100"}, {"-0", "0"},
		{"0e401", "0"}, {"-0e999999", "0"}, {"10000000000000000000", "1e19"}, {"2.5e-3", "0.0025"},
		{"9007199254740993", "9007199254740993"}, {"-7", "-7"},
	}
	for _, c := range equal {
		if !FrontmatterEqual(jsonOf(c[0]), yamlOf(c[1])) {
			t.Errorf("JSON %s and YAML %s compared different", c[0], c[1])
		}
	}
	different := [][2]string{{"0.1", "0.2"}, {"1", "-1"}, {"9007199254740993", "9007199254740992"}}
	for _, c := range different {
		if FrontmatterEqual(jsonOf(c[0]), yamlOf(c[1])) {
			t.Errorf("JSON %s and YAML %s compared equal", c[0], c[1])
		}
	}
	// A number too large to hold has no canonical form: it equals nothing,
	// itself included, so such a skill cannot be verified.
	if FrontmatterEqual(jsonOf("1e401"), jsonOf("1e401")) {
		t.Error("an out-of-range number compared equal")
	}
}
