package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
)

// TestHelpJSON108Goldens runs the real --help-json hook in a helper process (the
// production hook exits after writing JSON). Every new command and flag is
// listed in machine-readable help.
func TestHelpJSON108Goldens(t *testing.T) {
	if target := os.Getenv("MCPPROXY_108_HELP_TARGET"); target != "" {
		root := &cobra.Command{Use: "mcpproxy"}
		root.AddCommand(GetProfileCommand(), GetClientCommand(), GetAccessCommand())
		clioutput.SetupHelpJSON(root)
		root.SetArgs(append(strings.Fields(target), "--help-json"))
		require.NoError(t, root.Execute())
		return
	}
	for golden, target := range map[string]string{
		"profile-help-json.golden":            "profile",
		"profile-create-help-json.golden":     "profile create",
		"client-set-profile-help-json.golden": "client set-profile",
		"access-explain-help-json.golden":     "access explain",
	} {
		t.Run(golden, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelpJSON108Goldens$")
			cmd.Env = append(os.Environ(), "MCPPROXY_108_HELP_TARGET="+target)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			var payload map[string]any
			require.NoError(t, json.Unmarshal(out, &payload), string(out))
			require.Equal(t, target[strings.LastIndex(target, " ")+1:], payload["name"])
			assertGolden108(t, golden, string(out))
		})
	}
}
