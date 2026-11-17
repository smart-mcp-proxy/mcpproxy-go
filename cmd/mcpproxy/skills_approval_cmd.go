package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 125 (skills over MCP), PR-2: `skills approve|block|unblock` and
// `skills show --diff`. Approval is review-bound: the command lists the skills
// first, prints what it is about to approve and sends exactly the content
// hashes and server incarnation of that listing, so what is approved is what
// was printed.

// skillDiffFileCap bounds the text read for one file of `show --diff`.
const skillDiffFileCap = 1 << 20

// parseSkillTarget splits <server>:<skill>.
func parseSkillTarget(target string) (server, skill string, err error) {
	idx := strings.Index(target, ":")
	if idx <= 0 || idx == len(target)-1 {
		return "", "", newFlagValidationError("invalid target %q: use <server>:<skill>", target)
	}
	return target[:idx], target[idx+1:], nil
}

// parseSkillTargets splits several targets and requires one server for all.
func parseSkillTargets(targets []string) (server string, skills []string, err error) {
	for _, t := range targets {
		srv, skill, perr := parseSkillTarget(t)
		if perr != nil {
			return "", nil, perr
		}
		if server == "" {
			server = srv
		} else if srv != server {
			return "", nil, newFlagValidationError("all skills in one command must belong to one server (got %q and %q)", server, srv)
		}
		skills = append(skills, skill)
	}
	return server, skills, nil
}

// readSkillFile reads one text file of a skill in chunks. Every chunk must
// name the same version of the file. maxBytes > 0 cuts the text at that many
// bytes (on a line boundary when there is one) and reports truncated; 0 reads
// to the end.
//
// wantDigest binds the read to the listing the caller is showing: the text
// printed under a listed content hash must be the text of that listing, not of
// a refresh that ran between the listing and this read. Empty means the file
// is not in the listing (the core answers 404 for it).
func readSkillFile(ctx context.Context, client *cliclient.Client, server, skill, file string, approved bool, maxBytes int64, wantDigest string) (text string, truncated bool, err error) {
	var b strings.Builder
	offset := int64(0)
	var digest string
	var size int64
	for first := true; ; first = false {
		chunk, cerr := client.GetServerSkillContent(ctx, server, skill, file, approved, offset)
		if cerr != nil {
			return "", false, cerr
		}
		// Every chunk names the version it was cut from. A refresh between two
		// requests would otherwise be shown as text no version contains.
		if first {
			digest, size = chunk.Digest, chunk.Size
			if wantDigest != "" && digest != wantDigest {
				return "", false, cliRefusalError{fmt.Errorf("%s changed after the skill was listed; run the command again", file)}
			}
		} else if chunk.Digest != digest || chunk.Size != size {
			return "", false, fmt.Errorf("%s changed while it was being read; run the command again", termSafe(file))
		}
		b.WriteString(chunk.Text)
		if maxBytes > 0 && int64(b.Len()) >= maxBytes {
			if chunk.NextOffset != nil || int64(b.Len()) > maxBytes {
				return cutAtLine(b.String(), maxBytes), true, nil
			}
		}
		if chunk.NextOffset == nil {
			break
		}
		if *chunk.NextOffset <= offset {
			return "", false, fmt.Errorf("the daemon did not advance the read offset (%d); stopping", offset)
		}
		offset = *chunk.NextOffset
	}
	return b.String(), false, nil
}

// cutAtLine cuts s to at most max bytes, at the last line feed when it has
// one, else at a character boundary.
func cutAtLine(s string, max int64) string {
	if int64(len(s)) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1]
	}
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// --- show --diff ---

type skillFileChange struct {
	Path   string `json:"path"`
	Change string `json:"change"` // added, removed, changed
	Text   bool   `json:"text"`
}

// skillDiffDoc is the -o json|yaml document of `skills show --diff`.
type skillDiffDoc struct {
	ID           string            `json:"id"`
	ApprovedHash string            `json:"approved_hash"`
	CurrentHash  string            `json:"current_hash"`
	Approved     bool              `json:"approved"`
	Files        []skillFileChange `json:"files"`
	Diffs        map[string]string `json:"diffs"`
	Truncated    []string          `json:"truncated,omitempty"`
}

// diffSkillFiles compares the approved and current file sets.
// skillFileDigest returns the listed digest of one file, or "".
func skillFileDigest(files []contracts.SkillFileInfo, path string) string {
	for _, f := range files {
		if f.Path == path {
			return f.Digest
		}
	}
	return ""
}

func diffSkillFiles(approved, current []contracts.SkillFileInfo) []skillFileChange {
	byPath := map[string]contracts.SkillFileInfo{}
	for _, f := range approved {
		byPath[f.Path] = f
	}
	var out []skillFileChange
	seen := map[string]bool{}
	for _, f := range current {
		seen[f.Path] = true
		old, had := byPath[f.Path]
		switch {
		case !had:
			out = append(out, skillFileChange{Path: f.Path, Change: "added", Text: f.Text})
		case old.Digest != f.Digest || old.Size != f.Size:
			out = append(out, skillFileChange{Path: f.Path, Change: "changed", Text: f.Text && old.Text})
		}
	}
	for _, f := range approved {
		if !seen[f.Path] {
			out = append(out, skillFileChange{Path: f.Path, Change: "removed", Text: f.Text})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func runSkillsShowDiff(target string) error {
	server, skill, err := parseSkillTarget(target)
	if err != nil {
		return err
	}
	s, err := newRESTSession("skills")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*skillsCallTimeout)
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
	doc := skillDiffDoc{ID: entry.ID, ApprovedHash: entry.ApprovedHash, CurrentHash: entry.CurrentHash,
		Approved: entry.ApprovedHash != "", Files: []skillFileChange{}, Diffs: map[string]string{}}
	if !doc.Approved {
		if structuredOutput() {
			return printJSONValue(doc)
		}
		fmt.Printf("%s has no approved version, so there is nothing to compare. Review it with `mcpproxy skills show %s`.\n", termSafe(entry.ID), termSafe(entry.ID))
		return nil
	}

	doc.Files = diffSkillFiles(entry.ApprovedFiles, entry.Files)
	if doc.Files == nil {
		doc.Files = []skillFileChange{}
	}
	for _, f := range doc.Files {
		if !f.Text {
			continue
		}
		var oldText, newText string
		cut := false
		if f.Change != "added" {
			t, c, rerr := readSkillFile(ctx, s.client, server, skill, f.Path, true, skillDiffFileCap, skillFileDigest(entry.ApprovedFiles, f.Path))
			if rerr != nil {
				return rerr
			}
			oldText, cut = t, cut || c
		}
		if f.Change != "removed" {
			t, c, rerr := readSkillFile(ctx, s.client, server, skill, f.Path, false, skillDiffFileCap, skillFileDigest(entry.Files, f.Path))
			if rerr != nil {
				return rerr
			}
			newText, cut = t, cut || c
		}
		oldName, newName := "approved/"+f.Path, "current/"+f.Path
		if f.Change == "added" {
			oldName = "/dev/null"
		}
		if f.Change == "removed" {
			newName = "/dev/null"
		}
		doc.Diffs[f.Path] = unifiedDiff(oldName, newName, oldText, newText)
		if cut {
			doc.Truncated = append(doc.Truncated, f.Path)
		}
	}
	if structuredOutput() {
		return printJSONValue(doc)
	}

	fmt.Printf("Skill:     %s\n", termSafe(entry.ID))
	fmt.Printf("Approved:  %s\n", termSafe(entry.ApprovedHash))
	fmt.Printf("Current:   %s\n", termSafe(dash(entry.CurrentHash)))
	if entry.ApprovedHash == entry.CurrentHash {
		fmt.Println("\nThe current content is the approved content; nothing changed.")
		return nil
	}
	fmt.Println()
	if len(doc.Files) == 0 {
		fmt.Println("No file changed (the content hash differs, for example in the skill's metadata).")
	}
	for _, f := range doc.Files {
		note := ""
		if !f.Text {
			note = " (binary, not compared)"
		}
		fmt.Printf("%-8s %s%s\n", f.Change, termSafe(f.Path), note)
	}
	for _, f := range doc.Files {
		d, ok := doc.Diffs[f.Path]
		if !ok {
			continue
		}
		fmt.Printf("\n%s", termSafeText(d))
		for _, tp := range doc.Truncated {
			if tp == f.Path {
				fmt.Printf("(%s was cut at 1 MiB; the rest is not compared)\n", termSafe(f.Path))
			}
		}
	}
	return nil
}

// --- approve ---

func newSkillsApproveCmd() *cobra.Command {
	var server, expectHash string
	var all, yes bool
	cmd := &cobra.Command{
		Use:   "approve <server>:<skill> [<server>:<skill>...]",
		Short: "Approve skills for exactly the content you reviewed",
		Long: `Approve skills so they can be offered to agents. The command lists the server's
skills first, prints one line per skill it is about to approve (status, content
hash, scan result and findings), and sends the approval bound to exactly those
content hashes and to that server. If a skill changed after the listing, or the
server was replaced, nothing is approved and the command exits with status 1.

All skills of one command must belong to one server. With --server <name> --all
every pending or changed skill of that server that can be approved is taken.
--expect-hash binds a single skill to a hash you read earlier in
'skills show'; if the current hash differs, the command refuses without asking
the daemon to approve anything.

Without --yes the command asks for confirmation on a terminal and refuses to
run without one.

Examples:
  mcpproxy skills approve hf:hf-cli
  mcpproxy skills approve hf:hf-cli hf:jobs --yes
  mcpproxy skills approve hf:hf-cli --expect-hash sk1-ab12...
  mcpproxy skills approve --server hf --all`,
		Args: argsOrHelpJSON(cobra.ArbitraryArgs),
		RunE: func(_ *cobra.Command, args []string) error {
			return termSafeErr(runSkillsApprove(args, server, all, expectHash, yes))
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Server to approve for (with --all)")
	cmd.Flags().BoolVar(&all, "all", false, "Approve every pending or changed skill of --server that can be approved")
	cmd.Flags().StringVar(&expectHash, "expect-hash", "", "Refuse unless the skill's current content hash is this (one skill only)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")
	return cmd
}

// skillReviewLine is what the approve command prints for one skill before it
// asks.
func skillReviewLine(e contracts.SkillEntry) string {
	return fmt.Sprintf("  %s  %s  %s  scan: %s", e.ID, skillStatusCell(e), dash(e.CurrentHash), skillScanLabel(e))
}

func runSkillsApprove(args []string, serverFlag string, all bool, expectHash string, yes bool) error {
	var server string
	var names []string
	switch {
	case all:
		if len(args) > 0 {
			return newFlagValidationError("--all takes no <server>:<skill> arguments")
		}
		if serverFlag == "" {
			return newFlagValidationError("--all needs --server <name>")
		}
		if expectHash != "" {
			return newFlagValidationError("--expect-hash binds one skill; it cannot be combined with --all")
		}
		server = serverFlag
	default:
		if len(args) == 0 {
			return newFlagValidationError("name the skills to approve (<server>:<skill>), or use --server <name> --all")
		}
		var err error
		server, names, err = parseSkillTargets(args)
		if err != nil {
			return err
		}
		if serverFlag != "" && serverFlag != server {
			return newFlagValidationError("--server %q does not match the skills' server %q", serverFlag, server)
		}
		if expectHash != "" && len(uniqueStringsCLI(names)) != 1 {
			return newFlagValidationError("--expect-hash binds one skill; name exactly one")
		}
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

	var entries []*contracts.SkillEntry
	if all {
		for i := range list.Skills {
			e := &list.Skills[i]
			if (e.Status == "pending" || e.Status == "changed") && e.Listed && e.CurrentHash != "" {
				entries = append(entries, e)
			}
		}
	} else {
		for _, n := range uniqueStringsCLI(names) {
			e, err := skillEntryByPath(list.Skills, n)
			if err != nil {
				return fmt.Errorf("%w: %s:%s", err, server, n)
			}
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		fmt.Printf("Nothing to approve on %s.\n", termSafe(server))
		return nil
	}
	if expectHash != "" && entries[0].CurrentHash != expectHash {
		return cliRefusalError{fmt.Errorf("%s has content hash %s, not the expected %s; it changed since you read it. Review it again with `mcpproxy skills show %s`; nothing was approved",
			entries[0].ID, dash(entries[0].CurrentHash), expectHash, entries[0].ID)}
	}

	// What is about to be approved goes to stdout in table mode and to stderr
	// otherwise, so -o json|yaml output stays one document.
	review := os.Stdout
	if structuredOutput() {
		review = os.Stderr
	}
	var shown strings.Builder
	fmt.Fprintf(&shown, "About to approve %d skill(s) on %s (server incarnation %d):\n", len(entries), termSafe(server), list.Incarnation)
	req := contracts.SkillsApproveRequest{ExpectedHashes: map[string]string{}, ExpectedIncarnation: list.Incarnation}
	for _, e := range entries {
		fmt.Fprintln(&shown, termSafe(skillReviewLine(*e)))
		req.Skills = append(req.Skills, e.Path)
		req.ExpectedHashes[e.Path] = e.CurrentHash
	}
	// What is approved is what was shown: if the review could not be written
	// (a closed or full output), nothing is sent.
	if _, werr := io.WriteString(review, shown.String()); werr != nil {
		return cliRefusalError{fmt.Errorf("the review could not be written (%v); nothing was approved", werr)}
	}

	ok, err := confirmYes(fmt.Sprintf("Approve %d skill(s)?", len(entries)), yes)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(review, "Nothing was approved.")
		return nil
	}

	resp, err := s.client.ApproveServerSkills(ctx, server, req)
	if err != nil {
		var stale *cliclient.SkillsStaleReviewError
		if errors.As(err, &stale) {
			// A refusal with skill names in it: the exit code must not depend
			// on what a name happens to say.
			return cliRefusalError{fmt.Errorf("the skills changed after they were listed; nothing was approved. Changed: %s. Not reviewed: %s. Review them again and re-run the command",
				joinOrDash(stale.Changed), joinOrDash(stale.Unreviewed))}
		}
		return err
	}
	if structuredOutput() {
		return printJSONValue(resp)
	}
	fmt.Printf("Approved %d skill(s) on %s.\n", resp.Approved, termSafe(server))
	return nil
}

func joinOrDash(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ", ")
}

func uniqueStringsCLI(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// --- block / unblock ---

func newSkillsBlockCmd(block bool) *cobra.Command {
	use, short, long, verb := "unblock", "Clear the blocked mark of skills", `Clear the blocked mark of skills. The approval state is not changed: a skill that
is not approved stays unavailable.`, "unblocked"
	if block {
		use, short, long, verb = "block", "Block skills so they are never offered to agents", `Block skills. A blocked skill is never offered to an agent, whatever its
approval state; a refresh, a content change or an automatic approval does not
clear the mark. Only 'skills unblock' does.`, "blocked"
	}
	return &cobra.Command{
		Use:   use + " <server>:<skill> [<server>:<skill>...]",
		Short: short,
		Long: long + `

All skills of one command must belong to one server.

Examples:
  mcpproxy skills ` + use + ` hf:hf-cli`,
		Args: argsOrHelpJSON(cobra.MinimumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			return termSafeErr(runSkillsBlock(args, block, verb))
		},
	}
}

func runSkillsBlock(args []string, block bool, verb string) error {
	server, skills, err := parseSkillTargets(args)
	if err != nil {
		return err
	}
	s, err := newRESTSession("skills")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillsCallTimeout)
	defer cancel()
	resp, err := s.client.SetServerSkillsBlocked(ctx, server, block, contracts.SkillsBlockRequest{Skills: uniqueStringsCLI(skills)})
	if err != nil {
		return err
	}
	if structuredOutput() {
		return printJSONValue(resp)
	}
	fmt.Printf("%d skill(s) %s on %s.\n", resp.Changed, verb, termSafe(server))
	return nil
}
