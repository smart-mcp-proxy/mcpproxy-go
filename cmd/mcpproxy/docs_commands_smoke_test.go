package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ACT-03: published command examples must exist in the shipped binary. This
// test parses every `mcpproxy ...` command line in the guide pages below (both
// fenced blocks and inline code spans) and checks that each subcommand and flag
// resolves in the real cobra tree, so a guide can never advertise a command or
// flag the release does not have.

var (
	docsRootOnce sync.Once
	docsRoot     *cobra.Command
)

func docsSmokeRoot() *cobra.Command {
	docsRootOnce.Do(func() { docsRoot = newRootCmd() })
	return docsRoot
}

// docsSmokeFiles are the guides whose command lines are checked. Paths are
// relative to the repository root.
var docsSmokeFiles = []string{
	"docs/features/connect-clients.md",
	"docs/features/profiles.md",
	"docs/cli/management-commands.md",
	"docs/cli/review-commands.md",
	"docs/cli/profile-commands.md",
	"docs/cli/security-commands.md",
}

var inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")

// extractMCPProxyCommands returns (line number, command text) for every
// command line starting with "mcpproxy " in fenced blocks and inline spans.
func extractMCPProxyCommands(path string) ([]struct {
	Line int
	Cmd  string
}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	type hit = struct {
		Line int
		Cmd  string
	}
	var out []hit
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	inFence := false
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			c := strings.TrimPrefix(trim, "$ ")
			if strings.HasPrefix(c, "mcpproxy ") {
				start := n
				for strings.HasSuffix(c, "\\") && sc.Scan() {
					n++
					c = strings.TrimSpace(strings.TrimSuffix(c, "\\")) + " " + strings.TrimSpace(sc.Text())
				}
				out = append(out, hit{start, strings.TrimSpace(strings.TrimSuffix(c, "\\"))})
			}
			continue
		}
		for _, m := range inlineCodeRe.FindAllStringSubmatch(line, -1) {
			if c := strings.TrimSpace(m[1]); strings.HasPrefix(c, "mcpproxy ") {
				out = append(out, hit{n, c})
			}
		}
	}
	return out, sc.Err()
}

// splitCommandLine tokenizes a shell-ish line, honouring quotes and dropping a
// trailing "# comment".
func splitCommandLine(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			toks = append(toks, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ' || r == '\t':
			flush()
		case r == '#' && !started:
			flush()
			return toks
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return toks
}

// checkDocCommand resolves one documented command against the cobra tree and
// returns a description of the first mismatch, or "".
func checkDocCommand(root *cobra.Command, line string) string {
	toks := splitCommandLine(line)
	if len(toks) < 2 || toks[0] != "mcpproxy" {
		return ""
	}
	cmd := root
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		// "--" ends flag parsing: the rest is the upstream server's own command line.
		if t == "--" || t == "|" || t == "&&" || t == ">" || t == "\\" || isElision(t) {
			break
		}
		var fl *pflag.Flag
		switch {
		case strings.HasPrefix(t, "--"):
			name := strings.SplitN(strings.TrimPrefix(t, "--"), "=", 2)[0]
			if name == "" || name == "help" {
				continue
			}
			if fl = cmd.Flag(name); fl == nil {
				fl = root.PersistentFlags().Lookup(name)
			}
			if fl == nil {
				return fmt.Sprintf("flag --%s does not exist on %q", name, cmd.CommandPath())
			}
			if fl.Value.Type() != "bool" && !strings.Contains(t, "=") {
				i++ // the next token is the flag's value
			}
			continue
		case len(t) >= 2 && t[0] == '-' && t[1] != '-' && !isDigit(t[1]):
			consumeNext, msg := checkShortFlags(cmd, t)
			if msg != "" {
				return msg
			}
			if consumeNext {
				i++
			}
			continue
		case strings.HasPrefix(t, "-") || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "["):
			continue // placeholder or negative number
		}
		// A plain word: a subcommand, or a positional argument of a leaf command.
		if next := findSub(cmd, t); next != nil {
			cmd = next
			continue
		}
		if cmd == root {
			return fmt.Sprintf("unknown command %q", t)
		}
		if cmd.HasSubCommands() && cmd.Args == nil {
			// A group command with no positional-args contract: an unknown
			// word is a missing subcommand.
			return fmt.Sprintf("unknown subcommand %q under %q", t, cmd.CommandPath())
		}
	}
	return ""
}

// checkShortFlags validates one single-dash token: a lone shorthand (-o), an
// attached value (-o=json, -ojson) or a cluster of boolean shorthands (-yv).
// It returns whether the next token is the value of a trailing non-bool
// shorthand, or a description of the first unknown shorthand.
func checkShortFlags(cmd *cobra.Command, t string) (consumeNext bool, msg string) {
	body := t[1:]
	for j := 0; j < len(body); j++ {
		fl := lookupShort(cmd, string(body[j]))
		if fl == nil {
			return false, fmt.Sprintf("flag -%c (in %s) does not exist on %q", body[j], t, cmd.CommandPath())
		}
		if fl.Value.Type() == "bool" {
			continue
		}
		// A value-taking shorthand: the remainder (after an optional "=") is
		// its value; when nothing remains, the next token is.
		return j == len(body)-1, ""
	}
	return false, ""
}

// isElision reports a placeholder token such as "...", "…" or "a|b|c".
func isElision(t string) bool {
	return t == "|" || t == "\\" || strings.Contains(t, "...") || strings.Contains(t, "…") || strings.Contains(t, "|")
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func lookupShort(cmd *cobra.Command, sh string) *pflag.Flag {
	if f := cmd.Flags().ShorthandLookup(sh); f != nil {
		return f
	}
	return cmd.InheritedFlags().ShorthandLookup(sh)
}

func findSub(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
		for _, a := range c.Aliases {
			if a == name {
				return c
			}
		}
	}
	return nil
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func TestDocsCommandsExistInCobraTree(t *testing.T) {
	root := docsSmokeRoot()
	repo := repoRootFromTest(t)
	checked := 0
	for _, rel := range docsSmokeFiles {
		cmds, err := extractMCPProxyCommands(filepath.Join(repo, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		for _, c := range cmds {
			checked++
			if msg := checkDocCommand(root, c.Cmd); msg != "" {
				t.Errorf("%s:%d: `%s`: %s", rel, c.Line, c.Cmd, msg)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d documented commands found; the extractor is broken", checked)
	}
}

// The checker must actually bite: a nonexistent subcommand and flag fail.
func TestDocsCommandCheckerRejectsMissing(t *testing.T) {
	root := docsSmokeRoot()
	good := []string{
		"mcpproxy connect cursor --profile work --lock",
		"mcpproxy client set-profile cursor work-full",
		"mcpproxy upstream add fs -- npx -y pkg",
		"mcpproxy review approve fs --yes # comment",
		"mcpproxy --json client list",
		"mcpproxy -o json upstream list",
		"mcpproxy -o=json upstream list",
		"mcpproxy -ojson upstream list",
		"mcpproxy upstream patch obsidian --env \"A=B\" --env-remove OLD",
	}
	for _, g := range good {
		if msg := checkDocCommand(root, g); msg != "" {
			t.Errorf("%q wrongly rejected: %s", g, msg)
		}
	}
	bad := []string{
		"mcpproxy client frobnicate cursor",
		"mcpproxy connect cursor --no-such-flag",
		"mcpproxy nosuchcommand",
		"mcpproxy review approve fs --bogus",
		"mcpproxy --json client frobnicate",
		"mcpproxy --no-such-global client list",
		"mcpproxy connect cursor -Z=1",
		"mcpproxy connect cursor -Z",
		"mcpproxy connect cursor -Zo json",
		"mcpproxy -oZ=1 -Z client list",
		"mcpproxy upstream patch obsidian --env A=B --no-such-flag",
	}
	for _, b := range bad {
		if msg := checkDocCommand(root, b); msg == "" {
			t.Errorf("%q wrongly accepted", b)
		}
	}
}

var minReleaseRe = regexp.MustCompile(`(?i)requires mcpproxy v(\d+\.\d+\.\d+)`)

// Version gate: the client guide must state the first release that has the
// commands it documents, and that release must not be newer than the tree.
func TestConnectClientsGuideStatesMinimumRelease(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootFromTest(t), "docs/features/connect-clients.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := minReleaseRe.FindSubmatch(b)
	if m == nil {
		t.Fatal("docs/features/connect-clients.md must state \"Requires MCPProxy vX.Y.Z or later\" for the profile/client commands")
	}
	// The client and connect --profile commands first shipped in v0.70.0.
	if string(m[1]) != "0.70.0" {
		t.Errorf("minimum release = v%s, want v0.70.0 (first release with `mcpproxy client` and `connect --profile`)", m[1])
	}
}

// Continuation lines of a fenced command are part of that command, so flags on
// them are checked too.
func TestDocsExtractorJoinsContinuationLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "g.md")
	doc := "```bash\nmcpproxy upstream patch obsidian-pilot \\\n  --env \"A=B\" --no-such-flag\n```\n"
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds, err := extractMCPProxyCommands(p)
	if err != nil || len(cmds) != 1 {
		t.Fatalf("cmds=%v err=%v", cmds, err)
	}
	if !strings.Contains(cmds[0].Cmd, "--no-such-flag") {
		t.Fatalf("continuation line dropped: %q", cmds[0].Cmd)
	}
	if msg := checkDocCommand(docsSmokeRoot(), cmds[0].Cmd); msg == "" {
		t.Errorf("bad flag on a continuation line accepted: %q", cmds[0].Cmd)
	}
}
