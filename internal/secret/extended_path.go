package secret

import "strings"

// windowsExtendedLengthPath returns the \\?\ form of an absolute Windows path
// so CreateFile accepts it past MAX_PATH (the os package does this itself;
// syscall.CreateFile does not). Because \\?\ turns off Win32 normalization,
// separators are converted to '\', repeated separators are collapsed and
// "." / ".." elements are resolved first (".." never climbs above the drive
// or the \\server\share root). Paths already in the \\?\ or \\.\ namespace,
// and paths that are neither drive-absolute nor UNC, are returned unchanged.
//
// Pure string logic with Windows rules, so it is tested on every platform.
func windowsExtendedLengthPath(p string) string {
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return p
	}
	s := strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(s, `\\?\`) || strings.HasPrefix(s, `\\.\`) {
		return s
	}

	switch {
	case strings.HasPrefix(s, `\\`):
		parts := splitClean(s[2:])
		if len(parts) < 2 {
			return p // not \\server\share\...
		}
		root := parts[:2]
		rest := resolveDots(parts[2:])
		return `\\?\UNC\` + strings.Join(append(root, rest...), `\`)
	case len(s) >= 3 && s[1] == ':' && s[2] == '\\' && isDriveLetter(s[0]):
		rest := resolveDots(splitClean(s[3:]))
		return `\\?\` + s[:2] + `\` + strings.Join(rest, `\`)
	default:
		return p
	}
}

func isDriveLetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// splitClean splits on '\' and drops empty elements (repeated separators).
func splitClean(s string) []string {
	var out []string
	for _, e := range strings.Split(s, `\`) {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// resolveDots drops "." and applies ".." without climbing above the root.
func resolveDots(elems []string) []string {
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		switch e {
		case ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, e)
		}
	}
	return out
}
