package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
)

// Spec 108-l (FR-051, L4, T121): the CLI half of the parity-matrix check.
// Every CLI cell of specs/108-profiles-v3/parity-matrix.json names commands
// (`cmd:<path>`) and REST/MCP argument names (`fields`). Each command must be
// in the real `--help-json` output and each field must be a flag named like its
// kebab-case spelling (contracts/cli.md: "Flag names are the kebab-case of REST
// fields") on at least one command of the cell.
//
// The production --help-json hook exits the process after writing JSON, so the
// real hook runs in a helper process (the same pattern as help_json_108_test.go).

type p108ParityCell struct {
	Status string   `json:"status"`
	Ids    []string `json:"ids"`
	Fields []string `json:"fields"`
}

type p108ParityMatrix struct {
	Rows []struct {
		Row   int                       `json:"row"`
		Cells map[string]p108ParityCell `json:"cells"`
	} `json:"rows"`
}

type p108HelpJSON struct {
	Name  string `json:"name"`
	Flags []struct {
		Name string `json:"name"`
	} `json:"flags"`
}

// p108HelpRoot registers the same package-level command constructors main()
// adds, so `--help-json` sees the real flags.
func p108HelpRoot() *cobra.Command {
	root := &cobra.Command{Use: "mcpproxy"}
	root.AddCommand(
		GetProfileCommand(), GetClientCommand(), GetAccessCommand(), GetConnectCommand(),
		GetTokenCommand(), GetActivityCommand(), GetToolsCommand(), GetUpstreamCommand(),
		GetDoctorCommand(),
	)
	clioutput.SetupHelpJSON(root)
	return root
}

func p108KebabCase(field string) string { return strings.ReplaceAll(field, "_", "-") }

// p108HelpFor runs `mcpproxy <path> --help-json` in a helper process.
func p108HelpFor(t *testing.T, path string) (p108HelpJSON, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestParity108CLICellsResolve$")
	cmd.Env = append(os.Environ(), "MCPPROXY_108_PARITY_TARGET="+path)
	out, err := cmd.Output()
	if err != nil {
		return p108HelpJSON{}, err
	}
	var help p108HelpJSON
	require.NoError(t, json.Unmarshal(out, &help), string(out))
	// A group command swallows an unknown subcommand as an argument and prints
	// its own help, so the printed name must be the one asked for.
	if want := path[strings.LastIndex(path, " ")+1:]; help.Name != want {
		return p108HelpJSON{}, fmt.Errorf("`mcpproxy %s` resolved to %q", path, help.Name)
	}
	return help, nil
}

// p108FieldProblems lists the fields that no command in the set has as a flag.
func p108FieldProblems(helps []p108HelpJSON, fields []string) []string {
	var missing []string
	for _, field := range fields {
		found := false
		for _, help := range helps {
			for _, flag := range help.Flags {
				found = found || flag.Name == p108KebabCase(field)
			}
		}
		if !found {
			missing = append(missing, field)
		}
	}
	return missing
}

func TestParity108CLICellsResolve(t *testing.T) {
	if target := os.Getenv("MCPPROXY_108_PARITY_TARGET"); target != "" {
		root := p108HelpRoot()
		root.SetArgs(append(strings.Fields(target), "--help-json"))
		require.NoError(t, root.Execute())
		return
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "specs", "108-profiles-v3", "parity-matrix.json"))
	require.NoError(t, err, "parity-matrix.json missing (FR-051)")
	var matrix p108ParityMatrix
	require.NoError(t, json.Unmarshal(raw, &matrix))

	cache := map[string]p108HelpJSON{}
	checked := 0
	for _, row := range matrix.Rows {
		cell := row.Cells["cli"]
		if cell.Status != "must" {
			require.Empty(t, cell.Ids, "row %d: only a ticked CLI cell names commands", row.Row)
			continue
		}
		var helps []p108HelpJSON
		for _, id := range cell.Ids {
			kind, path, ok := strings.Cut(id, ":")
			require.True(t, ok && kind == "cmd", "row %d: CLI ids are cmd:<path>, got %q", row.Row, id)
			help, seen := cache[path]
			if !seen {
				help, err = p108HelpFor(t, path)
				require.NoError(t, err, "row %d: `mcpproxy %s --help-json` is not a command", row.Row, path)
				cache[path] = help
			}
			helps = append(helps, help)
			checked++
		}
		require.Empty(t, p108FieldProblems(helps, cell.Fields),
			"row %d: a REST/MCP field has no matching --flag on the cell's commands", row.Row)
	}
	require.Greater(t, checked, 20, "the CLI walk saw too few commands: the matrix reader is broken")

	t.Run("mutation self-check: an unknown command and an unknown flag are reported", func(t *testing.T) {
		_, err := p108HelpFor(t, "profile nope-not-a-command")
		require.Error(t, err)
		help, err := p108HelpFor(t, "profile create")
		require.NoError(t, err)
		require.Empty(t, p108FieldProblems([]p108HelpJSON{help}, []string{"max_tier"}))
		require.Equal(t, []string{"not_a_flag"}, p108FieldProblems([]p108HelpJSON{help}, []string{"max_tier", "not_a_flag"}))
	})
}
