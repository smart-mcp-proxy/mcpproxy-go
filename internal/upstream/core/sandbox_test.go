package core

import (
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestBuildSandboxSpec_WriteAllowlistDefaults(t *testing.T) {
	work := t.TempDir()
	spec := buildSandboxSpec(&config.ServerConfig{Name: "srv", WorkingDir: work})

	// Reads stay broad: a single read-only "/" root.
	if len(spec.ReadOnlyPaths) != 1 || spec.ReadOnlyPaths[0] != "/" {
		t.Errorf("ReadOnlyPaths = %v, want [\"/\"]", spec.ReadOnlyPaths)
	}
	// Writes are allowlisted: working dir + OS temp must be present.
	if !contains(spec.ReadWritePaths, work) {
		t.Errorf("ReadWritePaths %v missing working dir %q", spec.ReadWritePaths, work)
	}
	if !contains(spec.ReadWritePaths, os.TempDir()) {
		t.Errorf("ReadWritePaths %v missing temp dir %q", spec.ReadWritePaths, os.TempDir())
	}
	// Degrade-gracefully flag is set so a Landlock-less kernel doesn't hard-fail.
	if !spec.BestEffort {
		t.Errorf("BestEffort = false, want true (graceful fallback)")
	}
}

func TestBuildSandboxSpec_NoWorkingDir(t *testing.T) {
	spec := buildSandboxSpec(&config.ServerConfig{Name: "srv"})
	if len(spec.ReadWritePaths) == 0 || !contains(spec.ReadWritePaths, os.TempDir()) {
		t.Errorf("ReadWritePaths %v should include temp dir even without a working dir", spec.ReadWritePaths)
	}
	// A working dir that was never set must not sneak an empty path into the
	// allowlist (an empty Landlock path would be a silent no-op at best).
	if contains(spec.ReadWritePaths, "") {
		t.Errorf("ReadWritePaths %v contains an empty path", spec.ReadWritePaths)
	}
}

// GH #1704: the default ruleset grants "/" read-only, so without an explicit
// grant a confined shell cannot open /dev/null for writing — `2>/dev/null`
// fails with EACCES, which breaks launch scripts and the runtimes' own
// stdio-to-devnull plumbing. The grant must be the device itself, not /dev.
func TestBuildSandboxSpec_DevNullWritable(t *testing.T) {
	spec := buildSandboxSpec(&config.ServerConfig{Name: "srv"})
	if !contains(spec.ReadWritePaths, "/dev/null") {
		t.Errorf("ReadWritePaths %v missing /dev/null", spec.ReadWritePaths)
	}
	if contains(spec.ReadWritePaths, "/dev") {
		t.Errorf("ReadWritePaths %v grants all of /dev; only /dev/null is needed", spec.ReadWritePaths)
	}
}
