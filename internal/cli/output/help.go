package output

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// HelpInfo contains structured help information for a command hierarchy.
type HelpInfo struct {
	// Name is the command name
	Name string `json:"name"`

	// Description is a short description of the command
	Description string `json:"description"`

	// Usage shows how to use the command
	Usage string `json:"usage"`

	// Flags lists all available flags for this command
	Flags []FlagInfo `json:"flags,omitempty"`

	// Commands lists subcommands (for parent commands)
	Commands []CommandInfo `json:"commands,omitempty"`
}

// CommandInfo contains information about a subcommand.
type CommandInfo struct {
	// Name is the subcommand name
	Name string `json:"name"`

	// Description is a short description of the subcommand
	Description string `json:"description"`

	// Usage shows how to use the subcommand
	Usage string `json:"usage"`

	// HasSubcommands indicates if this command has further subcommands
	HasSubcommands bool `json:"has_subcommands,omitempty"`
}

// FlagInfo contains information about a command flag.
type FlagInfo struct {
	// Name is the long flag name (e.g., "output")
	Name string `json:"name"`

	// Shorthand is the short flag (e.g., "o")
	Shorthand string `json:"shorthand,omitempty"`

	// Description describes what the flag does
	Description string `json:"description"`

	// Type is the flag type (string, bool, int, etc.)
	Type string `json:"type"`

	// Default is the default value
	Default string `json:"default,omitempty"`

	// Required indicates if the flag is required
	Required bool `json:"required,omitempty"`
}

// ExtractHelpInfo builds a HelpInfo struct from a cobra.Command.
func ExtractHelpInfo(cmd *cobra.Command) HelpInfo {
	info := HelpInfo{
		Name:        cmd.Name(),
		Description: cmd.Short,
		Usage:       cmd.UseLine(),
	}

	// Extract flags
	info.Flags = extractFlags(cmd)

	// Extract subcommands
	for _, sub := range cmd.Commands() {
		if sub.Hidden || !sub.IsAvailableCommand() {
			continue
		}
		cmdInfo := CommandInfo{
			Name:           sub.Name(),
			Description:    sub.Short,
			Usage:          sub.UseLine(),
			HasSubcommands: len(sub.Commands()) > 0,
		}
		info.Commands = append(info.Commands, cmdInfo)
	}

	return info
}

// extractFlags extracts flag information from a command.
func extractFlags(cmd *cobra.Command) []FlagInfo {
	var flags []FlagInfo

	// Add local flags
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		flags = append(flags, FlagInfo{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Usage,
			Type:        f.Value.Type(),
			Default:     f.DefValue,
		})
	})

	// Add inherited persistent flags
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		flags = append(flags, FlagInfo{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Usage,
			Type:        f.Value.Type(),
			Default:     f.DefValue,
		})
	})

	return flags
}

// AddHelpJSONFlag adds a --help-json flag to a command.
// When invoked, it outputs structured help information as JSON.
func AddHelpJSONFlag(cmd *cobra.Command) {
	var helpJSON bool
	cmd.PersistentFlags().BoolVar(&helpJSON, "help-json", false, "Output help information as JSON")

	// Store original PreRunE if any
	originalPreRunE := cmd.PreRunE

	// Wrap PreRunE to check for --help-json
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if helpJSON {
			// Output help as JSON and exit
			info := ExtractHelpInfo(cmd)
			output, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal help info: %w", err)
			}
			fmt.Println(string(output))
			os.Exit(0)
		}

		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}
		return nil
	}
}

// SetupHelpJSON sets up --help-json on a command tree.
// It adds the flag and hooks to check for it on all commands.
func SetupHelpJSON(rootCmd *cobra.Command) {
	rootCmd.PersistentFlags().Bool("help-json", false, "Output help information as JSON")

	// Store original PersistentPreRunE if any
	originalPersistentPreRunE := rootCmd.PersistentPreRunE

	// Wrap PersistentPreRunE to check for --help-json
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		helpJSONFlag, _ := cmd.Flags().GetBool("help-json")
		if helpJSONFlag {
			info := ExtractHelpInfo(cmd)
			output, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal help info: %w", err)
			}
			fmt.Println(string(output))
			os.Exit(0)
		}

		if originalPersistentPreRunE != nil {
			return originalPersistentPreRunE(cmd, args)
		}
		return nil
	}

	// Also add a hook for parent commands that don't have Run
	// by wrapping the help template
	addHelpJSONToTree(rootCmd)
}

// addHelpJSONToTree recursively adds --help-json support to commands that don't have Run.
func addHelpJSONToTree(cmd *cobra.Command) {
	// For commands without Run (like "upstream"), add a Run that checks for --help-json
	if cmd.Run == nil && cmd.RunE == nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			helpJSONFlag, _ := c.Flags().GetBool("help-json")
			if helpJSONFlag {
				info := ExtractHelpInfo(c)
				output, err := json.MarshalIndent(info, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal help info: %w", err)
				}
				fmt.Println(string(output))
				return nil
			}
			// Show normal help if not --help-json
			return c.Help()
		}
	}

	// Recurse into subcommands
	for _, sub := range cmd.Commands() {
		addHelpJSONToTree(sub)
	}
}
