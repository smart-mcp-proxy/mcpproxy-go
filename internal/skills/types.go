// Package skills holds the wire types and pure rules for skills served by an
// upstream MCP server through the io.modelcontextprotocol/skills extension.
// It is a leaf: every layer may import it, and it imports no other package of
// this module.
package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// Reasons a skill cannot be used. They are stored and shown, so they are a
// stable vocabulary.
const (
	ReasonInvalidURI          = "invalid_uri"
	ReasonNameMismatch        = "name_mismatch"
	ReasonDynamic             = "dynamic"
	ReasonTooManyFiles        = "too_many_files"
	ReasonTooLarge            = "too_large"
	ReasonServerBoundExceeded = "server_bound_exceeded"
	ReasonUnsupportedDigest   = "unsupported_digest"
	ReasonDigestMismatch      = "digest_mismatch"
	ReasonSizeMismatch        = "size_mismatch"
	ReasonFrontmatterMismatch = "frontmatter_mismatch"
	ReasonFrontmatterInvalid  = "frontmatter_invalid"
	ReasonReadFailed          = "read_failed"
	ReasonStoredCopyDamaged   = "stored_copy_damaged"
	skillFileName             = "SKILL.md"
	dynamicResources          = "dynamic"
)

// UnusableError says why a skill cannot be used: a machine Code from the
// Reason constants and a human Detail.
type UnusableError struct {
	Code   string
	Detail string
}

func (e *UnusableError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func unusable(code, format string, args ...any) *UnusableError {
	return &UnusableError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// FileRef is one file the listing promises, with the digest and size the
// bytes must match.
type FileRef struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// Entry is one skill as listed by skills/list or returned by skills/get.
type Entry struct {
	URI         string
	Frontmatter map[string]any
	Files       []FileRef
	// Dynamic is set when the server declares resources as "dynamic": the
	// file set cannot be pinned, so the skill is unusable.
	Dynamic bool
}

// EntryError is a listing entry that did not decode.
type EntryError struct {
	Index int
	URI   string
	Err   error
}

// ListPage is one decoded skills/list result. A malformed entry lands in
// Errors and does not fail the page.
type ListPage struct {
	Entries    []Entry
	Errors     []EntryError
	NextCursor string
}

type wireEntry struct {
	URI         string          `json:"uri"`
	Frontmatter json.RawMessage `json:"frontmatter"`
	Resources   json.RawMessage `json:"resources"`
}

// decodeFrontmatter keeps numbers as written (json.Number): a float64 would
// round large integers, and two different listings would then look the same.
func decodeFrontmatter(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// DecodeEntry decodes one wire entry. Unknown fields are ignored.
func DecodeEntry(raw json.RawMessage) (Entry, error) {
	var w wireEntry
	if err := json.Unmarshal(raw, &w); err != nil {
		return Entry{}, fmt.Errorf("decode skill entry: %w", err)
	}
	fm, err := decodeFrontmatter(w.Frontmatter)
	if err != nil {
		return Entry{}, fmt.Errorf("decode skill entry frontmatter: %w", err)
	}
	e := Entry{URI: w.URI, Frontmatter: fm}

	trimmed := string(w.Resources)
	if trimmed == "" || trimmed == "null" {
		return Entry{}, errors.New("skill entry has no resources")
	}
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(w.Resources, &e.Files); err != nil {
			return Entry{}, fmt.Errorf("decode skill resources: %w", err)
		}
	case '"':
		var s string
		if err := json.Unmarshal(w.Resources, &s); err != nil {
			return Entry{}, fmt.Errorf("decode skill resources: %w", err)
		}
		if s != dynamicResources {
			return Entry{}, fmt.Errorf("skill resources string %q is not %q", s, dynamicResources)
		}
		e.Dynamic = true
	default:
		return Entry{}, errors.New("skill resources must be an array or \"dynamic\"")
	}
	return e, nil
}

// DecodeListResult decodes a skills/list result. Only an unusable envelope is
// an error; per-entry failures are reported in the page.
func DecodeListResult(raw json.RawMessage) (ListPage, error) {
	var env struct {
		Skills     []json.RawMessage `json:"skills"`
		NextCursor string            `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ListPage{}, fmt.Errorf("decode skills/list result: %w", err)
	}
	if env.Skills == nil {
		return ListPage{}, errors.New("skills/list result has no skills array")
	}
	page := ListPage{NextCursor: env.NextCursor}
	for i, r := range env.Skills {
		e, err := DecodeEntry(r)
		if err != nil {
			var probe struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(r, &probe)
			page.Errors = append(page.Errors, EntryError{Index: i, URI: probe.URI, Err: err})
			continue
		}
		page.Entries = append(page.Entries, e)
	}
	return page, nil
}

// DecodeGetResult decodes a skills/get result.
func DecodeGetResult(raw json.RawMessage) (Entry, error) {
	var env struct {
		Skill json.RawMessage `json:"skill"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Entry{}, fmt.Errorf("decode skills/get result: %w", err)
	}
	if len(env.Skill) == 0 || string(env.Skill) == "null" {
		return Entry{}, errors.New("skills/get result has no skill")
	}
	return DecodeEntry(env.Skill)
}

var sha256DigestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate applies the rules a listed skill must meet before anything is read
// for it. It returns nil for a usable entry.
func (e Entry) Validate() *UnusableError {
	if e.Dynamic {
		return unusable(ReasonDynamic, "the server declares resources as dynamic")
	}
	root, _, name, err := ParseSkillURI(e.URI)
	if err != nil {
		return unusable(ReasonInvalidURI, "%v", err)
	}

	fm, ok := e.Frontmatter["name"].(string)
	if !ok || fm == "" {
		return unusable(ReasonFrontmatterInvalid, "frontmatter.name must be a non-empty string")
	}
	if d, ok := e.Frontmatter["description"].(string); !ok || d == "" {
		return unusable(ReasonFrontmatterInvalid, "frontmatter.description must be a non-empty string")
	}
	fmJSON, err := json.Marshal(e.Frontmatter)
	if err != nil {
		return unusable(ReasonFrontmatterInvalid, "frontmatter is not JSON: %v", err)
	}
	if len(fmJSON) > MaxFrontmatterBytes {
		return unusable(ReasonFrontmatterInvalid, "frontmatter is %d bytes, limit %d", len(fmJSON), MaxFrontmatterBytes)
	}
	if fm != name {
		return unusable(ReasonNameMismatch, "frontmatter.name %q differs from the last path segment %q", fm, name)
	}

	if len(e.Files) > MaxFilesPerSkill {
		return unusable(ReasonTooManyFiles, "%d files, limit %d", len(e.Files), MaxFilesPerSkill)
	}
	if len(e.Files) == 0 {
		return unusable(ReasonInvalidURI, "no files listed")
	}

	seen := make(map[string]struct{}, len(e.Files))
	var total int64
	hasSkillMD := false
	for _, f := range e.Files {
		if len(f.URI) > MaxURIBytes {
			return unusable(ReasonInvalidURI, "file URI longer than %d bytes", MaxURIBytes)
		}
		rel, err := RelPath(root, f.URI)
		if err != nil {
			return unusable(ReasonInvalidURI, "%v", err)
		}
		if _, dup := seen[rel]; dup {
			return unusable(ReasonInvalidURI, "duplicate file path %s", rel)
		}
		seen[rel] = struct{}{}
		if f.URI == e.URI {
			hasSkillMD = true
		}
		if !sha256DigestRe.MatchString(f.Digest) {
			return unusable(ReasonUnsupportedDigest, "%s: digest must be sha256:<64 lower-case hex>", rel)
		}
		if f.Size < 0 {
			return unusable(ReasonTooLarge, "%s: negative size", rel)
		}
		// Compared by subtraction: adding a size near the int64 limit to the
		// running total would wrap it back under the bound.
		if f.Size > MaxSkillBytes-total {
			return unusable(ReasonTooLarge, "files total more than %d bytes", MaxSkillBytes)
		}
		total += f.Size
		if total > MaxSkillBytes {
			return unusable(ReasonTooLarge, "files total more than %d bytes", MaxSkillBytes)
		}
	}
	if !hasSkillMD {
		return unusable(ReasonInvalidURI, "%s is not among the listed files", skillFileName)
	}
	return nil
}
