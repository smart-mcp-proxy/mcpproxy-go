package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"golang.org/x/term"
)

// GetSecretsCommand returns the secrets management command
func GetSecretsCommand() *cobra.Command {
	secretsCmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage secrets stored in OS keyring",
		Long:  "Store, retrieve, and manage secrets using the operating system's secure keyring (Keychain on macOS, Secret Service on Linux, WinCred on Windows)",
	}

	// Add subcommands
	secretsCmd.AddCommand(getSecretsSetCommand())
	secretsCmd.AddCommand(getSecretsGetCommand())
	secretsCmd.AddCommand(getSecretsDeleteCommand())
	secretsCmd.AddCommand(getSecretsListCommand())
	secretsCmd.AddCommand(getSecretsMigrateCommand())

	return secretsCmd
}

// secretsSetOptions are the flags of `secrets set`.
type secretsSetOptions struct {
	secretType string
	fromEnv    string
	fromStdin  bool
}

// secretsSetLong is the `secrets set --help` text. The macOS paragraph exists
// because the opt-in is otherwise discoverable only by failing (GH #1924).
const secretsSetLong = `Store a secret in the OS keyring. If no value is provided, will prompt for input.

macOS: writing to the Keychain from a terminal requires an explicit opt-in,
because a missing or locked keychain makes macOS show a system dialog that
mcpproxy cannot cancel. Opt in for one command:

  MCPPROXY_KEYRING_WRITE=1 mcpproxy secrets set <name>

The tray app opts in automatically for the core it launches. Linux and Windows
need no opt-in.`

// getSecretsSetCommand returns the secrets set command
func getSecretsSetCommand() *cobra.Command {
	var opts secretsSetOptions

	cmd := &cobra.Command{
		Use:   "set <name> [value]",
		Short: "Store a secret in the keyring",
		Long:  secretsSetLong,
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The arguments parsed; from here a failure is about the keyring,
			// and the usage text would only bury the explanation.
			cmd.SilenceUsage = true
			return runSecretsSet(secret.NewResolver(), os.Stdin, os.Stdout, args, opts)
		},
	}

	cmd.Flags().StringVar(&opts.secretType, "type", "keyring", "Secret provider type (keyring, env)")
	cmd.Flags().StringVar(&opts.fromEnv, "from-env", "", "Read value from environment variable")
	cmd.Flags().BoolVar(&opts.fromStdin, "from-stdin", false, "Read value from stdin")

	return cmd
}

// runSecretsSet implements `secrets set`: args is <name> [value].
func runSecretsSet(resolver *secret.Resolver, in io.Reader, out io.Writer, args []string, opts secretsSetOptions) error {
	name := args[0]

	// GH #1924: decide whether the write can happen BEFORE asking for the
	// value. The keyring provider refuses a Store on macOS without the write
	// opt-in, and the user used to learn that only after typing the secret.
	if opts.secretType == secret.SecretTypeKeyring {
		if err := keyringWritePreflight(resolver, name, len(args) >= 2, opts); err != nil {
			return err
		}
	}

	var value string

	// Determine how to get the secret value
	switch {
	case len(args) >= 2:
		value = args[1]
	case opts.fromEnv != "":
		value = os.Getenv(opts.fromEnv)
		if value == "" {
			return fmt.Errorf("environment variable %s is not set or empty", opts.fromEnv)
		}
	default:
		// --from-stdin is piped input: a prompt there would only pollute the
		// output of a script.
		if !opts.fromStdin {
			fmt.Fprint(out, "Enter secret value: ")
		}
		var err error
		value, err = readPassword(in)
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
	}

	if value == "" {
		return fmt.Errorf("secret value cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ref := secret.Ref{
		Type: opts.secretType,
		Name: name,
	}

	if err := resolver.Store(ctx, ref, value); err != nil {
		return fmt.Errorf("failed to store secret: %w", err)
	}

	fmt.Fprintf(out, "Secret '%s' stored successfully in %s\n", name, opts.secretType)
	fmt.Fprintf(out, "Use in config: ${%s:%s}\n", opts.secretType, name)

	return nil
}

// keyringWritePreflight reports why a keyring write cannot happen, or nil
// when it can be attempted. It keeps two conditions apart that used to share
// one "keyring unavailable" message (GH #1924):
//
//   - the macOS write opt-in is absent — nothing is wrong with the Keychain,
//     and the user can lift it, so the error is the command to run;
//   - the keyring really is unusable (headless session, locked or
//     unresponsive backend) — the provider's own reason is reported.
//
// The opt-in is never taken on the user's behalf: it exists because a
// Keychain write can raise a system dialog mcpproxy cannot cancel.
func keyringWritePreflight(resolver *secret.Resolver, name string, valueAsArg bool, opts secretsSetOptions) error {
	if resolver.KeyringWriteOptInRequired() {
		// Repeat how the value was going to be supplied, so the command can
		// be re-run as printed. A value given as an argument is never echoed.
		retry := "MCPPROXY_KEYRING_WRITE=1 mcpproxy secrets set " + shellQuoteArg(name)
		switch {
		case valueAsArg:
			retry += " <value>"
		case opts.fromEnv != "":
			retry += " --from-env=" + shellQuoteArg(opts.fromEnv)
		case opts.fromStdin:
			retry += " --from-stdin"
		}
		return fmt.Errorf("storing a secret in the macOS Keychain from a terminal requires an explicit opt-in; nothing was stored and the Keychain was not touched.\n\n"+
			"Run:\n  %s\n\n"+
			"Why: if the keychain is missing or locked, macOS shows a system dialog that mcpproxy cannot cancel, so it never writes without being asked. "+
			"The tray app opts in automatically for the core it launches", retry)
	}
	if ok, reason := resolver.KeyringAvailability(); !ok {
		return fmt.Errorf("cannot store secret: OS keyring unavailable (%s)", reason)
	}
	return nil
}

// getSecretsGetCommand returns the secrets get command
func getSecretsGetCommand() *cobra.Command {
	var (
		secretType string
		masked     bool
	)

	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Retrieve a secret from the keyring",
		Long:  "Retrieve a secret from the OS keyring. By default, output is masked for security.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]

			resolver := secret.NewResolver()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			ref := secret.Ref{
				Type: secretType,
				Name: name,
			}

			value, err := resolver.Resolve(ctx, ref)
			if err != nil {
				return fmt.Errorf("failed to retrieve secret: %w", err)
			}

			if masked {
				fmt.Printf("%s: %s\n", name, secret.MaskSecretValue(value))
			} else {
				fmt.Printf("%s: %s\n", name, value)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&secretType, "type", "keyring", "Secret provider type (keyring, env)")
	cmd.Flags().BoolVar(&masked, "masked", true, "Mask the secret value in output")

	return cmd
}

// getSecretsDeleteCommand returns the secrets delete command
func getSecretsDeleteCommand() *cobra.Command {
	var secretType string

	cmd := &cobra.Command{
		Use:   "del <name>",
		Short: "Delete a secret from the keyring",
		Long:  "Delete a secret from the OS keyring.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]

			resolver := secret.NewResolver()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			ref := secret.Ref{
				Type: secretType,
				Name: name,
			}

			err := resolver.Delete(ctx, ref)
			if err != nil {
				return fmt.Errorf("failed to delete secret: %w", err)
			}

			fmt.Printf("Secret '%s' deleted successfully from %s\n", name, secretType)

			return nil
		},
	}

	cmd.Flags().StringVar(&secretType, "type", "keyring", "Secret provider type (keyring, env)")

	return cmd
}

// getSecretsListCommand returns the secrets list command
func getSecretsListCommand() *cobra.Command {
	var allTypes bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all stored secrets",
		Long:  "List all secrets stored in available providers. Secret values are never displayed.",
		RunE: func(_ *cobra.Command, _ []string) error {
			resolver := secret.NewResolver()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Get output format from global flags
			outputFormat := ResolveOutputFormat()
			formatter, err := GetOutputFormatter()
			if err != nil {
				return output.NewStructuredError(output.ErrCodeInvalidOutputFormat, err.Error()).
					WithGuidance("Use -o table, -o json, or -o yaml")
			}

			var refs []secret.Ref
			var providerName string

			if allTypes {
				// List from all available providers
				refs, err = resolver.ListAll(ctx)
				if err != nil {
					return fmt.Errorf("failed to list secrets: %w", err)
				}
				providerName = "all providers"
			} else {
				// List from keyring only
				keyringProvider := secret.NewKeyringProvider()
				if !keyringProvider.IsAvailable() {
					return fmt.Errorf("keyring is not available on this system")
				}

				refs, err = keyringProvider.List(ctx)
				if err != nil {
					return fmt.Errorf("failed to list keyring secrets: %w", err)
				}
				providerName = "keyring"
			}

			// Handle JSON/YAML output
			if outputFormat == "json" || outputFormat == "yaml" {
				result, fmtErr := formatter.Format(refs)
				if fmtErr != nil {
					return fmt.Errorf("failed to format output: %w", fmtErr)
				}
				fmt.Println(result)
				return nil
			}

			// Table output
			if len(refs) == 0 {
				fmt.Printf("No secrets found in %s\n", providerName)
				return nil
			}

			headers := []string{"NAME", "TYPE"}
			var rows [][]string
			for _, ref := range refs {
				rows = append(rows, []string{ref.Name, ref.Type})
			}

			result, fmtErr := formatter.FormatTable(headers, rows)
			if fmtErr != nil {
				return fmt.Errorf("failed to format table: %w", fmtErr)
			}
			fmt.Print(result)
			return nil
		},
	}

	// Note: --json flag removed, use global -o json instead
	cmd.Flags().BoolVar(&allTypes, "all", false, "List secrets from all available providers")

	return cmd
}

// getSecretsMigrateCommand returns the secrets migrate command
func getSecretsMigrateCommand() *cobra.Command {
	var (
		dryRun      bool
		autoApprove bool
		fromType    string
		toType      string
	)

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate plaintext secrets to secure storage",
		Long:  "Analyze configuration for plaintext secrets and migrate them to secure keyring storage.",
		RunE: func(_ *cobra.Command, _ []string) error {
			// Initialize logger
			logger, err := logs.SetupLogger(&config.LogConfig{
				Level:         logLevel,
				EnableFile:    false,
				EnableConsole: true,
				JSONFormat:    false,
			})
			if err != nil {
				return fmt.Errorf("failed to setup logger: %w", err)
			}
			defer func() { _ = logger.Sync() }()

			// Load configuration
			cfg, err := config.LoadFromFile(configFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Analyze configuration for potential secrets
			resolver := secret.NewResolver()
			analysis := resolver.AnalyzeForMigration(cfg)

			if len(analysis.Candidates) == 0 {
				fmt.Println("No potential secrets found for migration")
				return nil
			}

			fmt.Printf("Found %d potential secrets for migration:\n\n", analysis.TotalFound)

			for i, candidate := range analysis.Candidates {
				fmt.Printf("%d. Field: %s\n", i+1, candidate.Field)
				fmt.Printf("   Current value: %s\n", candidate.Value)
				fmt.Printf("   Suggested ref: %s\n", candidate.Suggested)
				fmt.Printf("   Confidence: %.1f%%\n\n", candidate.Confidence*100)
			}

			if dryRun {
				fmt.Println("Dry run completed. No changes made.")
				return nil
			}

			if !autoApprove {
				fmt.Print("Proceed with migration? (y/N): ")
				var response string
				_, _ = fmt.Scanln(&response)
				if !strings.EqualFold(response, "y") && !strings.EqualFold(response, "yes") {
					fmt.Println("Migration cancelled")
					return nil
				}
			}

			fmt.Println("Migration feature not yet implemented. Use 'mcpproxy secrets set' to manually store secrets.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be migrated without making changes")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "Automatically approve all migrations")
	cmd.Flags().StringVar(&fromType, "from", "plaintext", "Source type (plaintext)")
	cmd.Flags().StringVar(&toType, "to", "keyring", "Target type (keyring)")

	return cmd
}

// shellQuoteArg renders arg so a POSIX shell reads it back as one word. A
// plain word is returned as is; anything else is single-quoted ("=" included:
// zsh expands a leading "=name" to a command path), so a secret
// name with a space or a shell metacharacter survives being copied from a
// printed command.
func shellQuoteArg(arg string) string {
	plain := arg != ""
	for _, r := range arg {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_-./:@%+,", r) {
			plain = false
			break
		}
	}
	if plain {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// readPassword reads a secret without echoing the typed characters when the
// input is a terminal. For piped input (including --from-stdin) there is
// nothing to echo and the whole stream is the value.
func readPassword(in io.Reader) (string, error) {
	if fd, ok := terminalFd(in); ok {
		password, err := term.ReadPassword(fd)
		fmt.Println()
		return string(password), err
	}
	return readSecretStdin(in)
}

// terminalFd reports the file descriptor of in when it is a terminal, which
// is the only case where typed characters would otherwise be echoed.
func terminalFd(in io.Reader) (int, bool) {
	f, ok := in.(*os.File)
	if !ok {
		return 0, false
	}
	fd := int(f.Fd())
	return fd, term.IsTerminal(fd)
}

// readSecretStdin reads all piped input and removes only line-ending bytes.
func readSecretStdin(input io.Reader) (string, error) {
	value, err := io.ReadAll(input)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(value), "\r\n"), nil
}
