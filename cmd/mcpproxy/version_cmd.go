package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
)

// Injected via -ldflags "-X main.commit=… -X main.date=…" (Makefile, scripts/build.sh).
// CI release workflows only set main.version, so these may be empty.
var (
	commit = ""
	date   = ""
)

// versionLine returns the human-readable version string shared by
// `mcpproxy version` and the root command's --version flag.
func versionLine() string {
	return fmt.Sprintf("MCPProxy %s (%s) %s/%s\n", version, Edition, runtime.GOOS, runtime.GOARCH)
}

// VersionInfo is the machine-readable version payload for -o json/yaml.
type VersionInfo struct {
	Version string `json:"version" yaml:"version"`
	Edition string `json:"edition" yaml:"edition"`
	OS      string `json:"os" yaml:"os"`
	Arch    string `json:"arch" yaml:"arch"`
	Commit  string `json:"commit,omitempty" yaml:"commit,omitempty"`
	Date    string `json:"date,omitempty" yaml:"date,omitempty"`
}

func newVersionInfo() VersionInfo {
	return VersionInfo{
		Version: version,
		Edition: Edition,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Commit:  commit,
		Date:    date,
	}
}

func printVersionOutput(w io.Writer, info VersionInfo, format string) error {
	// Table output is the shared human-readable line (byte-identical to
	// --version), not the generic table formatter.
	if f := strings.ToLower(format); f == "" || f == "table" {
		fmt.Fprint(w, versionLine())
		return nil
	}
	// NewFormatter is case-insensitive and rejects unknown formats.
	formatter, err := clioutput.NewFormatter(format)
	if err != nil {
		return err
	}
	out, err := formatter.Format(info)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, out)
	return nil
}

// GetVersionCommand returns the `version` subcommand.
func GetVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print MCPProxy version, edition, and platform",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format := clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput)
			return printVersionOutput(cmd.OutOrStdout(), newVersionInfo(), format)
		},
	}
}
