package shellwrap

import (
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// dockerEnvFlagRegex matches a docker env flag (`-e` / `--env`) followed by a
// whitespace-separated value token within a command STRING. The value token is
// either single/double-quoted (it may then contain spaces) or a run of
// non-whitespace characters. Group 1 anchors the flag to a word boundary
// (start-of-string or preceding whitespace) so it does not match inside other
// flags or image names.
var dockerEnvFlagRegex = regexp.MustCompile(`(^|\s)(--env|-e)(\s+)('[^']*'|"[^"]*"|\S+)`)

// RedactDockerArgs returns a copy of a docker-style argv slice with the VALUE
// of every `KEY=VALUE` env injection masked, leaving keys, flags, image names,
// and other args untouched. It handles three shapes the spawn path produces:
//
//   - two-token form:   ["-e", "KEY=VALUE"]               (direct-exec docker)
//   - glued single tok:  ["-eKEY=VALUE"] / ["--env=KEY=VALUE"]
//   - command-string el: ["-l", "-c", "docker run -e KEY=VALUE img"] (shell wrap)
//
// The input slice is never mutated. A nil input returns nil.
func RedactDockerArgs(args []string) []string {
	if args == nil {
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		// Two-token form: `-e`/`--env` followed by `KEY=VALUE`.
		if (a == "-e" || a == "--env") && i+1 < len(args) {
			out = append(out, a, maskEnvToken(args[i+1]))
			i++
			continue
		}
		out = append(out, redactArgElement(a))
	}
	return out
}

// redactArgElement masks env secrets inside a single argv element, covering the
// glued single-token forms and the embedded command-string form.
func redactArgElement(a string) string {
	// Command-string element (e.g. the `-c` argument of a login-shell wrap):
	// mask any embedded `-e KEY=VALUE` occurrences.
	if strings.ContainsAny(a, " \t") {
		return RedactDockerCommandString(a)
	}
	// Glued long form: `--env=KEY=VALUE`.
	if rest, ok := strings.CutPrefix(a, "--env="); ok {
		return "--env=" + maskEnvToken(rest)
	}
	// Glued short form: `-eKEY=VALUE`.
	if strings.HasPrefix(a, "-e") && len(a) > 2 && strings.Contains(a[2:], "=") {
		return "-e" + maskEnvToken(a[2:])
	}
	return a
}

// RedactDockerCommandString masks the VALUE of any `-e KEY=VALUE` /
// `--env KEY=VALUE` flag found within a single command string, leaving the rest
// of the string (subcommand, flags, image name) intact.
func RedactDockerCommandString(s string) string {
	return dockerEnvFlagRegex.ReplaceAllStringFunc(s, func(m string) string {
		sub := dockerEnvFlagRegex.FindStringSubmatch(m)
		// sub[1]=lead, sub[2]=flag, sub[3]=whitespace, sub[4]=value token
		return sub[1] + sub[2] + sub[3] + maskEnvToken(sub[4])
	})
}

// maskEnvToken masks the value portion of a `KEY=VALUE` token, preserving the
// key and any surrounding single/double quotes. Tokens without a `=` or with an
// empty value are returned unchanged (they are not secret-bearing env values).
func maskEnvToken(tok string) string {
	quote := ""
	inner := tok
	if len(inner) >= 2 {
		if (inner[0] == '\'' && inner[len(inner)-1] == '\'') ||
			(inner[0] == '"' && inner[len(inner)-1] == '"') {
			quote = string(inner[0])
			inner = inner[1 : len(inner)-1]
		}
	}
	eq := strings.IndexByte(inner, '=')
	if eq < 0 {
		return tok
	}
	key := inner[:eq]
	val := inner[eq+1:]
	if val == "" {
		return tok
	}
	return quote + key + "=" + secret.MaskSecretValue(val) + quote
}
