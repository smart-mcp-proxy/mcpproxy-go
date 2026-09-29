// Package shellwords implements a small, dependency-free, quote-aware
// tokenizer for a single command line, shared by the Paste-source command
// detector (internal/configimport, FR-064) and the catalog install-command
// splitter (internal/registries, data-model §9 CatalogInstall). It is
// deliberately independent of internal/oauth's spawnargv helpers, which parse
// argv already split by the shell for a different purpose (OAuth browser
// launch); duplicating a ~30-line tokenizer here is cheaper than coupling the
// two packages.
package shellwords

import (
	"fmt"
	"strings"
	"unicode"
)

// Split tokenizes s the way a POSIX shell would for a single command line:
// whitespace-separated words, with '...' and "..." both grouping their
// contents into one word (no expansion of any kind — this only ever feeds a
// preview, never a shell).
//
// Backslash escaping follows POSIX with one deliberate softening so that
// pasted Windows paths survive: inside single quotes a backslash is always
// literal; inside double quotes it escapes only ", \, $ and `; outside quotes
// it escapes only whitespace, ' and " (NOT another backslash), so
// C:\Users\me\node.exe and \\host\share stay intact. A backslash before any
// other character, or at the very end of the input, is kept literally.
//
// Returns an error if a quote is left unterminated. An empty or
// whitespace-only input returns (nil, nil).
func Split(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	haveWord := false

	var quote rune // 0, '\'' or '"'
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '"' && r == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[i+1]):
			i++
			cur.WriteRune(runes[i])
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
		case r == '\\' && i+1 < len(runes) && (unicode.IsSpace(runes[i+1]) || runes[i+1] == '\'' || runes[i+1] == '"'):
			i++
			cur.WriteRune(runes[i])
			haveWord = true
		case r == '\'' || r == '"':
			quote = r
			haveWord = true
		case unicode.IsSpace(r):
			if haveWord {
				words = append(words, cur.String())
				cur.Reset()
				haveWord = false
			}
		default:
			cur.WriteRune(r)
			haveWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if haveWord {
		words = append(words, cur.String())
	}
	return words, nil
}
