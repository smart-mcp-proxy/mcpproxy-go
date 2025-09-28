package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/tlslocal"

	"github.com/spf13/cobra"
)

const (
	darwinOS = "darwin"
)

var (
	trustCertCmd = &cobra.Command{
		Use:   "trust-cert",
		Short: "Install mcpproxy CA certificate as trusted",
		Long: `Install the mcpproxy CA certificate into the system's trusted certificate store.

This enables HTTPS connections to mcpproxy without certificate warnings.
You'll be prompted for your password once.

After installation:
1. Enable HTTPS in config: "tls": { "enabled": true }
2. Or use environment variable: export MCPPROXY_TLS_ENABLED=true

For Claude Desktop, add to your config:
  "env": {
    "NODE_EXTRA_CA_CERTS": "~/.mcpproxy/certs/ca.pem"
  }

Examples:
  mcpproxy trust-cert                    # Install certificate with prompts
  mcpproxy trust-cert --force            # Install without confirmation prompt
  mcpproxy trust-cert --keychain=login   # Install to login keychain only`,
		RunE: runTrustCert,
	}

	trustCertForce    bool
	trustCertKeychain string
)

func init() {
	trustCertCmd.Flags().BoolVar(&trustCertForce, "force", false, "Install certificate without confirmation prompt")
	trustCertCmd.Flags().StringVar(&trustCertKeychain, "keychain", "system", "Target keychain: 'system' or 'login'")
}

func runTrustCert(_ *cobra.Command, _ []string) error {
	// Load configuration to get certificate directory
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Determine certificate directory
	certsDir := cfg.TLS.CertsDir
	if certsDir == "" {
		dataDir := cfg.DataDir
		if dataDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get user home directory: %w", err)
			}
			dataDir = filepath.Join(homeDir, ".mcpproxy")
		}
		certsDir = filepath.Join(dataDir, "certs")
	}

	caCertPath := filepath.Join(certsDir, "ca.pem")

	// Check if certificate exists, if not generate it
	if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
		fmt.Printf("Certificate not found at %s\n", caCertPath)
		fmt.Println("Generating mcpproxy CA certificate...")

		// Create directory if it doesn't exist
		if err := os.MkdirAll(certsDir, 0755); err != nil {
			return fmt.Errorf("failed to create certificates directory: %w", err)
		}

		// Generate certificate
		opts := tlslocal.Options{
			Dir:               certsDir,
			RequireClientCert: cfg.TLS.RequireClientCert,
		}

		_, err := tlslocal.EnsureServerTLSConfig(opts)
		if err != nil {
			return fmt.Errorf("failed to generate certificate: %w", err)
		}

		fmt.Printf("✅ Certificate generated at %s\n", caCertPath)
	}

	// Verify certificate exists
	if _, err := os.Stat(caCertPath); err != nil {
		return fmt.Errorf("certificate file not found: %s", caCertPath)
	}

	// Show information and get confirmation
	if !trustCertForce {
		fmt.Println("\n🔐 Installing mcpproxy CA certificate")
		fmt.Println("=====================================")
		fmt.Printf("Certificate: %s\n", caCertPath)
		fmt.Printf("Target: %s keychain\n", trustCertKeychain)
		fmt.Println("\nThis will:")
		fmt.Println("• Add the mcpproxy CA certificate to your keychain")
		fmt.Println("• Allow HTTPS connections to mcpproxy without warnings")
		fmt.Println("• Require your password for keychain access")
		fmt.Println()

		if runtime.GOOS == darwinOS {
			fmt.Println("After installation, you can enable HTTPS:")
			fmt.Println("1. Set environment: export MCPPROXY_TLS_ENABLED=true")
			fmt.Println("2. Or edit config: ~/.mcpproxy/config.json")
			fmt.Println()
			fmt.Println("For Claude Desktop, add to config:")
			fmt.Println(`  "env": {`)
			fmt.Printf(`    "NODE_EXTRA_CA_CERTS": "%s"`, caCertPath)
			fmt.Println(`  }`)
			fmt.Println()
		}

		fmt.Print("Continue? [Y/n]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "" && response != "y" && response != "yes" {
			fmt.Println("Installation cancelled.")
			return nil
		}
	}

	// Install certificate based on OS
	switch runtime.GOOS {
	case darwinOS:
		return installCertificateMacOS(caCertPath, trustCertKeychain)
	case "linux":
		return installCertificateLinux(caCertPath)
	case "windows":
		return installCertificateWindows(caCertPath)
	default:
		return fmt.Errorf("certificate installation not supported on %s", runtime.GOOS)
	}
}

func installCertificateMacOS(certPath, keychain string) error {
	fmt.Println("\n⏳ Installing certificate to macOS keychain...")

	var keychainPath string
	switch keychain {
	case "system":
		keychainPath = "/Library/Keychains/System.keychain"
	case "login":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		keychainPath = filepath.Join(homeDir, "Library", "Keychains", "login.keychain-db")
	default:
		return fmt.Errorf("invalid keychain: %s (must be 'system' or 'login')", keychain)
	}

	// Try the full add-trusted-cert command first
	trustArgs := []string{
		"add-trusted-cert",
		"-d",                // Add to default trust domain
		"-r", "trustAsRoot", // Trust as root certificate
		"-p", "ssl", // For SSL/TLS usage
		"-p", "basic", // For basic certificate policies
		"-k", keychainPath, // Target keychain
		certPath, // Certificate file
	}

	trustCmd := exec.Command("security", trustArgs...)

	// Capture stderr to check for specific errors
	var stderr strings.Builder
	trustCmd.Stderr = &stderr

	if err := trustCmd.Run(); err != nil {
		stderrStr := stderr.String()

		// If it's already installed or trust settings issue, try simpler approach
		if strings.Contains(stderrStr, "already in") ||
			strings.Contains(stderrStr, "SecTrustSettingsSetTrustSettings") {

			fmt.Println("Certificate already exists, adding with basic trust...")

			// First add the certificate (if not already there)
			addArgs := []string{"add-cert", "-k", keychainPath, certPath}
			addCmd := exec.Command("security", addArgs...)

			var addStderr strings.Builder
			addCmd.Stderr = &addStderr

			if err := addCmd.Run(); err != nil {
				addStderrStr := addStderr.String()
				// Ignore "already in keychain" errors
				if !strings.Contains(addStderrStr, "already in") {
					return fmt.Errorf("failed to add certificate: %w\nError details: %s", err, addStderrStr)
				}
				fmt.Println("Certificate already in keychain.")
			}

			// Then try to set trust settings separately
			// This might fail on some systems, but we'll continue
			trustSettingsArgs := []string{
				"set-trust-settings",
				"-t", "unspecified", // Use unspecified trust (user can modify)
				"-k", keychainPath,
				certPath,
			}

			trustSettingsCmd := exec.Command("security", trustSettingsArgs...)
			if err := trustSettingsCmd.Run(); err != nil {
				fmt.Println("⚠️  Certificate added but couldn't set trust automatically.")
				fmt.Println("   You may need to manually trust it in Keychain Access.")
			}
		} else {
			return fmt.Errorf("failed to install certificate: %w\nError details: %s", err, stderrStr)
		}
	}

	// Verify installation
	fmt.Println("\n🔍 Verifying certificate installation...")
	verifyCmd := exec.Command("security", "verify-cert", "-c", certPath)
	if err := verifyCmd.Run(); err != nil {
		fmt.Println("⚠️  Certificate installed but verification failed. It should still work for mcpproxy.")
	} else {
		fmt.Println("✅ Certificate verification successful!")
	}

	fmt.Println("\n🎉 Certificate installation complete!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("1. Enable HTTPS: export MCPPROXY_TLS_ENABLED=true")
	fmt.Println("2. Start mcpproxy: mcpproxy serve")
	fmt.Println("3. Access via: https://localhost:8080")

	return nil
}

func installCertificateLinux(certPath string) error {
	fmt.Println("\n⏳ Installing certificate to Linux system...")

	// Copy to system certificate directory
	systemCertDir := "/usr/local/share/ca-certificates"
	targetPath := filepath.Join(systemCertDir, "mcpproxy-ca.crt")

	// Copy certificate
	cpCmd := exec.Command("sudo", "cp", certPath, targetPath)
	cpCmd.Stdout = os.Stdout
	cpCmd.Stderr = os.Stderr

	if err := cpCmd.Run(); err != nil {
		return fmt.Errorf("failed to copy certificate: %w", err)
	}

	// Update CA certificates
	updateCmd := exec.Command("sudo", "update-ca-certificates")
	updateCmd.Stdout = os.Stdout
	updateCmd.Stderr = os.Stderr

	if err := updateCmd.Run(); err != nil {
		return fmt.Errorf("failed to update CA certificates: %w", err)
	}

	fmt.Println("✅ Certificate installation complete!")
	return nil
}

func installCertificateWindows(certPath string) error {
	fmt.Println("\n⏳ Installing certificate to Windows certificate store...")

	// Use certlm.msc to install to Local Machine store
	cmd := exec.Command("certlm.msc", "-addstore", "Root", certPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Try alternative method using PowerShell
		psCmd := fmt.Sprintf(`Import-Certificate -FilePath %q -CertStoreLocation Cert:\LocalMachine\Root`, certPath)
		powershellCmd := exec.Command("powershell", "-Command", psCmd)
		powershellCmd.Stdout = os.Stdout
		powershellCmd.Stderr = os.Stderr

		if err := powershellCmd.Run(); err != nil {
			return fmt.Errorf("failed to install certificate: %w", err)
		}
	}

	fmt.Println("✅ Certificate installation complete!")
	return nil
}

// GetTrustCertCommand returns the trust-cert command for adding to the root command
func GetTrustCertCommand() *cobra.Command {
	return trustCertCmd
}
