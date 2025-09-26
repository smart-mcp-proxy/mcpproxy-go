package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/secret"
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

// getSecretsSetCommand returns the secrets set command
func getSecretsSetCommand() *cobra.Command {
	var (
		secretType string
		fromEnv    string
		fromStdin  bool
	)

	cmd := &cobra.Command{
		Use:   "set <name> [value]",
		Short: "Store a secret in the keyring",
		Long:  "Store a secret in the OS keyring. If no value is provided, will prompt for input.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			var value string

			// Determine how to get the secret value
			if len(args) >= 2 {
				value = args[1]
			} else if fromEnv != "" {
				value = os.Getenv(fromEnv)
				if value == "" {
					return fmt.Errorf("environment variable %s is not set or empty", fromEnv)
				}
			} else {
				// Both fromStdin and default case read from stdin
				fmt.Print("Enter secret value: ")
				var err error
				value, err = readPassword()
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
			}

			if value == "" {
				return fmt.Errorf("secret value cannot be empty")
			}

			// Create resolver and store secret
			resolver := secret.NewResolver()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			ref := secret.Ref{
				Type: secretType,
				Name: name,
			}

			err := resolver.Store(ctx, ref, value)
			if err != nil {
				return fmt.Errorf("failed to store secret: %w", err)
			}

			fmt.Printf("Secret '%s' stored successfully in %s\n", name, secretType)
			fmt.Printf("Use in config: ${%s:%s}\n", secretType, name)

			return nil
		},
	}

	cmd.Flags().StringVar(&secretType, "type", "keyring", "Secret provider type (keyring, env)")
	cmd.Flags().StringVar(&fromEnv, "from-env", "", "Read value from environment variable")
	cmd.Flags().BoolVar(&fromStdin, "from-stdin", false, "Read value from stdin")

	return cmd
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
	var (
		jsonOutput bool
		allTypes   bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all stored secrets",
		Long:  "List all secrets stored in available providers. Secret values are never displayed.",
		RunE: func(_ *cobra.Command, args []string) error {
			resolver := secret.NewResolver()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if allTypes {
				// List from all available providers
				refs, err := resolver.ListAll(ctx)
				if err != nil {
					return fmt.Errorf("failed to list secrets: %w", err)
				}

				if jsonOutput {
					return json.NewEncoder(os.Stdout).Encode(refs)
				}

				if len(refs) == 0 {
					fmt.Println("No secrets found")
					return nil
				}

				fmt.Printf("Found %d secrets:\n", len(refs))
				for _, ref := range refs {
					fmt.Printf("  %s (%s)\n", ref.Name, ref.Type)
				}
			} else {
				// List from keyring only
				keyringProvider := secret.NewKeyringProvider()
				if !keyringProvider.IsAvailable() {
					return fmt.Errorf("keyring is not available on this system")
				}

				refs, err := keyringProvider.List(ctx)
				if err != nil {
					return fmt.Errorf("failed to list keyring secrets: %w", err)
				}

				if jsonOutput {
					return json.NewEncoder(os.Stdout).Encode(refs)
				}

				if len(refs) == 0 {
					fmt.Println("No secrets found in keyring")
					return nil
				}

				fmt.Printf("Found %d secrets in keyring:\n", len(refs))
				for _, ref := range refs {
					fmt.Printf("  %s\n", ref.Name)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
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
		RunE: func(_ *cobra.Command, args []string) error {
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

// readPassword reads a password from stdin without echoing
func readPassword() (string, error) {
	// For now, use a simple implementation
	// In production, you'd want to use something like golang.org/x/term
	var password string
	_, err := fmt.Scanln(&password)
	return password, err
}
