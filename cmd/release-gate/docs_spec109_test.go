package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Spec 109-m (T149a, M14): the Spec 109 documentation is published, linked from
// the sidebar, accurate about the commands it names, and free of links to
// repo-only variants. It reuses the Spec 108 helpers of docs_spec108_test.go
// (readDocByID, unpublishedLinkProblems) and resolveDocID / publishedDocFiles of
// docs_claims_test.go.

// spec109Pages are the published docs ids this spec owns or changes.
var spec109Pages = []string{
	"features/needs-attention",
	"features/registry-add",
	"cli/attention-command",
	"cli/catalog-commands",
	"cli/review-commands",
	"cli/management-commands",
	"cli/status-command",
	"cli/command-reference",
	"web-ui/dashboard",
	"api/rest-api",
}

// spec109CLIPageForGroup maps a command group of contracts/cli.md "New groups
// and commands" to the published page that documents it. `client` commands are
// documented with the profile commands (Spec 108-l owns that page).
var spec109CLIPageForGroup = map[string]string{
	"attention": "cli/attention-command",
	"review":    "cli/review-commands",
	"catalog":   "cli/catalog-commands",
	"client":    "cli/profile-commands",
}

var spec109CLICommandRe = regexp.MustCompile("^\\| `(mcpproxy [a-z-]+(?: [a-z-]+)?)")

func TestSpec109DocsPublished(t *testing.T) {
	sidebarBytes, err := os.ReadFile(filepath.Join(repoRootFromTest, "website", "sidebars.js"))
	if err != nil {
		t.Fatal(err)
	}
	sidebar := string(sidebarBytes)

	published := map[string]bool{}
	for _, path := range publishedDocFiles(t) {
		if rel, relErr := filepath.Rel(filepath.Join(repoRootFromTest, "docs"), path); relErr == nil {
			published[filepath.ToSlash(rel)] = true
		}
	}

	for _, id := range spec109Pages {
		path, ok := resolveDocID(id)
		if !ok {
			t.Errorf("docs/%s.md does not exist", id)
			continue
		}
		rel, _ := filepath.Rel(filepath.Join(repoRootFromTest, "docs"), path)
		if !published[filepath.ToSlash(rel)] {
			t.Errorf("docs/%s is not in the docusaurus include list (website/docusaurus.config.js): it would not be published", rel)
		}
		if !strings.Contains(sidebar, "'"+id+"'") {
			t.Errorf("website/sidebars.js has no entry for %q: the page is an orphan", id)
		}
		for _, problem := range unpublishedLinkProblems(t, id, readDocByID(t, id), published) {
			t.Error(problem)
		}
	}

	t.Run("each CLI page names every command of contracts/cli.md New groups it covers", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(repoRootFromTest, "specs", "109-ux-navigation-consistency", "contracts", "cli.md"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		start := strings.Index(text, "## New groups and commands")
		end := strings.Index(text, "## Changed commands")
		if start < 0 || end <= start {
			t.Fatal("contracts/cli.md has no New groups and commands section")
		}
		checked := 0
		for _, line := range strings.Split(text[start:end], "\n") {
			m := spec109CLICommandRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			command := m[1] // e.g. "mcpproxy review show"
			group := strings.Fields(command)[1]
			page, ok := spec109CLIPageForGroup[group]
			if !ok {
				t.Errorf("contracts/cli.md lists %q but no docs page is mapped to the %q group", command, group)
				continue
			}
			// Pages may print the command with or without the "mcpproxy " prefix.
			if !strings.Contains(readDocByID(t, page), strings.TrimPrefix(command, "mcpproxy ")) {
				t.Errorf("%s.md does not name %q", page, command)
			}
			checked++
		}
		if checked < 9 {
			t.Errorf("only %d commands were checked: the contracts/cli.md reader is broken", checked)
		}
	})

	t.Run("the REST reference has Attention, Review and Catalog sections", func(t *testing.T) {
		rest := readDocByID(t, "api/rest-api")
		for _, heading := range []string{"### Attention", "### Review", "### Catalog"} {
			if !strings.Contains(rest, "\n"+heading+"\n") {
				t.Errorf("docs/api/rest-api.md has no %q section", heading)
			}
		}
		for _, route := range []string{"GET /api/v1/attention", "GET /api/v1/review", "GET /api/v1/servers/{id}/review", "GET /api/v1/catalog/search"} {
			if !strings.Contains(rest, route) {
				t.Errorf("docs/api/rest-api.md does not document %s", route)
			}
		}
	})

	t.Run("the Web UI page is titled Home and the registry page says Add to MCPProxy", func(t *testing.T) {
		home := readDocByID(t, "web-ui/dashboard")
		if !strings.Contains(home, "\ntitle: Home") || strings.Contains(home, "title: Web Dashboard") {
			t.Error("docs/web-ui/dashboard.md must be titled Home")
		}
		for _, want := range []string{"Needs attention", "Review queue", "⌘K", "+ Add", "/repositories", "/sessions"} {
			if !strings.Contains(home, want) {
				t.Errorf("docs/web-ui/dashboard.md does not mention %q", want)
			}
		}
		if !strings.Contains(readDocByID(t, "features/registry-add"), "Add to MCPProxy") {
			t.Error("docs/features/registry-add.md must use the name Add to MCPProxy")
		}
	})

	t.Run("management and status pages describe the declared CLI text changes", func(t *testing.T) {
		mgmt := readDocByID(t, "cli/management-commands")
		for _, want := range []string{"--status", "--secret-env", "--secret-header", "review approve"} {
			if !strings.Contains(mgmt, want) {
				t.Errorf("cli/management-commands.md does not mention %q", want)
			}
		}
		status := readDocByID(t, "cli/status-command")
		for _, want := range []string{"Needs attention:", "Endpoint & mode"} {
			if !strings.Contains(status, want) {
				t.Errorf("cli/status-command.md does not mention %q", want)
			}
		}
	})

	t.Run("--secret-env and --secret-header examples put the flag before the -- separator", func(t *testing.T) {
		// Cobra treats everything after `--` as positional stdio args, so a flag
		// written after it is never parsed (the value would land in the config
		// as a plain child-process argument instead of the keyring).
		for _, id := range []string{"cli/management-commands", "cli/catalog-commands"} {
			for i, line := range strings.Split(readDocByID(t, id), "\n") {
				if !strings.Contains(line, "mcpproxy upstream add") {
					continue
				}
				dash := strings.Index(line, " -- ")
				if dash < 0 {
					continue
				}
				for _, flag := range []string{"--secret-env", "--secret-header"} {
					if strings.Contains(line[dash:], flag) {
						t.Errorf("%s.md:%d puts %s after the `--` separator, where Cobra does not parse it: %s", id, i+1, flag, line)
					}
				}
			}
		}
	})

	t.Run("mutation: a missing command, a root-variant link and an unsidebarred id are caught", func(t *testing.T) {
		if strings.Contains(readDocByID(t, "cli/attention-command"), "mcpproxy no-such-command") {
			t.Error("the command check is vacuous")
		}
		bad := unpublishedLinkProblems(t, "cli/attention-command", "see [all](../cli-management-commands.md)", published)
		if len(bad) != 1 {
			t.Errorf("want 1 problem for a link to the unpublished root variant, got %v", bad)
		}
		if strings.Contains(sidebar, "'cli/no-such-page'") {
			t.Error("the sidebar check is vacuous")
		}
	})
}
