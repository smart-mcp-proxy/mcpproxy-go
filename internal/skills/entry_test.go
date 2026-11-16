package skills

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goodDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func validEntry() Entry {
	return Entry{
		URI:         "skill://pdf/SKILL.md",
		Frontmatter: map[string]any{"name": "pdf", "description": "Work with PDFs"},
		Files: []FileRef{
			{URI: "skill://pdf/SKILL.md", Digest: goodDigest, Size: 10},
			{URI: "skill://pdf/scripts/x.py", Digest: goodDigest, Size: 20},
		},
	}
}

func TestDecodeEntry(t *testing.T) {
	t.Run("array resources, unknown fields ignored", func(t *testing.T) {
		raw := `{"uri":"skill://pdf/SKILL.md","extra":1,"frontmatter":{"name":"pdf","description":"d","n":3},
		 "resources":[{"uri":"skill://pdf/SKILL.md","digest":"` + goodDigest + `","size":10,"mime":"x"}]}`
		e, err := DecodeEntry(json.RawMessage(raw))
		require.NoError(t, err)
		assert.Equal(t, "skill://pdf/SKILL.md", e.URI)
		assert.False(t, e.Dynamic)
		assert.Equal(t, []FileRef{{URI: "skill://pdf/SKILL.md", Digest: goodDigest, Size: 10}}, e.Files)
		assert.Equal(t, "pdf", e.Frontmatter["name"])
	})
	t.Run("dynamic", func(t *testing.T) {
		e, err := DecodeEntry(json.RawMessage(`{"uri":"skill://pdf/SKILL.md","frontmatter":{},"resources":"dynamic"}`))
		require.NoError(t, err)
		assert.True(t, e.Dynamic)
		assert.Empty(t, e.Files)
	})
	for name, raw := range map[string]string{
		"other string":      `{"uri":"u","resources":"static"}`,
		"number":            `{"uri":"u","resources":5}`,
		"object":            `{"uri":"u","resources":{}}`,
		"missing resources": `{"uri":"u"}`,
		"null resources":    `{"uri":"u","resources":null}`,
		"uri not string":    `{"uri":3,"resources":[]}`,
		"size not number":   `{"uri":"u","resources":[{"uri":"a","digest":"d","size":"x"}]}`,
		"not an object":     `[1]`,
		"frontmatter array": `{"uri":"u","frontmatter":[],"resources":[]}`,
	} {
		t.Run("error/"+name, func(t *testing.T) {
			_, err := DecodeEntry(json.RawMessage(raw))
			require.Error(t, err)
		})
	}
}

func TestDecodeListResult(t *testing.T) {
	raw := `{"skills":[
		{"uri":"skill://a/SKILL.md","frontmatter":{"name":"a","description":"d"},"resources":[]},
		{"uri":"skill://b/SKILL.md","resources":7},
		{"uri":"skill://c/SKILL.md","frontmatter":{"name":"c","description":"d"},"resources":"dynamic"}
	],"nextCursor":"3","x":1}`
	page, err := DecodeListResult(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Equal(t, "3", page.NextCursor)
	require.Len(t, page.Entries, 2, "one malformed entry must not fail the list")
	assert.Equal(t, "skill://a/SKILL.md", page.Entries[0].URI)
	assert.True(t, page.Entries[1].Dynamic)
	require.Len(t, page.Errors, 1)
	assert.Equal(t, 1, page.Errors[0].Index)
	assert.Equal(t, "skill://b/SKILL.md", page.Errors[0].URI)
	require.Error(t, page.Errors[0].Err)

	t.Run("empty list", func(t *testing.T) {
		page, err := DecodeListResult(json.RawMessage(`{"skills":[]}`))
		require.NoError(t, err)
		assert.Empty(t, page.Entries)
		assert.Empty(t, page.NextCursor)
	})
	for name, bad := range map[string]string{
		"not an object":    `[]`,
		"skills missing":   `{}`,
		"skills not array": `{"skills":{}}`,
		"cursor not str":   `{"skills":[],"nextCursor":1}`,
		"garbage":          `nope`,
	} {
		t.Run("error/"+name, func(t *testing.T) {
			_, err := DecodeListResult(json.RawMessage(bad))
			require.Error(t, err)
		})
	}
}

func TestDecodeGetResult(t *testing.T) {
	e, err := DecodeGetResult(json.RawMessage(`{"skill":{"uri":"skill://a/SKILL.md","frontmatter":{},"resources":[]}}`))
	require.NoError(t, err)
	assert.Equal(t, "skill://a/SKILL.md", e.URI)
	for _, bad := range []string{`{}`, `{"skill":null}`, `{"skill":{"uri":"u","resources":3}}`, `[]`} {
		_, err := DecodeGetResult(json.RawMessage(bad))
		require.Error(t, err, bad)
	}
}

func TestEntryValidate(t *testing.T) {
	require.Nil(t, validEntry().Validate())

	mut := func(f func(e *Entry)) Entry {
		e := validEntry()
		f(&e)
		return e
	}
	tests := []struct {
		name string
		e    Entry
		code string
	}{
		{"dynamic", mut(func(e *Entry) { e.Dynamic = true; e.Files = nil }), ReasonDynamic},
		{"bad uri", mut(func(e *Entry) { e.URI = "skill://pdf/README.md" }), ReasonInvalidURI},
		{"uri too long", mut(func(e *Entry) { e.URI = "skill://" + strings.Repeat("a", MaxURIBytes) + "/SKILL.md" }), ReasonInvalidURI},
		{"name missing", mut(func(e *Entry) { e.Frontmatter = map[string]any{"description": "d"} }), ReasonFrontmatterInvalid},
		{"name not string", mut(func(e *Entry) { e.Frontmatter = map[string]any{"name": 1, "description": "d"} }), ReasonFrontmatterInvalid},
		{"description missing", mut(func(e *Entry) { e.Frontmatter = map[string]any{"name": "pdf"} }), ReasonFrontmatterInvalid},
		{"description not string", mut(func(e *Entry) { e.Frontmatter = map[string]any{"name": "pdf", "description": []any{}} }), ReasonFrontmatterInvalid},
		{"nil frontmatter", mut(func(e *Entry) { e.Frontmatter = nil }), ReasonFrontmatterInvalid},
		{"name mismatch", mut(func(e *Entry) { e.Frontmatter = map[string]any{"name": "other", "description": "d"} }), ReasonNameMismatch},
		{"frontmatter too big", mut(func(e *Entry) {
			e.Frontmatter = map[string]any{"name": "pdf", "description": strings.Repeat("x", MaxFrontmatterBytes)}
		}), ReasonFrontmatterInvalid},
		{"no files", mut(func(e *Entry) { e.Files = nil }), ReasonInvalidURI},
		{"too many files", mut(func(e *Entry) {
			e.Files = []FileRef{{URI: "skill://pdf/SKILL.md", Digest: goodDigest}}
			for i := 0; i < MaxFilesPerSkill; i++ {
				e.Files = append(e.Files, FileRef{URI: fmt.Sprintf("skill://pdf/f%d", i), Digest: goodDigest})
			}
		}), ReasonTooManyFiles},
		{"SKILL.md not listed", mut(func(e *Entry) { e.Files = e.Files[1:] }), ReasonInvalidURI},
		{"file outside root", mut(func(e *Entry) { e.Files[1].URI = "skill://other/x.py" }), ReasonInvalidURI},
		{"file with query", mut(func(e *Entry) { e.Files[1].URI = "skill://pdf/x.py?a=1" }), ReasonInvalidURI},
		{"duplicate relative path", mut(func(e *Entry) {
			e.Files = append(e.Files, FileRef{URI: "skill://pdf/scripts/x.py", Digest: goodDigest})
		}), ReasonInvalidURI},
		{"duplicate via encoding", mut(func(e *Entry) {
			e.Files = append(e.Files, FileRef{URI: "skill://pdf/scripts/%78.py", Digest: goodDigest})
		}), ReasonInvalidURI},
		{"digest md5", mut(func(e *Entry) { e.Files[1].Digest = "md5:abcd" }), ReasonUnsupportedDigest},
		{"digest short", mut(func(e *Entry) { e.Files[1].Digest = "sha256:abcd" }), ReasonUnsupportedDigest},
		{"digest upper case", mut(func(e *Entry) { e.Files[1].Digest = "sha256:" + strings.Repeat("A", 64) }), ReasonUnsupportedDigest},
		{"digest empty", mut(func(e *Entry) { e.Files[1].Digest = "" }), ReasonUnsupportedDigest},
		{"negative size", mut(func(e *Entry) { e.Files[1].Size = -1 }), ReasonTooLarge},
		{"sizes sum over 16 MiB", mut(func(e *Entry) { e.Files[0].Size = MaxSkillBytes; e.Files[1].Size = 1 }), ReasonTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ue := tc.e.Validate()
			require.NotNil(t, ue)
			assert.Equal(t, tc.code, ue.Code)
			assert.NotEmpty(t, ue.Detail)
			assert.Contains(t, ue.Error(), tc.code)
		})
	}

	t.Run("sizes exactly 16 MiB are fine", func(t *testing.T) {
		e := validEntry()
		e.Files[0].Size = MaxSkillBytes - 1
		e.Files[1].Size = 1
		assert.Nil(t, e.Validate())
	})
	t.Run("512 files are fine", func(t *testing.T) {
		e := validEntry()
		e.Files = []FileRef{{URI: "skill://pdf/SKILL.md", Digest: goodDigest}}
		for i := 1; i < MaxFilesPerSkill; i++ {
			e.Files = append(e.Files, FileRef{URI: fmt.Sprintf("skill://pdf/f%d", i), Digest: goodDigest})
		}
		assert.Nil(t, e.Validate())
	})
	t.Run("nested path name is the last segment", func(t *testing.T) {
		e := Entry{
			URI:         "skill://acme/billing/refunds/SKILL.md",
			Frontmatter: map[string]any{"name": "refunds", "description": "d"},
			Files:       []FileRef{{URI: "skill://acme/billing/refunds/SKILL.md", Digest: goodDigest}},
		}
		assert.Nil(t, e.Validate())
	})
}

// A size near the int64 limit must not wrap the running total back under the
// per-skill bound.
func TestValidateSizeSumDoesNotOverflow(t *testing.T) {
	d := "sha256:" + strings.Repeat("0", 64)
	e := Entry{
		URI:         "skill://a/SKILL.md",
		Frontmatter: map[string]any{"name": "a", "description": "d"},
		Files: []FileRef{
			{URI: "skill://a/SKILL.md", Digest: d, Size: 10},
			{URI: "skill://a/big.bin", Digest: d, Size: math.MaxInt64},
		},
	}
	u := e.Validate()
	if u == nil || u.Code != ReasonTooLarge {
		t.Fatalf("Validate = %v, want %s", u, ReasonTooLarge)
	}
}
