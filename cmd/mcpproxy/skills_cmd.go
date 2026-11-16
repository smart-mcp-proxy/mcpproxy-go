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
		RunE: func(_ *cobra.Command, _ []string) error { return runSkillsList(server) },
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
		RunE: func(_ *cobra.Command, args []string) error { return runSkillsShow(args[0], file, approved) },
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
		RunE: func(_ *cobra.Command, args []string) error { return runSkillsRefresh(args[0]) },
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
			rows = append(rows, []string{l.Server, e.Path, skillStatusCell(e), strconv.Itoa(len(e.Files)), humanBytes(e.TotalSize)})
		}
		if l.Error != "" {
			notes = append(notes, l.Server+": "+l.Error)
		}
		if l.Truncated {
			notes = append(notes, l.Server+": listing truncated (a skills limit was reached; some skills are missing)")
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
	for {
		chunk, err := s.client.GetServerSkillContent(ctx, server, skill, file, approved, offset)
		if err != nil {
			return err
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
	fmt.Printf("Skill:        %s\n", entry.ID)
	fmt.Printf("Name:         %s\n", entry.Name)
	fmt.Printf("Description:  %s\n", entry.Description)
	fmt.Printf("Status:       %s\n", skillStatusCell(*entry))
	fmt.Printf("Hash:         %s\n", dash(entry.CurrentHash))
	if entry.ApprovedHash != "" {
		fmt.Printf("Approved:     %s\n", entry.ApprovedHash)
	}
	fmt.Printf("Available:    %t\n\n", entry.Available)
	rows := make([][]string, 0, len(entry.Files))
	for _, f := range entry.Files {
		kind := "text"
		if !f.Text {
			kind = "binary"
		}
		rows = append(rows, []string{f.Path, humanBytes(f.Size), kind, f.Digest})
	}
	if err := printTable([]string{"FILE", "SIZE", "TYPE", "DIGEST"}, rows); err != nil {
		return err
	}
	fmt.Printf("\n--- %s (%s copy) ---\n%s", file, copyName, text.String())
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
	fmt.Printf("%s: refreshed, %d %s%s\n", resp.Server, len(resp.Skills), noun, summary)
	if resp.Error != "" {
		fmt.Printf("%s: %s\n", resp.Server, resp.Error)
	}
	if resp.Truncated {
		fmt.Printf("%s: listing truncated (a skills limit was reached; some skills are missing)\n", resp.Server)
	}
	return nil
}
