package secureenv

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// dockerBundleBinDir is the macOS Docker Desktop app-bundle bin dir that holds
// the docker CLI even when the optional, admin-gated "install CLI tools" step
// was skipped (issue #696).
const dockerBundleBinDir = "/Applications/Docker.app/Contents/Resources/bin"

// TestUnixCandidatePathsIncludesDockerBundleOnMacOS asserts that the macOS
// Docker Desktop app-bundle bin dir is among the candidate spawn-PATH dirs on
// darwin (defense in depth for the absolute-path docker spawn fix), and is not
// injected on other platforms.
func TestUnixCandidatePathsIncludesDockerBundleOnMacOS(t *testing.T) {
	paths := unixCandidatePaths()

	if runtime.GOOS == "darwin" {
		assert.Contains(t, paths, dockerBundleBinDir,
			"macOS candidate paths must include the Docker Desktop bundle bin dir (#696)")
	} else {
		assert.NotContains(t, paths, dockerBundleBinDir,
			"non-macOS candidate paths must not include the macOS-only bundle bin dir")
	}

	// Existing well-known dirs must be preserved regardless of platform.
	assert.Contains(t, paths, "/usr/local/bin")
	assert.Contains(t, paths, "/usr/bin")
}
