package skills

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hashEntry() Entry {
	return Entry{
		URI:         "skill://pdf/SKILL.md",
		Frontmatter: map[string]any{"name": "pdf", "description": "d", "meta": map[string]any{"b": 1, "a": []any{"x", "y"}}},
		Files: []FileRef{
			{URI: "skill://pdf/SKILL.md", Digest: goodDigest, Size: 10},
			{URI: "skill://pdf/scripts/x.py", Digest: goodDigest, Size: 20},
		},
	}
}

func mustHash(t *testing.T, e Entry) string {
	t.Helper()
	h, err := ContentHash(e)
	require.NoError(t, err)
	return h
}

// Pinned so an accidental change to the encoding (which would silently void
// every stored approval) fails here instead of in the field.
func TestContentHashGolden(t *testing.T) {
	assert.Equal(t, "sk1-8195fa47721dc3a181011ce0d9944636df0265ca6ee91a1960a6d6c8fd0b966c", mustHash(t, hashEntry()))
}

func TestContentHashStableUnderFileOrder(t *testing.T) {
	a := hashEntry()
	b := hashEntry()
	b.Files[0], b.Files[1] = b.Files[1], b.Files[0]
	assert.Equal(t, mustHash(t, a), mustHash(t, b))
}

func TestContentHashStableUnderFrontmatterKeyOrderAndNumberType(t *testing.T) {
	a := hashEntry()
	b := hashEntry()
	b.Frontmatter = map[string]any{"meta": map[string]any{"a": []any{"x", "y"}, "b": float64(1)}, "description": "d", "name": "pdf"}
	assert.Equal(t, mustHash(t, a), mustHash(t, b))
}

func TestContentHashChanges(t *testing.T) {
	base := mustHash(t, hashEntry())
	other := "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	mutations := map[string]func(e *Entry){
		"file digest":   func(e *Entry) { e.Files[1].Digest = other },
		"file size":     func(e *Entry) { e.Files[1].Size++ },
		"file rel path": func(e *Entry) { e.Files[1].URI = "skill://pdf/scripts/y.py" },
		"added file": func(e *Entry) {
			e.Files = append(e.Files, FileRef{URI: "skill://pdf/z", Digest: goodDigest})
		},
		"removed file": func(e *Entry) { e.Files = e.Files[:1] },
		"description":  func(e *Entry) { e.Frontmatter["description"] = "e" },
		"nested value": func(e *Entry) { e.Frontmatter["meta"].(map[string]any)["b"] = 2 },
		"extra key":    func(e *Entry) { e.Frontmatter["k"] = "v" },
		"skill uri": func(e *Entry) {
			e.URI = "skill://other/SKILL.md"
			e.Files = []FileRef{{URI: "skill://other/SKILL.md", Digest: goodDigest, Size: 10}}
		},
	}
	for name, f := range mutations {
		t.Run(name, func(t *testing.T) {
			e := hashEntry()
			f(&e)
			assert.NotEqual(t, base, mustHash(t, e))
		})
	}
}

func TestContentHashNoCollisionAcrossFieldBoundaries(t *testing.T) {
	x := Entry{URI: "skill://s/SKILL.md", Frontmatter: map[string]any{}, Files: []FileRef{{URI: "skill://s/ab", Digest: goodDigest}, {URI: "skill://s/c", Digest: goodDigest}}}
	y := Entry{URI: "skill://s/SKILL.md", Frontmatter: map[string]any{}, Files: []FileRef{{URI: "skill://s/a", Digest: goodDigest}, {URI: "skill://s/bc", Digest: goodDigest}}}
	assert.NotEqual(t, mustHash(t, x), mustHash(t, y))

	a := Entry{URI: "skill://ab/SKILL.md", Frontmatter: map[string]any{"c": "d"}}
	b := Entry{URI: "skill://a/SKILL.md", Frontmatter: map[string]any{"bc": "d"}}
	assert.NotEqual(t, mustHash(t, a), mustHash(t, b))
}

func TestContentHashErrors(t *testing.T) {
	e := hashEntry()
	e.URI = "not a skill uri"
	_, err := ContentHash(e)
	require.Error(t, err)

	e = hashEntry()
	e.Files[1].URI = "skill://elsewhere/x"
	_, err = ContentHash(e)
	require.Error(t, err)

	e = hashEntry()
	e.Frontmatter["bad"] = func() {}
	_, err = ContentHash(e)
	require.Error(t, err)
}
