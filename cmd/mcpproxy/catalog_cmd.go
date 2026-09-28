package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// printCatalogDeprecationNotice prints the FR-066 deprecation note to
// stderr: 'registry search'/'registry add' remain as aliases (scripts must
// not break), but point at their 'catalog' equivalent.
func printCatalogDeprecationNotice(oldCmd, newCmd string) {
	fmt.Fprintf(os.Stderr, "Note: 'mcpproxy %s' is deprecated; use 'mcpproxy %s' instead.\n", oldCmd, newCmd)
}

// Catalog command flags (Spec 109 FR-060/066).
var (
	catalogSearchSource string
	catalogSearchTag    string
	catalogSearchLimit  int
	catalogAddName      string
	catalogAddEnv       []string
	catalogAddEnabled   bool
)

// GetCatalogCommand builds the `catalog` command group (Spec 109 FR-066): a
// source-agnostic search/browse/add flow layered over the same registries
// `registry` manages as sources.
//
// 'catalog' supersedes 'registry search'/'registry add' for DISCOVERY; the
// commands that manage catalog sources themselves — 'registry
// list'/'add-source'/'edit'/'remove' — are unchanged and keep their name
// (renaming a command group breaks scripts, research D17).
func GetCatalogCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Browse and add MCP servers from the catalog",
		Long: `Search across every enabled catalog source (registry) in one call, or browse
the curated official/popular sections with no query.

  mcpproxy catalog search github                                  # search every source
  mcpproxy catalog search                                          # browse official + popular
  mcpproxy catalog show official/io.github.github/github-mcp-server
  mcpproxy catalog add official/io.github.github/github-mcp-server
  mcpproxy upstream approve <name>                                 # approve once trusted

'catalog search'/'catalog add' supersede 'registry search'/'registry add'.
'registry list'/'add-source'/'edit'/'remove' still manage catalog SOURCES.`,
	}
	cmd.PersistentFlags().StringVarP(&registryConfigPath, "config", "c", "", "Path to MCP configuration file")
	cmd.AddCommand(newCatalogSearchCmd(), newCatalogShowCmd(), newCatalogAddCmd())
	return cmd
}

func newCatalogSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the catalog across every enabled source",
		Long: `Search every enabled catalog source at once (FR-060), ranked official-first,
then verified, then popularity, then text relevance. Omit the query to browse
the curated "official" and "popular" sections instead.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}
			formatter, err := GetOutputFormatter()
			if err != nil {
				return err
			}

			ctx, cancel := registryContext()
			defer cancel()
			resp, err := catalogSearch(ctx, cfg, query, catalogSearchSource, catalogSearchTag, catalogSearchLimit)
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeOperationFailed, err.Error()), clioutput.ErrCodeOperationFailed)
			}
			return renderCatalogSearch(formatter, resp)
		},
	}
	cmd.Flags().StringVar(&catalogSearchSource, "source", "", "Narrow to one catalog source id (use 'registry list' to see ids)")
	cmd.Flags().StringVarP(&catalogSearchTag, "tag", "t", "", "Filter by tag")
	cmd.Flags().IntVarP(&catalogSearchLimit, "limit", "l", 20, "Maximum number of results (default 20, max 50)")
	return cmd
}

func newCatalogShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <source>/<id>",
		Short: "Show one catalog entry's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			source, id, err := parseCatalogRef(args[0])
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, err.Error()).
					WithGuidance("Pass '<source>/<id>', e.g. official/io.github.github/github-mcp-server"), clioutput.ErrCodeInvalidInput)
			}

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}
			formatter, err := GetOutputFormatter()
			if err != nil {
				return err
			}

			ctx, cancel := registryContext()
			defer cancel()
			registries.SetRegistriesFromConfig(cfg)
			reg := registries.FindRegistry(source)
			if reg == nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeServerNotFound, fmt.Sprintf("catalog source %q not found", source)).
					WithGuidance("Use 'mcpproxy registry list' to see source ids"), clioutput.ErrCodeServerNotFound)
			}
			entry, err := registries.FindServerByID(ctx, source, id, nil)
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeServerNotFound, err.Error()).
					WithGuidance("Use 'mcpproxy catalog search' to find the id"), clioutput.ErrCodeServerNotFound)
			}

			hit := registries.BuildCatalogHit(reg, *entry)
			result := registries.ToCatalogResult(hit, catalogAddedFromConfig(cfg)(hit))
			return renderCatalogShow(formatter, result)
		},
	}
	return cmd
}

func newCatalogAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <source>/<id>",
		Short: "Add a catalog entry as a (quarantined) upstream server",
		Long: `Add a server found via 'catalog search'/'catalog show' as an upstream server.
The server is added quarantined by default; approve it once you trust it:
  mcpproxy upstream approve <name>

This is the same keystone add operation as 'registry add <source> <id>' (Spec
070) — it just takes the combined "<source>/<id>" ref catalog results print.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			source, id, err := parseCatalogRef(args[0])
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeInvalidInput, err.Error()).
					WithGuidance("Pass '<source>/<id>', e.g. official/io.github.github/github-mcp-server"), clioutput.ErrCodeInvalidInput)
			}

			env, err := parseRegistryEnv(catalogAddEnv)
			if err != nil {
				return err
			}

			cfg, err := loadRegistryConfig()
			if err != nil {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConfigNotFound, err.Error()).
					WithRecoveryCommand("mcpproxy doctor"), clioutput.ErrCodeConfigNotFound)
			}

			// add MUST go through the daemon (keystone op is server-side, same as
			// 'registry add').
			client, ok := newDaemonClient(cfg, nil)
			if !ok {
				return outputError(clioutput.NewStructuredError(clioutput.ErrCodeConnectionFailed,
					"adding from the catalog requires a running mcpproxy daemon").
					WithGuidance("Start the daemon, then retry").
					WithRecoveryCommand("mcpproxy serve"), clioutput.ErrCodeConnectionFailed)
			}

			ctx, cancel := registryContext()
			defer cancel()
			enabled := catalogAddEnabled
			result, err := client.AddFromRegistry(ctx, source, id, catalogAddName, env, &enabled)
			if err != nil {
				return registryAddErrorOutput(err)
			}

			outputFormat := ResolveOutputFormat()
			if outputFormat == "json" || outputFormat == "yaml" {
				formatter, _ := GetOutputFormatter()
				out, _ := formatter.Format(result)
				fmt.Println(out)
				return nil
			}

			// FR-063: identical wording to 'registry add' and the Web/macOS "Add
			// to MCPProxy" action.
			fmt.Println(registryAddMessage(result.Name, result.Quarantined))
			return nil
		},
	}
	cmd.Flags().StringVar(&catalogAddName, "name", "", "Override the server name")
	cmd.Flags().StringArrayVar(&catalogAddEnv, "env", nil, "Set an environment variable (KEY=VALUE); repeatable")
	cmd.Flags().BoolVar(&catalogAddEnabled, "enabled", true, "Whether the added server is enabled")
	return cmd
}

// parseCatalogRef splits a "<source>/<id>" ref on the FIRST '/' only, since
// an official-protocol id is itself reverse-DNS-shaped and contains further
// slashes (e.g. "io.github.github/github-mcp-server").
func parseCatalogRef(ref string) (source, id string, err error) {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid catalog ref %q: expected '<source>/<id>'", ref)
	}
	return parts[0], parts[1], nil
}

// catalogSearch is daemon-first with an in-process fallback (mirrors
// 'registry search'), so catalog discovery works whether or not a daemon is
// running.
func catalogSearch(ctx context.Context, cfg *config.Config, q, source, tag string, limit int) (*cliclient.CatalogSearchResponse, error) {
	if client, ok := newDaemonClient(cfg, nil); ok {
		if resp, derr := client.CatalogSearch(ctx, q, source, tag, limit); derr == nil {
			return resp, nil
		}
		// Fall through to in-process on daemon error.
	}
	// Load the effective registry list (built-in defaults + the user's
	// configured sources) once, here, so catalogSearchInProcess itself stays
	// a pure function over whatever registries.ListRegistries() currently
	// returns — which is also what makes it independently testable against a
	// fixture list (registries.SetRegistriesForTest).
	registries.SetRegistriesFromConfig(cfg)
	return catalogSearchInProcess(ctx, cfg, q, source, tag, limit)
}

// catalogSearchInProcess mirrors handleCatalogSearch (internal/httpapi) but
// computes "added" directly from the loaded config, since the CLI's
// in-process fallback has no scoped caller to narrow against. It reads
// whatever registries.ListRegistries() currently returns rather than loading
// it itself — see catalogSearch, its only production caller.
func catalogSearchInProcess(ctx context.Context, cfg *config.Config, q, source, tag string, limit int) (*cliclient.CatalogSearchResponse, error) {
	// Source is applied inside SearchAll, BEFORE ranking/truncation to
	// limit — filtering after truncation could silently drop a narrower
	// source's real matches that simply lost out to an official/verified
	// source for one of the truncated top-`limit` slots (same fix as
	// httpapi.handleCatalogSearch).
	hits, sections, unavailable := registries.SearchAll(ctx, q, tag, limit, registries.SearchOptions{Source: source})

	added := catalogAddedFromConfig(cfg)
	resp := &cliclient.CatalogSearchResponse{Query: q, Unavailable: unavailable}
	if resp.Unavailable == nil {
		resp.Unavailable = []registries.SourceError{}
	}
	for _, h := range hits {
		resp.Results = append(resp.Results, registries.ToCatalogResult(h, added(h)))
	}
	if sections != nil {
		resp.Sections = &cliclient.CatalogSections{}
		for _, h := range sections.Official {
			resp.Sections.Official = append(resp.Sections.Official, registries.ToCatalogResult(h, added(h)))
		}
		for _, h := range sections.Popular {
			resp.Sections.Popular = append(resp.Sections.Popular, registries.ToCatalogResult(h, added(h)))
		}
	}
	return resp, nil
}

// catalogAddedFromConfig returns a predicate reporting whether a catalog hit
// matches an already-configured server (contracts/rest-api.md#catalog "added"):
// a registry-sourced server also needs a matching source, a manual add
// matches on install target alone.
func catalogAddedFromConfig(cfg *config.Config) func(registries.CatalogHit) bool {
	byRegistryAndTarget := make(map[string]bool)
	byTargetOnly := make(map[string]bool)
	if cfg != nil {
		for _, s := range cfg.Servers {
			if s == nil {
				continue
			}
			target := catalogInstallTargetForConfigServer(s)
			if s.SourceRegistryID == "" {
				// Manual add: matches any source by install target alone.
				byTargetOnly[target] = true
			} else {
				// Registry-sourced: must also match its own source, so it
				// never falsely matches a different source's identical
				// install target (contracts/rest-api.md#catalog "added").
				byRegistryAndTarget[s.SourceRegistryID+"\x00"+target] = true
			}
		}
	}
	return func(h registries.CatalogHit) bool {
		target := registries.CatalogInstallTarget(registries.ToCatalogResult(h, false).Install)
		if byRegistryAndTarget[h.Source+"\x00"+target] {
			return true
		}
		return byTargetOnly[target]
	}
}

func catalogInstallTargetForConfigServer(s *config.ServerConfig) string {
	if s.URL != "" {
		return "url:" + s.URL
	}
	return "cmd:" + s.Command + " " + strings.Join(s.Args, " ")
}

// renderCatalogSearch prints a search response as a table (or the raw
// formatter output for json/yaml).
func renderCatalogSearch(formatter clioutput.OutputFormatter, resp *cliclient.CatalogSearchResponse) error {
	if _, isTable := formatter.(*clioutput.TableFormatter); isTable {
		if resp.Sections != nil {
			fmt.Println("Official:")
			printCatalogTable(formatter, resp.Sections.Official)
			fmt.Println("\nPopular:")
			printCatalogTable(formatter, resp.Sections.Popular)
		} else {
			printCatalogTable(formatter, resp.Results)
		}
		for _, u := range resp.Unavailable {
			fmt.Printf("⚠ %s unavailable: %s\n", u.Source, u.Reason)
		}
		return nil
	}
	out, err := formatter.Format(resp)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func printCatalogTable(formatter clioutput.OutputFormatter, results []registries.CatalogResult) {
	headers := []string{"SOURCE", "ID", "TITLE", "TRANSPORT", "ADDED"}
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		added := ""
		if r.Added {
			added = "✓"
		}
		rows = append(rows, []string{r.Source, r.ID, truncateStr(r.Title, 40), r.Transport, added})
	}
	out, err := formatter.FormatTable(headers, rows)
	if err == nil {
		fmt.Print(out)
	}
	fmt.Printf("Found %d results. Add one with: mcpproxy catalog add <source>/<id>\n", len(results))
}

func renderCatalogShow(formatter clioutput.OutputFormatter, result registries.CatalogResult) error {
	if _, isTable := formatter.(*clioutput.TableFormatter); isTable {
		fmt.Printf("%s\n", result.Title)
		fmt.Printf("  ref:         %s/%s\n", result.Source, result.ID)
		if result.Publisher != "" {
			fmt.Printf("  publisher:   %s\n", result.Publisher)
		}
		fmt.Printf("  official:    %v\n", result.Official)
		fmt.Printf("  verified:    %v\n", result.Verified)
		fmt.Printf("  transport:   %s\n", result.Transport)
		if result.Install.URL != "" {
			fmt.Printf("  install url: %s\n", result.Install.URL)
		} else {
			fmt.Printf("  install cmd: %s %s\n", result.Install.Command, strings.Join(result.Install.Args, " "))
		}
		if result.Description != "" {
			fmt.Printf("  description: %s\n", result.Description)
		}
		for _, in := range result.RequiredInputs {
			secret := ""
			if in.SecretLike {
				secret = " (secret)"
			}
			fmt.Printf("  requires:    %s%s\n", in.Name, secret)
		}
		fmt.Printf("  added:       %v\n", result.Added)
		return nil
	}
	out, err := formatter.Format(result)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}
