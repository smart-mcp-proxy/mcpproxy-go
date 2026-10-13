package launch

import (
	"os"
	"sync"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"tray marker", "tray", ByTray},
		{"installer marker", "installer", ByInstaller},
		{"unset", "", ByUnknown},
		{"whitespace padded tray", "  tray\n", ByTray},
		{"uppercase tray", "TRAY", ByTray},
		{"mixed case installer", "Installer", ByInstaller},
		{"unknown marker never guesses", "launchd", ByUnknown},
		{"near-miss is not honored", "tray-app", ByUnknown},
		{"whitespace only", "   ", ByUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.raw); got != tt.want {
				t.Errorf("Classify(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLaunchedBy_CapturesEnvOnce(t *testing.T) {
	// Reset the process-wide capture so this test owns it; restore afterwards
	// so a later test in the same binary still sees a clean once.
	t.Cleanup(func() {
		captureOnce = sync.Once{}
		captured = ""
	})
	captureOnce = sync.Once{}
	captured = ""

	t.Setenv(EnvLaunchedBy, "tray")
	if got := LaunchedBy(); got != ByTray {
		t.Fatalf("LaunchedBy() = %q, want %q", got, ByTray)
	}

	// A later mutation of the environment must not rewrite provenance: the
	// value describes how the process started, not what the env says now.
	if err := os.Setenv(EnvLaunchedBy, "installer"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	if got := LaunchedBy(); got != ByTray {
		t.Errorf("LaunchedBy() after env mutation = %q, want the captured %q", got, ByTray)
	}
}

func TestLaunchedBy_UnsetIsUnknown(t *testing.T) {
	t.Cleanup(func() {
		captureOnce = sync.Once{}
		captured = ""
	})
	captureOnce = sync.Once{}
	captured = ""

	t.Setenv(EnvLaunchedBy, "")
	if got := LaunchedBy(); got != ByUnknown {
		t.Errorf("LaunchedBy() = %q, want %q for an unset marker", got, ByUnknown)
	}
}
