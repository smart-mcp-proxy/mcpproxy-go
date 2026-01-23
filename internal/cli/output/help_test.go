package output

import (
	"testing"

	"github.com/spf13/cobra"
)

// T037: Unit test for HelpInfo structure
func TestHelpInfo_Structure(t *testing.T) {
	info := HelpInfo{
		Name:        "test",
		Description: "A test command",
		Usage:       "test [flags]",
		Flags: []FlagInfo{
			{
				Name:        "output",
				Shorthand:   "o",
				Description: "Output format",
				Type:        "string",
				Default:     "table",
			},
		},
		Commands: []CommandInfo{
			{
				Name:           "sub",
				Description:    "A subcommand",
				Usage:          "test sub",
				HasSubcommands: false,
			},
		},
	}

	if info.Name != "test" {
		t.Errorf("Expected name 'test', got: %s", info.Name)
	}
	if len(info.Flags) != 1 {
		t.Errorf("Expected 1 flag, got: %d", len(info.Flags))
	}
	if len(info.Commands) != 1 {
		t.Errorf("Expected 1 command, got: %d", len(info.Commands))
	}
}

// T038: Unit test for ExtractHelpInfo from Cobra command
func TestExtractHelpInfo(t *testing.T) {
	// Create a test command hierarchy
	rootCmd := &cobra.Command{
		Use:   "myapp",
		Short: "My test application",
	}

	subCmd := &cobra.Command{
		Use:   "list",
		Short: "List items",
		Run:   func(cmd *cobra.Command, args []string) {}, // Need a Run function for command to be available
	}
	rootCmd.AddCommand(subCmd)

	// Add a flag to root command
	rootCmd.Flags().StringP("output", "o", "table", "Output format")

	info := ExtractHelpInfo(rootCmd)

	if info.Name != "myapp" {
		t.Errorf("Expected name 'myapp', got: %s", info.Name)
	}
	if info.Description != "My test application" {
		t.Errorf("Expected description 'My test application', got: %s", info.Description)
	}

	// Should have the subcommand
	if len(info.Commands) != 1 {
		t.Fatalf("Expected 1 subcommand, got: %d", len(info.Commands))
	}
	if info.Commands[0].Name != "list" {
		t.Errorf("Expected subcommand 'list', got: %s", info.Commands[0].Name)
	}
}

func TestExtractHelpInfo_WithSubcommands(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "app",
		Short: "Application",
	}

	parentCmd := &cobra.Command{
		Use:   "parent",
		Short: "Parent command",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	childCmd := &cobra.Command{
		Use:   "child",
		Short: "Child command",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	parentCmd.AddCommand(childCmd)
	rootCmd.AddCommand(parentCmd)

	info := ExtractHelpInfo(rootCmd)

	// Root should show parent command
	if len(info.Commands) != 1 {
		t.Fatalf("Expected 1 command, got: %d", len(info.Commands))
	}

	// Parent command should indicate it has subcommands
	if !info.Commands[0].HasSubcommands {
		t.Error("Expected HasSubcommands to be true for parent command")
	}
}

func TestExtractHelpInfo_HiddenCommands(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "app",
		Short: "Application",
	}

	visibleCmd := &cobra.Command{
		Use:   "visible",
		Short: "Visible command",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	hiddenCmd := &cobra.Command{
		Use:    "hidden",
		Short:  "Hidden command",
		Hidden: true,
		Run:    func(cmd *cobra.Command, args []string) {},
	}

	rootCmd.AddCommand(visibleCmd)
	rootCmd.AddCommand(hiddenCmd)

	info := ExtractHelpInfo(rootCmd)

	// Should only have the visible command
	if len(info.Commands) != 1 {
		t.Errorf("Expected 1 visible command, got: %d", len(info.Commands))
	}
	if len(info.Commands) > 0 && info.Commands[0].Name != "visible" {
		t.Errorf("Expected 'visible' command, got: %s", info.Commands[0].Name)
	}
}

// T039: Unit test for FlagInfo extraction
func TestExtractHelpInfo_Flags(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test command",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	cmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")
	cmd.Flags().BoolP("verbose", "v", false, "Enable verbose output")
	cmd.Flags().Int("limit", 10, "Maximum number of results")

	info := ExtractHelpInfo(cmd)

	if len(info.Flags) != 3 {
		t.Fatalf("Expected 3 flags, got: %d", len(info.Flags))
	}

	// Find the output flag
	var outputFlag *FlagInfo
	for i := range info.Flags {
		if info.Flags[i].Name == "output" {
			outputFlag = &info.Flags[i]
			break
		}
	}

	if outputFlag == nil {
		t.Fatal("Expected to find 'output' flag")
		return // unreachable but satisfies staticcheck SA5011
	}

	if outputFlag.Shorthand != "o" {
		t.Errorf("Expected shorthand 'o', got: %s", outputFlag.Shorthand)
	}
	if outputFlag.Type != "string" {
		t.Errorf("Expected type 'string', got: %s", outputFlag.Type)
	}
	if outputFlag.Default != "table" {
		t.Errorf("Expected default 'table', got: %s", outputFlag.Default)
	}
}

func TestExtractHelpInfo_HiddenFlags(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test command",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	cmd.Flags().String("visible", "", "Visible flag")
	cmd.Flags().String("hidden", "", "Hidden flag")
	cmd.Flags().MarkHidden("hidden")

	info := ExtractHelpInfo(cmd)

	// Should only have the visible flag
	if len(info.Flags) != 1 {
		t.Errorf("Expected 1 visible flag, got: %d", len(info.Flags))
	}
	if len(info.Flags) > 0 && info.Flags[0].Name != "visible" {
		t.Errorf("Expected 'visible' flag, got: %s", info.Flags[0].Name)
	}
}

func TestExtractHelpInfo_InheritedFlags(t *testing.T) {
	rootCmd := &cobra.Command{
		Use:   "app",
		Short: "Application",
	}
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")

	subCmd := &cobra.Command{
		Use:   "sub",
		Short: "Subcommand",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	subCmd.Flags().Bool("local", false, "Local flag")

	rootCmd.AddCommand(subCmd)

	info := ExtractHelpInfo(subCmd)

	// Should have both local and inherited flags
	if len(info.Flags) < 2 {
		t.Errorf("Expected at least 2 flags (local + inherited), got: %d", len(info.Flags))
	}

	// Find the inherited output flag
	var hasOutputFlag bool
	var hasLocalFlag bool
	for _, f := range info.Flags {
		if f.Name == "output" {
			hasOutputFlag = true
		}
		if f.Name == "local" {
			hasLocalFlag = true
		}
	}

	if !hasOutputFlag {
		t.Error("Expected inherited 'output' flag to be present")
	}
	if !hasLocalFlag {
		t.Error("Expected local 'local' flag to be present")
	}
}

func TestFlagInfo_Structure(t *testing.T) {
	flag := FlagInfo{
		Name:        "timeout",
		Shorthand:   "t",
		Description: "Request timeout in seconds",
		Type:        "int",
		Default:     "30",
		Required:    false,
	}

	if flag.Name != "timeout" {
		t.Errorf("Expected name 'timeout', got: %s", flag.Name)
	}
	if flag.Shorthand != "t" {
		t.Errorf("Expected shorthand 't', got: %s", flag.Shorthand)
	}
	if flag.Type != "int" {
		t.Errorf("Expected type 'int', got: %s", flag.Type)
	}
}

func TestCommandInfo_Structure(t *testing.T) {
	cmd := CommandInfo{
		Name:           "serve",
		Description:    "Start the server",
		Usage:          "app serve [flags]",
		HasSubcommands: false,
	}

	if cmd.Name != "serve" {
		t.Errorf("Expected name 'serve', got: %s", cmd.Name)
	}
	if cmd.HasSubcommands {
		t.Error("Expected HasSubcommands to be false")
	}
}
