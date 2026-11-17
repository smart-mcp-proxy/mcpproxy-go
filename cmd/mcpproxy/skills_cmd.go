package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 125 (skills over MCP): `mcpproxy skills list|show|refresh` (PR-1) and
// `approve|block|unblock` (PR-2). Daemon-only and administrator-only, like the
// REST endpoints they call: nothing here is offered to agents yet.

const skillsCallTimeout = 30 * time.Second

const skillsDisabledLine = "Skills over MCP is off. Set enable_skills: true in the configuration to turn it on."

// GetSkillsCommand returns the `skills` command group.
func GetSkillsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Inspect the skills that upstream servers serve (administrators)",
		Long: `Inspect the agent skills that upstream MCP servers serve through the skills
extension. mcpproxy reads each skill, checks the size and SHA-256 of every file
and keeps a record you can list and read. A skill stays held until an
administrator approves the exact content that was reviewed; skills are not
offered to agents yet.

Skills over MCP is off by default. Set enable_skills: true in the configuration
to turn it on, and expose_skills: false on a server to exclude that server.

Examples:
  mcpproxy skills list
  mcpproxy skills list --server hf
  mcpproxy skills show hf:hf-cli
  mcpproxy skills show hf:hf-cli --file references/auth.md
  mcpproxy skills show hf:hf-cli --diff
  mcpproxy skills refresh hf
  mcpproxy skills approve hf:hf-cli
  mcpproxy skills approve --server hf --all --yes
  mcpproxy skills block hf:hf-cli`,
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsShowCmd(), newSkillsRefreshCmd(),
		newSkillsApproveCmd(), newSkillsBlockCmd(true), newSkillsBlockCmd(false))
	silenceUsageOnRun(cmd)
	return cmd
}

func newSkillsListCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List skills, for one server or for every server",
		Long: `List the skills mcpproxy holds. Without --server every server is listed in
one table. A server that failed its last refresh or whose listing hit a limit
gets a line after the table.

Examples:
  mcpproxy skills list
  mcpproxy skills list --server hf -o json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error { return termSafeErr(runSkillsList(server)) },
	}
	cmd.Flags().StringVarP(&server, "server", "s", "", "List only this server")
	return cmd
}

func newSkillsShowCmd() *cobra.Command {
	var file string
	var approved, diff bool
	cmd := &cobra.Command{
		Use:   "show <server>:<skill>",
		Short: "Show one skill and the text of one of its files, or what changed since approval",
		Long: `Show a skill: name, description, status, content hash, who approved it and the
scan result, its files, then the text of one file (SKILL.md unless --file is
given). Long files are read in chunks until the end. A binary file cannot be
shown.

With --diff, show what changed since the last approval instead: the files that
were added, removed or changed, and for every text file that changed a unified
diff of the approved copy against the current copy (each file is cut at 1 MiB;
binary files are named but not compared). A skill that was never approved has
nothing to compare.

Examples:
  mcpproxy skills show hf:hf-cli
  mcpproxy skills show hf:hf-cli --file references/auth.md
  mcpproxy skills show hf:hf-cli --approved
  mcpproxy skills show hf:hf-cli --diff`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if diff {
				if cmd.Flags().Changed("file") || approved {
					return newFlagValidationError("--diff cannot be combined with --file or --approved")
				}
				return termSafeErr(runSkillsShowDiff(args[0]))
			}
			return termSafeErr(runSkillsShow(args[0], file, approved))
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "File inside the skill (default SKILL.md)")
	cmd.Flags().BoolVar(&approved, "approved", false, "Read the approved copy instead of the current one")
	cmd.Flags().BoolVar(&diff, "diff", false, "Show what changed since the last approval")
	return cmd
}

func newSkillsRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh <server>",
		Short: "Read a server's skills again and verify them",
		Long: `Ask the server for its skills again, verify every file and show the result.
Needs the server to announce the skills extension.

Examples:
  mcpproxy skills refresh hf`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error { return termSafeErr(runSkillsRefresh(args[0])) },
	}
}

// --- list ---

func runSkillsList(server string) error {
	s, err := newRESTSession("skills")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillsCallTimeout)
	defer cancel()

	names := []string{server}
	if server == "" {
		rows, err := s.client.GetServers(ctx)
		if err != nil {
			return fmt.Errorf("failed to list servers: %w", err)
		}
		names = names[:0]
		for _, row := range rows {
			if n, _ := row["name"].(string); n != "" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
	}

	var lists []*contracts.SkillsListResponse
	for _, name := range names {
		resp, err := s.client.ListServerSkills(ctx, name)
		if err != nil {
			return err
		}
		if !resp.Enabled {
			// The switch is global: one answer covers every server.
			lists = []*contracts.SkillsListResponse{resp}
			break
		}
		lists = append(lists, resp)
	}
	if structuredOutput() {
		if server != "" {
			return printJSONValue(lists[0])
		}
		if lists == nil {
			lists = []*contracts.SkillsListResponse{}
		}
		return printJSONValue(lists)
	}
	if len(lists) > 0 && !lists[0].Enabled {
		fmt.Println(skillsDisabledLine)
		return nil
	}
	return printSkillsTable(lists)
}

func printSkillsTable(lists []*contracts.SkillsListResponse) error {
	var rows [][]string
	var notes []string
	for _, l := range lists {
		for _, e := range l.Skills {
			rows = append(rows, []string{termSafe(l.Server), termSafe(e.Path), termSafe(skillStatusCell(e)), strconv.Itoa(len(e.Files)), humanBytes(e.TotalSize), termSafe(skillScanLabel(e))})
		}
		if l.Error != "" {
			notes = append(notes, termSafe(l.Server+": "+l.Error))
		}
		if l.Truncated {
			notes = append(notes, termSafe(l.Server)+": listing truncated (a skills limit was reached; some skills are missing)")
		}
	}
	if len(rows) == 0 {
		fmt.Println("No skills found.")
	} else if err := printTable([]string{"SERVER", "SKILL", "STATUS", "FILES", "SIZE", "SCAN"}, rows); err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Println(n)
	}
	return nil
}

func skillStatusCell(e contracts.SkillEntry) string {
	st := e.Status
	if st == "unusable" && e.UnusableReason != "" {
		st = "unusable (" + e.UnusableReason + ")"
	}
	if e.Blocked {
		st += ", blocked"
	}
	return st
}

// skillScanLabel says what the scan of the skill's current content found, in
// the words an approval review needs: the verdict, "scan incomplete" (with the
// reason) when the scan did not cover everything, or "not scanned", and the
// number of findings.
func skillScanLabel(e contracts.SkillEntry) string {
	sc := e.Scan
	if sc.Verdict == "" && sc.ScannedAt == nil {
		return "not scanned"
	}
	label := sc.Verdict
	if !sc.Complete {
		label = "scan incomplete"
		if sc.Incomplete != "" {
			label += " (" + sc.Incomplete + ")"
		}
		if sc.Verdict != "" {
			label += ", " + sc.Verdict
		}
	}
	more := ""
	if sc.FindingsTruncated {
		more = "+"
	}
	return label + fmt.Sprintf(", %d%s finding(s)", len(sc.Findings), more)
}

// termSafe makes text that came from an upstream server safe to print on one
// line of a terminal. Skill names, descriptions, paths and reasons are shown
// before anyone approved them, and an escape sequence printed raw would be
// carried out by the terminal: clear the screen, set the title, write the
// clipboard, reverse the reading order. Control characters, the Unicode
// direction and invisible-format characters, and bytes that are not UTF-8 are
// shown as visible escapes instead. Structured output (-o json|yaml) goes
// through printJSONValue, which keeps the same characters off the terminal.
func termSafe(s string) string { return termEscape(s, false) }

// escapedError is an error whose message is safe to print; the error it wraps
// is still there for errors.Is and errors.As.
type escapedError struct{ err error }

func (e escapedError) Error() string { return termSafe(e.err.Error()) }
func (e escapedError) Unwrap() error { return e.err }

// termSafeErr escapes the message of an error a skills command returns. Such a
// message can quote an upstream server (the text of its JSON-RPC error), and
// it is printed by the command's error path, not by the code above.
//
// A refusal by the core is also marked as one (cliRefusalError): its text can
// carry a skill name or path, and the exit code must not depend on whether
// that name happens to say "port in use" or "permission denied".
func termSafeErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *cliclient.APIError
	var refusal cliRefusalError
	if errors.As(err, &apiErr) && !errors.As(err, &refusal) {
		err = cliRefusalError{err}
	}
	return escapedError{err}
}

// termSafeText is termSafe for a block of text: line feeds and tabs stay.
func termSafeText(s string) string { return termEscape(s, true) }

func termEscape(s string, keepLayout bool) string {
	clean := true
	for _, r := range s {
		if termUnsafe(r, keepLayout) {
			clean = false
			break
		}
	}
	if clean && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case !termUnsafe(r, keepLayout):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += n
	}
	return b.String()
}

// termUnsafe reports whether a rune can change what a terminal shows beyond
// its own glyph.
func termUnsafe(r rune, keepLayout bool) bool {
	switch {
	case keepLayout && (r == '\n' || r == '\t'):
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f: // C0, DEL, C1
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, // direction marks
		r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, // embeddings, overrides, isolates
		r == 0x2028, r == 0x2029, // line and paragraph separators
		r == 0xfeff: // zero-width no-break space
		return true
	}
	return false
}

// printJSONValue prints v through the -o json|yaml path, and keeps what a
// terminal would act on out of the output. Go's JSON encoder escapes the C0
// controls but writes the direction overrides, the C1 controls and DEL as
// they are, and the YAML encoder writes most of them too.
//
// JSON stays exact: each such character becomes its \uXXXX escape, which a
// JSON parser decodes back. YAML has no escape that works in every scalar
// style, so there the characters are shown as visible text (\u202e), as in
// the table output; -o json is the format for the exact text.
func printJSONValue(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if ResolveOutputFormat() == "yaml" {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		out, err := yaml.Marshal(termSafeValue(decoded))
		if err != nil {
			return err
		}
		fmt.Print(string(out))
		return nil
	}
	return printData(jsonTermSafe(raw))
}

// jsonTermSafe replaces every character of an encoded JSON document that a
// terminal would act on with its \uXXXX escape. Such a character can only sit
// inside a string (the encoder writes nothing else outside ASCII), and all of
// them are in the basic plane, so the document decodes to the same value.
func jsonTermSafe(raw []byte) []byte {
	clean := true
	for _, r := range string(raw) {
		if termUnsafe(r, true) {
			clean = false
			break
		}
	}
	if clean {
		return raw
	}
	var b bytes.Buffer
	b.Grow(len(raw) + 32)
	for _, r := range string(raw) {
		if termUnsafe(r, true) {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.Bytes()
}

// termSafeValue escapes every string (keys included) of a decoded JSON value.
func termSafeValue(v any) any {
	switch t := v.(type) {
	case string:
		return termSafeText(t)
	case []any:
		for i := range t {
			t[i] = termSafeValue(t[i])
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[termSafe(k)] = termSafeValue(val)
		}
		return out
	}
	return v
}

// --- show ---

// skillShowDoc is the -o json|yaml document of `skills show`.
type skillShowDoc struct {
	contracts.SkillEntry
	File string `json:"file"`
	Copy string `json:"copy"`
	Text string `json:"text"`
}

// skillEntryByPath picks the record a path means, by the rule the core's
// content endpoint uses: the only record with that path, or, when an earlier
// record that is no longer listed shares it with one listed now, the listed
// one. Any other overlap is refused rather than guessed.
func skillEntryByPath(entries []contracts.SkillEntry, path string) (*contracts.SkillEntry, error) {
	var all, listed []*contracts.SkillEntry
	for i := range entries {
		if entries[i].Path != path {
			continue
		}
		all = append(all, &entries[i])
		if entries[i].Listed {
			listed = append(listed, &entries[i])
		}
	}
	switch {
	case len(all) == 0:
		return nil, errors.New("skill not found")
	case len(all) == 1:
		return all[0], nil
	case len(listed) == 1:
		return listed[0], nil
	}
	return nil, errors.New("more than one skill record has this path; run `mcpproxy skills list` to see them")
}

func runSkillsShow(target, file string, approved bool) error {
	idx := strings.Index(target, ":")
	if idx <= 0 || idx == len(target)-1 {
		return newFlagValidationError("invalid target %q: use <server>:<skill>", target)
	}
	server, skill := target[:idx], target[idx+1:]
	if file == "" {
		file = "SKILL.md"
	}
	copyName := "current"
	if approved {
		copyName = "approved"
	}

	s, err := newRESTSession("skills")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*skillsCallTimeout)
	defer cancel()

	list, err := s.client.ListServerSkills(ctx, server)
	if err != nil {
		return err
	}
	if !list.Enabled {
		return fmt.Errorf("%s", skillsDisabledLine)
	}
	entry, err := skillEntryByPath(list.Skills, skill)
	if err != nil {
		return fmt.Errorf("%w: %s", err, target)
	}

	listed := entry.Files
	if approved {
		listed = entry.ApprovedFiles
	}
	body, _, err := readSkillFile(ctx, s.client, server, skill, file, approved, 0, skillFileDigest(listed, file))
	if err != nil {
		return err
	}

	if structuredOutput() {
		return printJSONValue(skillShowDoc{SkillEntry: *entry, File: file, Copy: copyName, Text: body})
	}
	fmt.Printf("Skill:        %s\n", termSafe(entry.ID))
	fmt.Printf("Name:         %s\n", termSafe(entry.Name))
	fmt.Printf("Description:  %s\n", termSafe(entry.Description))
	fmt.Printf("Status:       %s\n", termSafe(skillStatusCell(*entry)))
	fmt.Printf("Hash:         %s\n", termSafe(dash(entry.CurrentHash)))
	if entry.ApprovedHash != "" {
		fmt.Printf("Approved:     %s\n", termSafe(entry.ApprovedHash))
	}
	if entry.ApprovedAt != nil || entry.ApprovedBy != "" {
		when := "-"
		if entry.ApprovedAt != nil {
			when = entry.ApprovedAt.UTC().Format(time.RFC3339)
		}
		fmt.Printf("Approved by:  %s at %s\n", termSafe(dash(entry.ApprovedBy)), when)
	}
	fmt.Printf("Scan:         %s\n", termSafe(skillScanLabel(*entry)))
	if entry.Scan.ScannedAt != nil {
		fmt.Printf("Scanned at:   %s\n", entry.Scan.ScannedAt.UTC().Format(time.RFC3339))
	}
	fmt.Printf("Available:    %t\n", entry.Available)
	if len(entry.Scan.Findings) > 0 {
		fmt.Println("Findings:")
		for _, f := range entry.Scan.Findings {
			fmt.Printf("  - %s %s in %s at byte %d: %s\n", termSafe(f.Severity), termSafe(f.Rule), termSafe(f.File), f.Offset, termSafe(f.Evidence))
		}
		if entry.Scan.FindingsTruncated {
			fmt.Println("  (more findings exist than are listed)")
		}
	}
	fmt.Println()
	rows := make([][]string, 0, len(entry.Files))
	for _, f := range entry.Files {
		kind := "text"
		if !f.Text {
			kind = "binary"
		}
		rows = append(rows, []string{termSafe(f.Path), humanBytes(f.Size), kind, termSafe(f.Digest)})
	}
	if err := printTable([]string{"FILE", "SIZE", "TYPE", "DIGEST"}, rows); err != nil {
		return err
	}
	fmt.Printf("\n--- %s (%s copy) ---\n%s", termSafe(file), copyName, termSafeText(body))
	if !strings.HasSuffix(body, "\n") {
		fmt.Println()
	}
	return nil
}

// --- refresh ---

func runSkillsRefresh(server string) error {
	s, err := newRESTSession("skills")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := s.client.RefreshServerSkills(ctx, server)
	if err != nil {
		return err
	}
	if structuredOutput() {
		return printJSONValue(resp)
	}
	counts := map[string]int{}
	for _, e := range resp.Skills {
		counts[e.Status]++
	}
	var parts []string
	for _, st := range []string{"pending", "approved", "changed", "unusable"} {
		if counts[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
		}
	}
	summary := ""
	if len(parts) > 0 {
		summary = " (" + strings.Join(parts, ", ") + ")"
	}
	noun := "skills"
	if len(resp.Skills) == 1 {
		noun = "skill"
	}
	fmt.Printf("%s: refreshed, %d %s%s\n", termSafe(resp.Server), len(resp.Skills), noun, summary)
	if resp.Error != "" {
		fmt.Printf("%s: %s\n", termSafe(resp.Server), termSafe(resp.Error))
	}
	if resp.Truncated {
		fmt.Printf("%s: listing truncated (a skills limit was reached; some skills are missing)\n", termSafe(resp.Server))
	}
	return nil
}
