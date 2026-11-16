package skills

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
)

// hashPrefix versions the encoding below, as "rv1-" does for tool reviews.
// Changing the encoding means a new prefix, never a silent edit.
const hashPrefix = "sk1-"

// ContentHash returns the approval binding of a skill: sk1- plus the sha256 of
// an unambiguous encoding of the skill URI, every file (sorted by URI) as
// (URI, relative path, digest, size), and the canonical frontmatter encoding. Every
// string is length-prefixed so two different inputs never share an encoding.
func ContentHash(e Entry) (string, error) {
	root, _, _, err := ParseSkillURI(e.URI)
	if err != nil {
		return "", err
	}

	type fileRow struct {
		ref FileRef
		rel string
	}
	rows := make([]fileRow, 0, len(e.Files))
	for _, f := range e.Files {
		rel, err := RelPath(root, f.URI)
		if err != nil {
			return "", err
		}
		rows = append(rows, fileRow{ref: f, rel: rel})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ref.URI < rows[j].ref.URI })

	fm, err := canonicalFrontmatter(e.Frontmatter)
	if err != nil {
		return "", fmt.Errorf("canonicalise frontmatter: %w", err)
	}

	h := sha256.New()
	writeStr := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	writeInt := func(v int64) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}

	writeStr(e.URI)
	writeInt(int64(len(rows)))
	for _, r := range rows {
		writeStr(r.ref.URI)
		writeStr(r.rel)
		writeStr(r.ref.Digest)
		writeInt(r.ref.Size)
	}
	writeStr(string(fm))
	return hashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}
