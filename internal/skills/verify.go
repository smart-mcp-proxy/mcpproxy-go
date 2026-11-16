package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// VerifyFile checks the bytes against the listing: size first, then sha256.
// Only sha256:<64 lower-case hex> digests are understood.
func VerifyFile(ref FileRef, content []byte) *UnusableError {
	if int64(len(content)) != ref.Size {
		return unusable(ReasonSizeMismatch, "%s: listed %d bytes, read %d", ref.URI, ref.Size, len(content))
	}
	if !sha256DigestRe.MatchString(ref.Digest) {
		return unusable(ReasonUnsupportedDigest, "%s: digest must be sha256:<64 lower-case hex>", ref.URI)
	}
	sum := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(sum[:]) != ref.Digest {
		return unusable(ReasonDigestMismatch, "%s", ref.URI)
	}
	return nil
}

// ParseFrontmatter reads the leading "---" YAML block of a SKILL.md. The block
// is bounded at MaxFrontmatterBytes and must be a mapping.
func ParseFrontmatter(skillMD []byte) (map[string]any, error) {
	b := bytes.TrimPrefix(skillMD, []byte("\xef\xbb\xbf"))
	first, rest, ok := bytes.Cut(b, []byte("\n"))
	if !ok || strings.TrimRight(string(first), " \t\r") != "---" {
		return nil, errors.New("SKILL.md does not start with a --- frontmatter block")
	}

	var block []byte
	closed := false
	for len(rest) > 0 {
		line, tail, _ := bytes.Cut(rest, []byte("\n"))
		if strings.TrimRight(string(line), " \t\r") == "---" {
			closed = true
			break
		}
		block = append(block, line...)
		block = append(block, '\n')
		if len(block) > MaxFrontmatterBytes {
			return nil, fmt.Errorf("frontmatter longer than %d bytes", MaxFrontmatterBytes)
		}
		rest = tail
	}
	if !closed {
		return nil, errors.New("frontmatter block is not closed")
	}

	var v any
	if err := yaml.Unmarshal(block, &v); err != nil {
		return nil, fmt.Errorf("frontmatter is not valid YAML: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("frontmatter must be a mapping with string keys")
	}
	if _, err := json.Marshal(m); err != nil {
		return nil, fmt.Errorf("frontmatter is not representable as JSON: %w", err)
	}
	return m, nil
}

func normalizeJSON(m map[string]any) (any, error) {
	if m == nil {
		m = map[string]any{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FrontmatterEqual compares the listed frontmatter with the one parsed from
// SKILL.md after passing both through JSON, so YAML integers and JSON numbers,
// and nested maps of either origin, compare equal. A value that cannot be
// represented in JSON never compares equal.
func FrontmatterEqual(listed, parsed map[string]any) bool {
	a, err := normalizeJSON(listed)
	if err != nil {
		return false
	}
	b, err := normalizeJSON(parsed)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
