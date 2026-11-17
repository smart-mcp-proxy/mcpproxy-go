package main

import (
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

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 125 (skills over MCP), PR-1: `mcpproxy skills list|show|refresh`.
// Daemon-only and administrator-only, like the REST endpoints they read:
// nothing here is offered to agents yet.

const skillsCallTimeout = 30 * time.Second

const skillsDisabledLine = "Skills over MCP is off. Set enable_skills: true in the configuration to turn it on."

// GetSkillsCommand returns the `skills` command group.
func GetSkillsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Inspect the skills that upstream servers serve (administrators)",
		Long: `Inspect the agent skills that upstream MCP servers serve through the skills
extension. mcpproxy reads each skill, checks the size and SHA-256 of every file
and keeps a record you can list and read. This release only shows them to
administrators; skills are not offered to agents yet.

Skills over MCP is off by default. Set enable_skills: true in the configuration
to turn it on, and expose_skills: false on a server to exclude that server.

Examples:
  mcpproxy skills list
  mcpproxy skills list --server hf
  mcpproxy skills show hf:hf-cli
  mcpproxy skills show hf:hf-cli --file references/auth.md
  mcpproxy skills refresh hf`,
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsShowCmd(), newSkillsRefreshCmd())
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
	var approved bool
	cmd := &cobra.Command{
		Use:   "show <server>:<skill>",
		Short: "Show one skill and the text of one of its files",
		Long: `Show a skill: name, description, status, content hash and its files, then the
text of one file (SKILL.md unless --file is given). Long files are read in
chunks until the end. A binary file cannot be shown.

Examples:
  mcpproxy skills show hf:hf-cli
  mcpproxy skills show hf:hf-cli --file references/auth.md
  mcpproxy skills show hf:hf-cli --approved`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			return termSafeErr(runSkillsShow(args[0], file, approved))
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "File inside the skill (default SKILL.md)")
	cmd.Flags().BoolVar(&approved, "approved", false, "Read the approved copy instead of the current one")
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
			rows = append(rows, []string{termSafe(l.Server), termSafe(e.Path), termSafe(skillStatusCell(e)), strconv.Itoa(len(e.Files)), humanBytes(e.TotalSize)})
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
	} else if err := printTable([]string{"SERVER", "SKILL", "STATUS", "FILES", "SIZE"}, rows); err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Println(n)
	}
	return nil
}

func skillStatusCell(e contracts.SkillEntry) string {
	if e.Status == "unusable" && e.UnusableReason != "" {
		return "unusable (" + e.UnusableReason + ")"
	}
	return e.Status
}

// termSafe makes text that came from an upstream server safe to print on one
// line of a terminal. Skill names, descriptions, paths and reasons are shown
// before anyone approved them, and an escape sequence printed raw would be
// carried out by the terminal: clear the screen, set the title, write the
// clipboard, reverse the reading order. Control characters, the Unicode
// direction and invisible-format characters, and bytes that are not UTF-8 are
// shown as visible escapes instead. Structured output (-o json|yaml) does not
// go through here: its encoder escapes on its own and stays exact.
func termSafe(s string) string { return termEscape(s, false) }

// escapedError is an error whose message is safe to print; the error it wraps
// is still there for errors.Is and errors.As.
type escapedError struct{ err error }

func (e escapedError) Error() string { return termSafe(e.err.Error()) }
func (e escapedError) Unwrap() error { return e.err }

// termSafeErr escapes the message of an error a skills command returns. Such a
// message can quote an upstream server (the text of its JSON-RPC error), and
// it is printed by the command's error path, not by the code above.
func termSafeErr(err error) error {
	if err == nil {
		return nil
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

// printJSONValue prints v through the -o json|yaml path.
func printJSONValue(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return printData(raw)
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

	var text strings.Builder
	offset := int64(0)
	var digest string
	var size int64
	for first := true; ; first = false {
		chunk, err := s.client.GetServerSkillContent(ctx, server, skill, file, approved, offset)
		if err != nil {
			return err
		}
		// Every chunk names the version it was cut from. A refresh between two
		// requests would otherwise be shown as text no version contains.
		if first {
			digest, size = chunk.Digest, chunk.Size
		} else if chunk.Digest != digest || chunk.Size != size {
			return fmt.Errorf("%s changed while it was being read; run the command again", termSafe(file))
		}
		text.WriteString(chunk.Text)
		if chunk.NextOffset == nil {
			break
		}
		if *chunk.NextOffset <= offset {
			return fmt.Errorf("the daemon did not advance the read offset (%d); stopping", offset)
		}
		offset = *chunk.NextOffset
	}

	if structuredOutput() {
		return printJSONValue(skillShowDoc{SkillEntry: *entry, File: file, Copy: copyName, Text: text.String()})
	}
	fmt.Printf("Skill:        %s\n", termSafe(entry.ID))
	fmt.Printf("Name:         %s\n", termSafe(entry.Name))
	fmt.Printf("Description:  %s\n", termSafe(entry.Description))
	fmt.Printf("Status:       %s\n", termSafe(skillStatusCell(*entry)))
	fmt.Printf("Hash:         %s\n", termSafe(dash(entry.CurrentHash)))
	if entry.ApprovedHash != "" {
		fmt.Printf("Approved:     %s\n", termSafe(entry.ApprovedHash))
	}
	fmt.Printf("Available:    %t\n\n", entry.Available)
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
	fmt.Printf("\n--- %s (%s copy) ---\n%s", termSafe(file), copyName, termSafeText(text.String()))
	if !strings.HasSuffix(text.String(), "\n") {
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
