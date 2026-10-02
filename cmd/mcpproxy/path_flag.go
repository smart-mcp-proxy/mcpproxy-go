package main

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// nonEmptyPathValue is the pflag.Value behind every --config / --data-dir flag.
// An explicit empty value (`-c ""`, `--config=`, an unset shell variable that
// expanded to nothing) used to be indistinguishable from "no flag", so a
// management command silently fell back to discovery and, when nothing was
// found, created $HOME/.mcpproxy/mcp_config.json and reported an empty server
// list. Reject it at parse time instead.
type nonEmptyPathValue struct {
	target *string
}

func newPathFlag(p *string) pflag.Value {
	return &nonEmptyPathValue{target: p}
}

func (v *nonEmptyPathValue) String() string {
	if v == nil || v.target == nil {
		return ""
	}
	return *v.target
}

func (v *nonEmptyPathValue) Set(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("must not be empty; omit the flag to use the default")
	}
	*v.target = s
	return nil
}

// Type keeps --help and --help-json identical to a plain string flag.
func (v *nonEmptyPathValue) Type() string { return "string" }

// addConfigFlag registers a --config/-c flag that rejects empty values.
func addConfigFlag(fs *pflag.FlagSet, p *string, usage string) {
	fs.VarP(newPathFlag(p), "config", "c", usage)
}

// registerRootPathFlags registers the root persistent -c/--config and
// -d/--data-dir flags.
func registerRootPathFlags(root *cobra.Command) {
	root.PersistentFlags().VarP(newPathFlag(&configFile), "config", "c", "Configuration file path")
	root.PersistentFlags().VarP(newPathFlag(&dataDir), "data-dir", "d", "Data directory path (default: ~/.mcpproxy)")
}
