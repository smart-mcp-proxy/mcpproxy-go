package diagnostics

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The remediation names a config key by mirroring its literal (this package
// stays free of internal dependencies in non-test code, as detectDockerRuntimeType
// already does). A rename on the config side would otherwise leave the message
// pointing at a key that no longer exists, with the whole suite green.
func TestGitCapableImageKeyMatchesConfig(t *testing.T) {
	if gitCapableImageKey != config.GitCapableImageKey {
		t.Fatalf("mirrored key %q != config.GitCapableImageKey %q — update remediation.go",
			gitCapableImageKey, config.GitCapableImageKey)
	}
}
