package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
)

// Spec 109-m (FR-090, FR-091, T148b, M3/M11): the CLI half of the parity checks.
//
//   - TestParity109CLICellsResolve: every CLI cell of
//     specs/109-ux-navigation-consistency/parity-matrix.json names commands
//     (`cmd:<path>`) and flags (`fields`, matched as the kebab-case flag). Each
//     command must be in the real `--help-json` output and each field must be a
//     flag of at least one command of the cell.
//   - TestParity109FlagUsageNamesTheValues: the `--approval`, `--tier`, `--view`
//     and `upstream list --status` usage strings name exactly the values (and
//     for --approval the labels) of internal/contracts/testdata/terminology.json.
//
// The production --help-json hook exits the process after writing JSON, so the
// real hook runs in a helper process (the pattern of parity_108_cli_test.go).

type p109CLICell struct {
	Status string   `json:"status"`
	Ids    []string `json:"ids"`
	Fields []string `json:"fields"`
}

type p109CLIMatrix struct {
	Rows []struct {
		Row   string                 `json:"row"`
		Cells map[string]p109CLICell `json:"cells"`
	} `json:"rows"`
}

type p109HelpJSON struct {
	Name  string `json:"name"`
	Flags []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"flags"`
}

// p109HelpRoot registers the package-level command constructors main() adds, so
// `--help-json` sees the real flags.
func p109HelpRoot() *cobra.Command {
	root := &cobra.Command{Use: "mcpproxy"}
	root.AddCommand(
		GetAttentionCommand(), GetStatusCommand(), GetDoctorCommand(), GetUpstreamCommand(),
		GetReviewCommand(), GetSecurityCommand(), GetToolsCommand(), GetClientCommand(),
		GetConnectCommand(), GetTokenCommand(), GetCatalogCommand(), GetRegistryCommand(),
		GetActivityCommand(), GetTelemetryCommand(),
	)
	clioutput.SetupHelpJSON(root)
	return root
}

// p109HelpFor runs `mcpproxy <path> --help-json` in a helper process. Commands
// with required arguments reject --help-json before the hook runs, so the
// helper retries with up to two placeholder arguments.
func p109HelpFor(t *testing.T, path string) (p109HelpJSON, error) {
	t.Helper()
	var lastErr error
	for extra := 0; extra <= 2; extra++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestParity109CLICellsResolve$")
		// Drop an inherited copy of our own variables: a duplicate key could
		// make the child read the stale value.
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "MCPPROXY_109_PARITY_") {
				env = append(env, kv)
			}
		}
		cmd.Env = append(env, "MCPPROXY_109_PARITY_TARGET="+path, "MCPPROXY_109_PARITY_EXTRA="+strconv.Itoa(extra))
		out, err := cmd.Output()
		if err != nil {
			lastErr = err
			continue
		}
		var help p109HelpJSON
		if err := json.Unmarshal(out, &help); err != nil {
			lastErr = fmt.Errorf("not JSON: %w: %s", err, out)
			continue
		}
		// A group command swallows an unknown subcommand as an argument and prints
		// its own help, so the printed name must be the one asked for.
		if want := path[strings.LastIndex(path, " ")+1:]; help.Name != want {
			lastErr = fmt.Errorf("`mcpproxy %s` resolved to %q", path, help.Name)
			continue
		}
		return help, nil
	}
	return p109HelpJSON{}, lastErr
}

func p109FieldProblems(helps []p109HelpJSON, fields []string) []string {
	var missing []string
	for _, field := range fields {
		want := strings.ReplaceAll(field, "_", "-")
		found := false
		for _, help := range helps {
			for _, flag := range help.Flags {
				found = found || flag.Name == want
			}
		}
		if !found {
			missing = append(missing, field)
		}
	}
	return missing
}

func TestParity109CLICellsResolve(t *testing.T) {
	if target := os.Getenv("MCPPROXY_109_PARITY_TARGET"); target != "" {
		extra, _ := strconv.Atoi(os.Getenv("MCPPROXY_109_PARITY_EXTRA"))
		args := strings.Fields(target)
		for i := 0; i < extra; i++ {
			args = append(args, "placeholder")
		}
		root := p109HelpRoot()
		root.SetArgs(append(args, "--help-json"))
		root.SilenceErrors, root.SilenceUsage = true, true
		require.NoError(t, root.Execute())
		return
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "specs", "109-ux-navigation-consistency", "parity-matrix.json"))
	require.NoError(t, err, "parity-matrix.json missing (FR-091)")
	var matrix p109CLIMatrix
	require.NoError(t, json.Unmarshal(raw, &matrix))

	cache := map[string]p109HelpJSON{}
	checked := 0
	for _, row := range matrix.Rows {
		cell := row.Cells["cli"]
		if cell.Status != "must" {
			require.Empty(t, cell.Ids, "row %s: only a ticked CLI cell names commands", row.Row)
			continue
		}
		var helps []p109HelpJSON
		for _, id := range cell.Ids {
			kind, path, ok := strings.Cut(id, ":")
			require.True(t, ok && kind == "cmd", "row %s: CLI ids are cmd:<path>, got %q", row.Row, id)
			help, seen := cache[path]
			if !seen {
				help, err = p109HelpFor(t, path)
				require.NoError(t, err, "row %s: `mcpproxy %s --help-json` is not a command", row.Row, path)
				cache[path] = help
			}
			helps = append(helps, help)
			checked++
		}
		require.Empty(t, p109FieldProblems(helps, cell.Fields),
			"row %s: a field has no matching --flag on the cell's commands", row.Row)
	}
	require.Greater(t, checked, 20, "the CLI walk saw too few commands: the matrix reader is broken")

	t.Run("mutation self-check: an unknown command and an unknown flag are reported", func(t *testing.T) {
		_, err := p109HelpFor(t, "review nope-not-a-command")
		require.Error(t, err)
		help, err := p109HelpFor(t, "tools list")
		require.NoError(t, err)
		require.Empty(t, p109FieldProblems([]p109HelpJSON{help}, []string{"approval", "tier"}))
		require.Equal(t, []string{"not_a_flag"}, p109FieldProblems([]p109HelpJSON{help}, []string{"tier", "not_a_flag"}))
	})
}

// TestParity109CLIGroupsAreRegisteredInMain: the helper builds its own command
// tree, so this pins the production one. Every command group a CLI cell of the
// matrix names must be constructed and added to the root command in main.go.
func TestParity109CLIGroupsAreRegisteredInMain(t *testing.T) {
	if os.Getenv("MCPPROXY_109_PARITY_TARGET") != "" {
		t.Skip("helper process")
	}
	constructors := map[string]string{
		"attention": "GetAttentionCommand", "status": "GetStatusCommand", "doctor": "GetDoctorCommand",
		"upstream": "GetUpstreamCommand", "review": "GetReviewCommand", "tools": "GetToolsCommand",
		"client": "GetClientCommand", "connect": "GetConnectCommand", "token": "GetTokenCommand",
		"catalog": "GetCatalogCommand", "registry": "GetRegistryCommand", "activity": "GetActivityCommand",
		"telemetry": "GetTelemetryCommand",
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "specs", "109-ux-navigation-consistency", "parity-matrix.json"))
	require.NoError(t, err)
	var matrix p109CLIMatrix
	require.NoError(t, json.Unmarshal(raw, &matrix))
	mainSrc, err := os.ReadFile("main.go")
	require.NoError(t, err)
	src := string(mainSrc)

	groups := map[string]bool{}
	for _, row := range matrix.Rows {
		for _, id := range row.Cells["cli"].Ids {
			_, path, _ := strings.Cut(id, ":")
			groups[strings.Fields(path)[0]] = true
		}
	}
	require.GreaterOrEqual(t, len(groups), 10)
	for group := range groups {
		ctor, ok := constructors[group]
		require.True(t, ok, "the matrix names the %q group: add its constructor to this test and to p109HelpRoot", group)
		direct := "rootCmd.AddCommand(" + ctor + "())"
		assigned := regexp.MustCompile(`(\w+) := ` + ctor + `\(\)`).FindStringSubmatch(src)
		registered := strings.Contains(src, direct) || (assigned != nil && strings.Contains(src, "rootCmd.AddCommand("+assigned[1]+")"))
		require.True(t, registered, "main.go does not add %s() to the root command", ctor)
	}
}

type p109TermFamily struct {
	Values []string          `json:"values"`
	Labels map[string]string `json:"labels"`
	CLI    []string          `json:"cli"`
}

func p109FlagUsage(t *testing.T, path, flag string) string {
	t.Helper()
	help, err := p109HelpFor(t, path)
	require.NoError(t, err, path)
	for _, f := range help.Flags {
		if f.Name == flag {
			return f.Description
		}
	}
	t.Fatalf("`mcpproxy %s` has no --%s flag", path, flag)
	return ""
}

func TestParity109FlagUsageNamesTheValues(t *testing.T) {
	if os.Getenv("MCPPROXY_109_PARITY_TARGET") != "" {
		t.Skip("helper process")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "contracts", "testdata", "terminology.json"))
	require.NoError(t, err)
	var golden map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &golden))
	family := func(name string) p109TermFamily {
		var f p109TermFamily
		require.NoError(t, json.Unmarshal(golden[name], &f), name)
		return f
	}

	// tools list --approval: "approved (Approved), pending (New, needs review), ..."
	approval := family("tool_approval")
	var pairs []string
	for _, v := range approval.Values {
		pairs = append(pairs, fmt.Sprintf("%s (%s)", v, approval.Labels[v]))
	}
	require.Equal(t, "Filter by approval: "+strings.Join(pairs, ", "), p109FlagUsage(t, "tools list", "approval"))

	// tools list --tier names every tier the filter accepts (never `unknown`,
	// which only the review payload produces).
	tiers := family("tier")
	var filterTiers []string
	for _, v := range tiers.Values {
		if v != "unknown" {
			filterTiers = append(filterTiers, v)
		}
	}
	require.Equal(t, "Filter by tier: "+strings.Join(filterTiers, ", "), p109FlagUsage(t, "tools list", "tier"))

	// activity list --view lists the CLI views (no `sessions`).
	views := family("activity_view")
	require.True(t, strings.HasPrefix(p109FlagUsage(t, "activity list", "view"), "Filter by view: "+strings.Join(views.CLI, ", ")),
		"activity list --view must name exactly the CLI views %v", views.CLI)
	require.NotContains(t, p109FlagUsage(t, "activity list", "view"), "sessions")

	// upstream list --status names every health status, in order.
	statuses := family("health_status")
	require.True(t, strings.HasSuffix(p109FlagUsage(t, "upstream list", "status"), ": "+strings.Join(statuses.Values, ", ")),
		"upstream list --status must end with the status list %v", statuses.Values)
}
