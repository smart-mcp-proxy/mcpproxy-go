package skills

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const skillSuffix = "/" + skillFileName

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// checkSegment validates one path segment and returns its decoded form. The
// decoded form is what a filesystem-like consumer would see, so it is the one
// that must not be a separator or a dot segment.
func checkSegment(seg string) (string, error) {
	if seg == "" {
		return "", errors.New("empty path segment")
	}
	dec, err := url.PathUnescape(seg)
	if err != nil {
		return "", fmt.Errorf("bad percent-encoding in %q", seg)
	}
	switch {
	case dec == "" || dec == "." || dec == "..":
		return "", fmt.Errorf("dot or empty segment %q", seg)
	case strings.ContainsAny(dec, `/\`):
		return "", fmt.Errorf("segment %q decodes to a separator", seg)
	case hasControl(dec):
		return "", fmt.Errorf("segment %q decodes to a control character", seg)
	}
	return dec, nil
}

// ParseSkillURI splits a skill address. The address must end in /SKILL.md;
// root is the address without that suffix, path is the root without scheme and
// "//", and name is the last segment of path.
func ParseSkillURI(uri string) (root, path, name string, err error) {
	if len(uri) > MaxURIBytes {
		return "", "", "", fmt.Errorf("skill URI longer than %d bytes", MaxURIBytes)
	}
	if hasControl(uri) {
		return "", "", "", errors.New("skill URI contains a control character")
	}
	if strings.ContainsAny(uri, "?#\\") {
		return "", "", "", errors.New("skill URI must not contain a query, fragment or backslash")
	}
	root, ok := strings.CutSuffix(uri, skillSuffix)
	if !ok {
		return "", "", "", fmt.Errorf("skill URI must end in %s", skillSuffix)
	}
	scheme, path, ok := strings.Cut(root, "://")
	if !ok || scheme == "" {
		return "", "", "", errors.New("skill URI has no scheme")
	}
	if path == "" {
		return "", "", "", errors.New("skill URI has no path")
	}
	segs := strings.Split(path, "/")
	for _, s := range segs {
		if _, err := checkSegment(s); err != nil {
			return "", "", "", err
		}
	}
	return root, path, segs[len(segs)-1], nil
}

// RelPath returns where fileURI sits inside the skill rooted at root. It
// rejects anything that is not strictly below root: another scheme or host, a
// query or fragment, an encoded separator or dot segment, an escaping "..", an
// absolute path, an empty segment or a control character.
func RelPath(root, fileURI string) (string, error) {
	if len(fileURI) > MaxURIBytes {
		return "", fmt.Errorf("file URI longer than %d bytes", MaxURIBytes)
	}
	if hasControl(fileURI) {
		return "", errors.New("file URI contains a control character")
	}
	rest, ok := strings.CutPrefix(fileURI, root+"/")
	if !ok {
		return "", fmt.Errorf("file URI %q is not under %q", fileURI, root)
	}
	if strings.ContainsAny(rest, "?#\\") {
		return "", fmt.Errorf("file URI %q has a query, fragment or backslash", fileURI)
	}
	segs := strings.Split(rest, "/")
	dec := make([]string, len(segs))
	for i, s := range segs {
		d, err := checkSegment(s)
		if err != nil {
			return "", fmt.Errorf("file URI %q: %w", fileURI, err)
		}
		dec[i] = d
	}
	return strings.Join(dec, "/"), nil
}
