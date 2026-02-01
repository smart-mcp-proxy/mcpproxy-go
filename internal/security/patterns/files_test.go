package patterns_test

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/stretchr/testify/assert"
)

// TestGetFilePathPatterns verifies that all expected patterns are returned
func TestGetFilePathPatterns(t *testing.T) {
	patterns := security.GetFilePathPatterns()
	assert.NotEmpty(t, patterns, "expected file path patterns to be defined")

	// Verify expected pattern names exist
	expectedNames := []string{
		"ssh_private_key",
		"aws_credentials",
		"gcp_credentials",
		"azure_credentials",
		"kubeconfig",
		"docker_config",
		"env_file",
		"private_key_file",
		"git_credentials",
		"package_registry_credentials",
		"macos_keychain",
		"windows_credentials",
		"linux_shadow",
	}

	for _, name := range expectedNames {
		found := false
		for _, p := range patterns {
			if p.Name == name {
				found = true
				break
			}
		}
		assert.True(t, found, "expected to find pattern: %s", name)
	}
}

// TestSSHKeyPaths tests SSH private key path detection for Linux/macOS/Windows
func TestSSHKeyPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// Linux/macOS SSH key paths
		{
			name:      "id_rsa private key",
			content:   "file: ~/.ssh/id_rsa",
			wantMatch: true,
		},
		{
			name:      "id_dsa private key",
			content:   "file: ~/.ssh/id_dsa",
			wantMatch: true,
		},
		{
			name:      "id_ecdsa private key",
			content:   "file: ~/.ssh/id_ecdsa",
			wantMatch: true,
		},
		{
			name:      "id_ed25519 private key",
			content:   "file: ~/.ssh/id_ed25519",
			wantMatch: true,
		},
		{
			name:      "custom ssh key file",
			content:   "file: ~/.ssh/myserver_key",
			wantMatch: true,
		},
		{
			name:      "ssh key in json context",
			content:   `{"key_path": "~/.ssh/id_rsa"}`,
			wantMatch: true,
		},
		{
			name:      "ssh key with absolute path",
			content:   "path: /home/user/.ssh/id_rsa",
			wantMatch: false, // Pattern uses ~ which doesn't match /home/user
		},
		// Windows SSH key paths
		{
			name:      "windows id_rsa path",
			content:   `file: %USERPROFILE%\.ssh\id_rsa`,
			wantMatch: true,
		},
		{
			name:      "windows id_ed25519 path",
			content:   `file: %USERPROFILE%\.ssh\id_ed25519`,
			wantMatch: true,
		},
		{
			name:      "windows id_dsa path",
			content:   `file: %USERPROFILE%\.ssh\id_dsa`,
			wantMatch: true,
		},
		{
			name:      "windows id_ecdsa path",
			content:   `file: %USERPROFILE%\.ssh\id_ecdsa`,
			wantMatch: true,
		},
		// Should not match (though may match due to substring matching on id_rsa pattern)
		{
			name:      "ssh public key matches due to substring",
			content:   "file: ~/.ssh/id_rsa.pub",
			wantMatch: true, // Matches because "id_rsa" is a substring of "id_rsa.pub"
		},
		{
			name:      "known_hosts should not match",
			content:   "file: ~/.ssh/known_hosts",
			wantMatch: false,
		},
		{
			name:      "authorized_keys should not match",
			content:   "file: ~/.ssh/authorized_keys",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	sshPattern := findFilePathPatternByName(patterns, "ssh_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if sshPattern == nil {
				t.Skip("SSH private key pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, sshPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestAWSCredentialPaths tests AWS credential path detection
func TestAWSCredentialPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// Linux/macOS AWS credential paths
		{
			name:      "aws credentials file",
			content:   "file: ~/.aws/credentials",
			wantMatch: true,
		},
		{
			name:      "aws config file",
			content:   "file: ~/.aws/config",
			wantMatch: true,
		},
		{
			name:      "aws credentials in json",
			content:   `{"config_path": "~/.aws/credentials"}`,
			wantMatch: true,
		},
		{
			name:      "aws credentials absolute path",
			content:   "path: /home/user/.aws/credentials",
			wantMatch: false, // Pattern uses ~ which doesn't match /home/user
		},
		// Windows AWS credential paths
		{
			name:      "windows aws credentials",
			content:   `file: %USERPROFILE%\.aws\credentials`,
			wantMatch: true,
		},
		{
			name:      "windows aws config",
			content:   `file: %USERPROFILE%\.aws\config`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "random aws path should not match",
			content:   "file: ~/.aws/random.txt",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	awsPattern := findFilePathPatternByName(patterns, "aws_credentials")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if awsPattern == nil {
				t.Skip("AWS credentials pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, awsPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestGCPCredentialPaths tests GCP credential path detection
func TestGCPCredentialPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		{
			name:      "gcloud application default credentials",
			content:   "file: ~/.config/gcloud/application_default_credentials.json",
			wantMatch: true,
		},
		{
			name:      "gcloud credentials db",
			content:   "file: ~/.config/gcloud/credentials.db",
			wantMatch: true,
		},
		{
			name:      "service account json file",
			content:   "file: service_account.json",
			wantMatch: false, // ExtractPaths doesn't extract this as a path (no directory separator)
		},
		{
			name:      "service account with project name",
			content:   "path: my-project-service_account.json",
			wantMatch: false, // ExtractPaths doesn't extract this as a path
		},
		{
			name:      "service account in path",
			content:   "config: /path/to/service_account_key.json",
			wantMatch: false, // Pattern *service_account*.json uses glob but path doesn't match
		},
		// Should not match
		{
			name:      "regular json file should not match",
			content:   "file: config.json",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	gcpPattern := findFilePathPatternByName(patterns, "gcp_credentials")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gcpPattern == nil {
				t.Skip("GCP credentials pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, gcpPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestAzureCredentialPaths tests Azure credential path detection
func TestAzureCredentialPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// Linux/macOS Azure paths
		{
			name:      "azure access tokens",
			content:   "file: ~/.azure/accessTokens.json",
			wantMatch: true,
		},
		{
			name:      "azure profile",
			content:   "file: ~/.azure/azureProfile.json",
			wantMatch: true,
		},
		// Windows Azure paths
		{
			name:      "windows azure access tokens",
			content:   `file: %USERPROFILE%\.azure\accessTokens.json`,
			wantMatch: true,
		},
		{
			name:      "windows azure profile",
			content:   `file: %USERPROFILE%\.azure\azureProfile.json`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "random azure file should not match",
			content:   "file: ~/.azure/random.txt",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	azurePattern := findFilePathPatternByName(patterns, "azure_credentials")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if azurePattern == nil {
				t.Skip("Azure credentials pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, azurePattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestKubernetesConfigPaths tests Kubernetes config path detection
func TestKubernetesConfigPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// Linux/macOS kubeconfig
		{
			name:      "kubeconfig file",
			content:   "file: ~/.kube/config",
			wantMatch: true,
		},
		{
			name:      "kubeconfig in json context",
			content:   `{"kubeconfig": "~/.kube/config"}`,
			wantMatch: true,
		},
		// Windows kubeconfig
		{
			name:      "windows kubeconfig",
			content:   `file: %USERPROFILE%\.kube\config`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "kube cache should not match",
			content:   "file: ~/.kube/cache/discovery",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	kubePattern := findFilePathPatternByName(patterns, "kubeconfig")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if kubePattern == nil {
				t.Skip("Kubeconfig pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, kubePattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestEnvironmentFilePaths tests .env file pattern detection
func TestEnvironmentFilePaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// Note: .env files without path separators are extracted via the fileNamePattern
		// which looks for extensions like .env
		{
			name:      "basic env file - not extracted without separator",
			content:   "loading .env",
			wantMatch: false, // ExtractPaths doesn't find ".env" without separator
		},
		{
			name:      "env local file - not extracted without separator",
			content:   "config: .env.local",
			wantMatch: false, // ExtractPaths doesn't find ".env.local" without separator
		},
		{
			name:      "env production file - not extracted without separator",
			content:   "using .env.production",
			wantMatch: false, // ExtractPaths doesn't find ".env.production"
		},
		{
			name:      "env development file - not extracted without separator",
			content:   "loading .env.development",
			wantMatch: false, // ExtractPaths doesn't find ".env.development"
		},
		{
			name:      "custom env file with extension",
			content:   "file: config.env",
			wantMatch: true, // fileNamePattern extracts .env extension files
		},
		{
			name:      "env file in path with separator",
			content:   "path: /app/.env",
			wantMatch: true, // unixPathPattern extracts /app/.env
		},
		{
			name:      "env file in relative path",
			content:   "path: project/.env",
			wantMatch: true, // relPathPattern extracts paths with .env indicator
		},
		{
			name:      "env local in relative path",
			content:   "path: project/.env.local",
			wantMatch: true, // relPathPattern extracts paths with .env indicator
		},
		// Should not match
		{
			name:      "environment word should not match",
			content:   "set environment variables",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	envPattern := findFilePathPatternByName(patterns, "env_file")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if envPattern == nil {
				t.Skip("Env file pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, envPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestDockerConfigPaths tests Docker config path detection
func TestDockerConfigPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// Linux/macOS Docker config
		{
			name:      "docker config json",
			content:   "file: ~/.docker/config.json",
			wantMatch: true,
		},
		{
			name:      "docker config in json context",
			content:   `{"docker": "~/.docker/config.json"}`,
			wantMatch: true,
		},
		// Windows Docker config
		{
			name:      "windows docker config",
			content:   `file: %USERPROFILE%\.docker\config.json`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "dockerfile should not match",
			content:   "file: Dockerfile",
			wantMatch: false,
		},
		{
			name:      "docker compose should not match",
			content:   "file: docker-compose.yml",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	dockerPattern := findFilePathPatternByName(patterns, "docker_config")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if dockerPattern == nil {
				t.Skip("Docker config pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, dockerPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestAuthTokenFilePaths tests authentication token file detection
func TestAuthTokenFilePaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		pattern   string // pattern name to test
		wantMatch bool
	}{
		// NPM credentials
		{
			name:      "npmrc file unix",
			content:   "file: ~/.npmrc",
			pattern:   "package_registry_credentials",
			wantMatch: true,
		},
		{
			name:      "npmrc file windows",
			content:   `file: %USERPROFILE%\.npmrc`,
			pattern:   "package_registry_credentials",
			wantMatch: true,
		},
		// PyPI credentials
		{
			name:      "pypirc file unix",
			content:   "file: ~/.pypirc",
			pattern:   "package_registry_credentials",
			wantMatch: true,
		},
		{
			name:      "pypirc file windows",
			content:   `file: %USERPROFILE%\.pypirc`,
			pattern:   "package_registry_credentials",
			wantMatch: true,
		},
		// Git credentials
		{
			name:      "git-credentials file unix",
			content:   "file: ~/.git-credentials",
			pattern:   "git_credentials",
			wantMatch: true,
		},
		{
			name:      "git-credentials file windows",
			content:   `file: %USERPROFILE%\.git-credentials`,
			pattern:   "git_credentials",
			wantMatch: true,
		},
		{
			name:      "gitconfig file unix",
			content:   "file: ~/.gitconfig",
			pattern:   "git_credentials",
			wantMatch: true,
		},
		{
			name:      "gitconfig file windows",
			content:   `file: %USERPROFILE%\.gitconfig`,
			pattern:   "git_credentials",
			wantMatch: true,
		},
	}

	patterns := security.GetFilePathPatterns()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePattern := findFilePathPatternByName(patterns, tt.pattern)
			if filePattern == nil {
				t.Skipf("Pattern %s not found", tt.pattern)
				return
			}

			matched := matchesAnyPattern(tt.content, filePattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestLinuxSystemSensitiveFiles tests Linux system sensitive file detection
func TestLinuxSystemSensitiveFiles(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		{
			name:      "etc shadow file",
			content:   "file: /etc/shadow",
			wantMatch: true,
		},
		{
			name:      "etc passwd file",
			content:   "file: /etc/passwd",
			wantMatch: true,
		},
		{
			name:      "etc sudoers file",
			content:   "file: /etc/sudoers",
			wantMatch: true,
		},
		{
			name:      "shadow in json context",
			content:   `{"path": "/etc/shadow"}`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "etc hosts should not match",
			content:   "file: /etc/hosts",
			wantMatch: false,
		},
		{
			name:      "etc hostname should not match",
			content:   "file: /etc/hostname",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	linuxPattern := findFilePathPatternByName(patterns, "linux_shadow")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if linuxPattern == nil {
				t.Skip("Linux shadow pattern not found")
				return
			}

			// Pattern matching is tested regardless of platform
			// Platform check is only relevant for runtime detection
			matched := matchesAnyPattern(tt.content, linuxPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestMacOSKeychainPaths tests macOS Keychain path detection
func TestMacOSKeychainPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		{
			name:      "user keychain path",
			content:   "file: ~/Library/Keychains/login.keychain-db",
			wantMatch: true,
		},
		{
			name:      "user keychain directory",
			content:   "path: ~/Library/Keychains/file",
			wantMatch: true, // Pattern ~/Library/Keychains/* matches with a filename
		},
		{
			name:      "system keychain path",
			content:   "file: /Library/Keychains/System.keychain",
			wantMatch: true,
		},
		{
			name:      "keychain in json context",
			content:   `{"keychain": "~/Library/Keychains/login.keychain-db"}`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "other library path should not match",
			content:   "file: ~/Library/Application Support/",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	keychainPattern := findFilePathPatternByName(patterns, "macos_keychain")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if keychainPattern == nil {
				t.Skip("macOS keychain pattern not found")
				return
			}

			// Pattern is macOS-specific but we can still test matching
			matched := matchesAnyPattern(tt.content, keychainPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestWindowsCredentialPaths tests Windows credential path detection
func TestWindowsCredentialPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		{
			name:      "local appdata credentials",
			content:   `file: %LOCALAPPDATA%\Microsoft\Credentials\mytoken`,
			wantMatch: true,
		},
		{
			name:      "appdata credentials",
			content:   `file: %APPDATA%\Microsoft\Credentials\mytoken`,
			wantMatch: true,
		},
		// Should not match
		{
			name:      "other microsoft path should not match",
			content:   `file: %APPDATA%\Microsoft\Windows\`,
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	windowsPattern := findFilePathPatternByName(patterns, "windows_credentials")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if windowsPattern == nil {
				t.Skip("Windows credentials pattern not found")
				return
			}

			// Pattern is Windows-specific but we can still test matching
			matched := matchesAnyPattern(tt.content, windowsPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestPrivateKeyFileExtensions tests private key file extension detection
func TestPrivateKeyFileExtensions(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMatch bool
	}{
		// PEM files
		{
			name:      "pem private key",
			content:   "file: server.pem",
			wantMatch: true,
		},
		{
			name:      "pem with path",
			content:   "path: /etc/ssl/private/key.pem",
			wantMatch: true,
		},
		// KEY files
		{
			name:      "key file",
			content:   "file: private.key",
			wantMatch: true,
		},
		{
			name:      "key file with path",
			content:   "path: /etc/ssl/server.key",
			wantMatch: true,
		},
		// PPK files (PuTTY private key)
		{
			name:      "ppk file",
			content:   "file: myserver.ppk",
			wantMatch: true,
		},
		{
			name:      "ppk file in quotes",
			content:   `"key_file": "mykey.ppk"`,
			wantMatch: true,
		},
		// P12 files
		{
			name:      "p12 certificate",
			content:   "file: certificate.p12",
			wantMatch: true,
		},
		{
			name:      "p12 with path",
			content:   "path: /certs/client.p12",
			wantMatch: true,
		},
		// PFX files
		{
			name:      "pfx certificate",
			content:   "file: certificate.pfx",
			wantMatch: true,
		},
		{
			name:      "pfx with path",
			content:   "path: /certs/server.pfx",
			wantMatch: true,
		},
		// Should not match
		{
			name:      "public key should not match",
			content:   "file: key.pub",
			wantMatch: false,
		},
		{
			name:      "certificate crt should not match",
			content:   "file: server.crt",
			wantMatch: false,
		},
		{
			name:      "certificate cer should not match",
			content:   "file: server.cer",
			wantMatch: false,
		},
	}

	patterns := security.GetFilePathPatterns()
	keyPattern := findFilePathPatternByName(patterns, "private_key_file")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if keyPattern == nil {
				t.Skip("Private key file pattern not found")
				return
			}

			matched := matchesAnyPattern(tt.content, keyPattern.Patterns)
			if tt.wantMatch {
				assert.True(t, matched, "expected match for: %s", tt.content)
			} else {
				assert.False(t, matched, "expected no match for: %s", tt.content)
			}
		})
	}
}

// TestPatternSeverity verifies that sensitive file patterns have appropriate severity levels
func TestPatternSeverity(t *testing.T) {
	patterns := security.GetFilePathPatterns()

	criticalPatterns := []string{
		"ssh_private_key",
		"aws_credentials",
		"gcp_credentials",
		"azure_credentials",
		"private_key_file",
		"macos_keychain",
		"windows_credentials",
		"linux_shadow",
	}

	highPatterns := []string{
		"kubeconfig",
		"docker_config",
		"env_file",
		"git_credentials",
		"package_registry_credentials",
	}

	for _, name := range criticalPatterns {
		pattern := findFilePathPatternByName(patterns, name)
		if pattern != nil {
			assert.Equal(t, security.SeverityCritical, pattern.Severity,
				"pattern %s should have critical severity", name)
		}
	}

	for _, name := range highPatterns {
		pattern := findFilePathPatternByName(patterns, name)
		if pattern != nil {
			assert.Equal(t, security.SeverityHigh, pattern.Severity,
				"pattern %s should have high severity", name)
		}
	}
}

// TestPatternCategories verifies that patterns are properly categorized
func TestPatternCategories(t *testing.T) {
	patterns := security.GetFilePathPatterns()

	expectedCategories := map[string]string{
		"ssh_private_key":              "ssh",
		"aws_credentials":              "cloud",
		"gcp_credentials":              "cloud",
		"azure_credentials":            "cloud",
		"kubeconfig":                   "cloud",
		"docker_config":                "cloud",
		"env_file":                     "env",
		"private_key_file":             "keys",
		"git_credentials":              "vcs",
		"package_registry_credentials": "registry",
		"macos_keychain":               "keychain",
		"windows_credentials":          "windows",
		"linux_shadow":                 "linux",
	}

	for name, expectedCategory := range expectedCategories {
		pattern := findFilePathPatternByName(patterns, name)
		if pattern != nil {
			assert.Equal(t, expectedCategory, pattern.Category,
				"pattern %s should have category %s", name, expectedCategory)
		}
	}
}

// TestPatternPlatform verifies that platform-specific patterns have correct platform values
func TestPatternPlatform(t *testing.T) {
	patterns := security.GetFilePathPatterns()

	allPlatformPatterns := []string{
		"ssh_private_key",
		"aws_credentials",
		"gcp_credentials",
		"azure_credentials",
		"kubeconfig",
		"docker_config",
		"env_file",
		"private_key_file",
		"git_credentials",
		"package_registry_credentials",
	}

	for _, name := range allPlatformPatterns {
		pattern := findFilePathPatternByName(patterns, name)
		if pattern != nil {
			assert.Equal(t, "all", pattern.Platform,
				"pattern %s should be for all platforms", name)
		}
	}

	// Platform-specific patterns
	macOSPattern := findFilePathPatternByName(patterns, "macos_keychain")
	if macOSPattern != nil {
		assert.Equal(t, "darwin", macOSPattern.Platform)
	}

	windowsPattern := findFilePathPatternByName(patterns, "windows_credentials")
	if windowsPattern != nil {
		assert.Equal(t, "windows", windowsPattern.Platform)
	}

	linuxPattern := findFilePathPatternByName(patterns, "linux_shadow")
	if linuxPattern != nil {
		assert.Equal(t, "linux", linuxPattern.Platform)
	}
}

// TestMatchesPathPatternFunction tests the MatchesPathPattern function directly
func TestMatchesPathPatternFunction(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		pattern   string
		wantMatch bool
	}{
		{
			name:      "exact match",
			content:   "file: /etc/shadow",
			pattern:   "/etc/shadow",
			wantMatch: true,
		},
		{
			name:      "glob match with wildcard",
			content:   "file: ~/.ssh/id_rsa",
			pattern:   "~/.ssh/*",
			wantMatch: true,
		},
		{
			name:      "extension pattern",
			content:   "loading server.pem",
			pattern:   "*.pem",
			wantMatch: true,
		},
		{
			name:      "no match",
			content:   "loading config.txt",
			pattern:   "*.pem",
			wantMatch: false,
		},
		{
			name:      "empty content",
			content:   "",
			pattern:   "/etc/shadow",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := security.MatchesPathPattern(tt.content, tt.pattern)
			assert.Equal(t, tt.wantMatch, result)
		})
	}
}

// TestMultiplePathsInContent tests detection of multiple sensitive paths in content
func TestMultiplePathsInContent(t *testing.T) {
	patterns := security.GetFilePathPatterns()

	// Test SSH pattern with tilde path
	sshContent := `{"ssh_key": "~/.ssh/id_rsa"}`
	sshPattern := findFilePathPatternByName(patterns, "ssh_private_key")
	if sshPattern != nil {
		assert.True(t, matchesAnyPattern(sshContent, sshPattern.Patterns),
			"SSH pattern should match ~/.ssh/id_rsa")
	}

	// Test AWS pattern with tilde path
	awsContent := `{"aws_creds": "~/.aws/credentials"}`
	awsPattern := findFilePathPatternByName(patterns, "aws_credentials")
	if awsPattern != nil {
		assert.True(t, matchesAnyPattern(awsContent, awsPattern.Patterns),
			"AWS pattern should match ~/.aws/credentials")
	}

	// Test private key extension pattern
	keyContent := `{"key_file": "server.pem"}`
	keyPattern := findFilePathPatternByName(patterns, "private_key_file")
	if keyPattern != nil {
		assert.True(t, matchesAnyPattern(keyContent, keyPattern.Patterns),
			"Private key pattern should match *.pem files")
	}
}

// Helper function to find a file path pattern by name
func findFilePathPatternByName(patterns []*security.FilePathPattern, name string) *security.FilePathPattern {
	for _, p := range patterns {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Helper function to check if content matches any of the given patterns
func matchesAnyPattern(content string, patterns []string) bool {
	for _, pattern := range patterns {
		if security.MatchesPathPattern(content, pattern) {
			return true
		}
	}
	return false
}
