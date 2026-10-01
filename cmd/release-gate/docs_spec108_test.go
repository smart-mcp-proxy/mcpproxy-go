package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Spec 108-l (L13, T124): the Profiles v3 documentation is published, linked from
// the sidebar, and says what a user needs. A docs page that is not in the
// docusaurus include list or has no sidebar entry is an orphan; a published page
// that links to a repo-only variant 404s on docs.mcpproxy.app. The pages are
// FR-051 deliverables.

// spec108Pages are the published docs ids this spec owns.
var spec108Pages = []string{
	"features/profiles",
	"features/connect-clients",
	"features/agent-tokens",
	"cli/profile-commands",
	"configuration/config-file",
	"api/rest-api",
}

// markdownLink matches [text](target); the target is group 1.
var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

func readDocByID(t *testing.T, id string) string {
	t.Helper()
	path, ok := resolveDocID(id)
	if !ok {
		t.Fatalf("docs/%s.md does not exist", id)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unpublishedLinkProblems lists every relative markdown link of page that points
// at a docs file the site does not publish (the repo-root variants such as
// docs/configuration.md and docs/cli-management-commands.md).
func unpublishedLinkProblems(t *testing.T, id, text string, published map[string]bool) []string {
	t.Helper()
	var problems []string
	pageDir := filepath.Dir(filepath.Join(repoRootFromTest, "docs", filepath.FromSlash(id)))
	for _, m := range markdownLink.FindAllStringSubmatch(text, -1) {
		target := m[1]
		if i := strings.IndexAny(target, "#?"); i >= 0 {
			target = target[:i]
		}
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || !strings.HasSuffix(target, ".md") {
			continue
		}
		abs := filepath.Clean(filepath.Join(pageDir, filepath.FromSlash(target)))
		rel, err := filepath.Rel(filepath.Join(repoRootFromTest, "docs"), abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			problems = append(problems, id+" links outside docs/: "+m[1])
			continue
		}
		if !published[filepath.ToSlash(rel)] {
			problems = append(problems, id+" links to "+m[1]+", which the docs site does not publish")
		}
	}
	return problems
}

func TestSpec108DocsPublished(t *testing.T) {
	sidebarBytes, err := os.ReadFile(filepath.Join(repoRootFromTest, "website", "sidebars.js"))
	if err != nil {
		t.Fatal(err)
	}
	sidebar := string(sidebarBytes)

	published := map[string]bool{}
	for _, path := range publishedDocFiles(t) {
		rel, relErr := filepath.Rel(filepath.Join(repoRootFromTest, "docs"), path)
		if relErr == nil {
			published[filepath.ToSlash(rel)] = true
		}
	}

	for _, id := range spec108Pages {
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

	t.Run("features/profiles documents the v3 policy, the explainer terms and the downgrade precondition", func(t *testing.T) {
		text := readDocByID(t, "features/profiles")
		for _, want := range []string{
			// the six policy fields
			"max_tier", "unannotated", "tools.allow", "tools.deny", "tools.classify", "code_execution", "management_tools", "switchable_to",
			// how a caller reaches a profile and what it sees
			"anonymous_profile", "hidden_by_profile", "binding_bypassable_without_auth",
			// the six audit checks' words
			"profile_tier", "profile_unannotated",
			// the downgrade precondition (SC-010)
			"require_mcp_auth", "before downgrading",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("features/profiles.md does not mention %q", want)
			}
		}
	})

	t.Run("cli/profile-commands covers every profile, client and access command", func(t *testing.T) {
		text := readDocByID(t, "cli/profile-commands")
		for _, want := range []string{
			"profile list", "profile show", "profile create", "profile update",
			"profile rename", "profile delete", "profile classify", "profile try",
			"profile anonymous", "client list", "client show", "client set-profile",
			"client lock", "client unlock", "client add", "client rotate",
			"client forget", "client upgrade-admin-key-holders", "access explain",
			"token create", "--profile", "mcpproxy activity list", "mcpproxy connect",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("cli/profile-commands.md does not mention %q", want)
			}
		}
	})

	t.Run("configuration/config-file lists the v3 fields", func(t *testing.T) {
		text := readDocByID(t, "configuration/config-file")
		for _, want := range []string{"profiles", "anonymous_profile", "max_tier", "unannotated", "switchable_to"} {
			if !strings.Contains(text, want) {
				t.Errorf("configuration/config-file.md does not mention %q", want)
			}
		}
	})

	t.Run("features/connect-clients and agent-tokens document the credential change", func(t *testing.T) {
		connect := readDocByID(t, "features/connect-clients")
		for _, want := range []string{"mcp_cli_", "--keyless", "rotate", "admin"} {
			if !strings.Contains(connect, want) {
				t.Errorf("features/connect-clients.md does not mention %q", want)
			}
		}
		tokens := readDocByID(t, "features/agent-tokens")
		for _, want := range []string{"--profile", "legacy", "client"} {
			if !strings.Contains(tokens, want) {
				t.Errorf("features/agent-tokens.md does not mention %q", want)
			}
		}
	})

	t.Run("mutation: a link to a repo-only variant and an unsidebarred id are caught", func(t *testing.T) {
		bad := unpublishedLinkProblems(t, "features/profiles", "see [config](../configuration.md) and [cli](../cli-management-commands.md#profile)", published)
		if len(bad) != 2 {
			t.Errorf("want 2 problems for the two repo-only links, got %v", bad)
		}
		good := unpublishedLinkProblems(t, "features/profiles", "see [config](../configuration/config-file.md#profiles) and [site](https://docs.mcpproxy.app/cli/profile-commands)", published)
		if len(good) != 0 {
			t.Errorf("published and absolute links must be clean, got %v", good)
		}
		if strings.Contains(sidebar, "'features/no-such-page'") {
			t.Error("the sidebar check is vacuous")
		}
	})
}
