package telemetry

import (
	"sync"
	"sync/atomic"
	"testing"
)

// fakeFileProber implements FileProber with a simple set of paths.
type fakeFileProber struct {
	exists map[string]bool
}

func (f *fakeFileProber) Exists(path string) bool {
	if f.exists == nil {
		return false
	}
	return f.exists[path]
}

func newFakeFS(present ...string) *fakeFileProber {
	m := make(map[string]bool, len(present))
	for _, p := range present {
		m[p] = true
	}
	return &fakeFileProber{exists: m}
}

// fakeTTY implements TTYChecker.
type fakeTTY bool

func (f fakeTTY) IsTerminal() bool { return bool(f) }

// TestDetectEnvKind_DecisionTree exercises every branch of the decision tree
// from research.md R1 / design §4.2. Each row injects a fake env map, file
// prober, OS name, and TTY checker.
func TestDetectEnvKind_DecisionTree(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		fs          *fakeFileProber
		osName      string
		tty         TTYChecker
		wantKind    EnvKind
		wantMarkers EnvMarkers
	}{
		{
			name:        "interactive-mac",
			env:         map[string]string{},
			fs:          newFakeFS(),
			osName:      "darwin",
			tty:         fakeTTY(true),
			wantKind:    EnvKindInteractive,
			wantMarkers: EnvMarkers{HasTTY: true},
		},
		{
			name:     "interactive-windows",
			env:      map[string]string{},
			fs:       newFakeFS(),
			osName:   "windows",
			tty:      fakeTTY(false),
			wantKind: EnvKindInteractive,
		},
		{
			name:        "interactive-linux-tty",
			env:         map[string]string{},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(true),
			wantKind:    EnvKindInteractive,
			wantMarkers: EnvMarkers{HasTTY: true},
		},
		{
			name:        "interactive-linux-display",
			env:         map[string]string{"DISPLAY": ":0"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindInteractive,
			wantMarkers: EnvMarkers{HasDisplay: true},
		},
		{
			name:        "interactive-linux-wayland",
			env:         map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindInteractive,
			wantMarkers: EnvMarkers{HasDisplay: true},
		},
		{
			name:        "ci-github",
			env:         map[string]string{"GITHUB_ACTIONS": "true"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCI,
			wantMarkers: EnvMarkers{HasCIEnv: true},
		},
		{
			name:        "ci-gitlab",
			env:         map[string]string{"GITLAB_CI": "true"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCI,
			wantMarkers: EnvMarkers{HasCIEnv: true},
		},
		{
			name:        "ci-jenkins",
			env:         map[string]string{"JENKINS_URL": "https://example"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCI,
			wantMarkers: EnvMarkers{HasCIEnv: true},
		},
		{
			name:        "ci-generic",
			env:         map[string]string{"CI": "true"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCI,
			wantMarkers: EnvMarkers{HasCIEnv: true},
		},
		{
			name:        "ci-beats-container",
			env:         map[string]string{"GITHUB_ACTIONS": "true"},
			fs:          newFakeFS("/.dockerenv"),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCI, // CI precedence over container
			wantMarkers: EnvMarkers{HasCIEnv: true, IsContainer: true},
		},
		{
			name:        "cloud-ide-codespaces",
			env:         map[string]string{"CODESPACES": "true"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCloudIDE,
			wantMarkers: EnvMarkers{HasCloudIDEEnv: true},
		},
		{
			// Gemini P1: GitHub Codespaces sets CI=true alongside CODESPACES.
			// Must resolve to cloud_ide, NOT ci, because it's a real human in
			// an interactive ephemeral session.
			name:        "cloud-ide-codespaces-beats-ci",
			env:         map[string]string{"CI": "true", "CODESPACES": "true"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCloudIDE,
			wantMarkers: EnvMarkers{HasCIEnv: true, HasCloudIDEEnv: true},
		},
		{
			// Gemini P1: Gitpod prebuilds run with CI=true but the workspace ID
			// is present — still a cloud_ide session, not a CI runner.
			name:        "cloud-ide-gitpod-prebuild-beats-ci",
			env:         map[string]string{"CI": "true", "GITPOD_WORKSPACE_ID": "abc"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCloudIDE,
			wantMarkers: EnvMarkers{HasCIEnv: true, HasCloudIDEEnv: true},
		},
		{
			name:        "cloud-ide-gitpod",
			env:         map[string]string{"GITPOD_WORKSPACE_ID": "abc"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCloudIDE,
			wantMarkers: EnvMarkers{HasCloudIDEEnv: true},
		},
		{
			name:        "cloud-ide-repl",
			env:         map[string]string{"REPL_ID": "abc"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindCloudIDE,
			wantMarkers: EnvMarkers{HasCloudIDEEnv: true},
		},
		{
			name:        "container-dockerenv",
			env:         map[string]string{},
			fs:          newFakeFS("/.dockerenv"),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindContainer,
			wantMarkers: EnvMarkers{IsContainer: true},
		},
		{
			name:        "container-containerenv",
			env:         map[string]string{},
			fs:          newFakeFS("/run/.containerenv"),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindContainer,
			wantMarkers: EnvMarkers{IsContainer: true},
		},
		{
			name:        "container-envvar",
			env:         map[string]string{"container": "podman"},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindContainer,
			wantMarkers: EnvMarkers{IsContainer: true},
		},
		{
			name:        "headless-linux",
			env:         map[string]string{},
			fs:          newFakeFS(),
			osName:      "linux",
			tty:         fakeTTY(false),
			wantKind:    EnvKindHeadless,
			wantMarkers: EnvMarkers{},
		},
		{
			name:     "unknown-fallback",
			env:      map[string]string{},
			fs:       newFakeFS(),
			osName:   "plan9",
			tty:      fakeTTY(false),
			wantKind: EnvKindUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, markers := DetectEnvKind(tc.env, tc.fs, tc.osName, tc.tty)
			if kind != tc.wantKind {
				t.Errorf("DetectEnvKind() kind = %q, want %q", kind, tc.wantKind)
			}
			if markers != tc.wantMarkers {
				t.Errorf("DetectEnvKind() markers = %+v, want %+v", markers, tc.wantMarkers)
			}
		})
	}
}

// TestDetectEnvKindOnce_ConcurrentCallers verifies that DetectEnvKindOnce
// returns the same value across 100 concurrent goroutines and the underlying
// detector runs at most once per process (simulated via ResetEnvKindForTest +
// a counting fake detector injected via DetectEnvKindOnceWith).
func TestDetectEnvKindOnce_ConcurrentCallers(t *testing.T) {
	ResetEnvKindForTest()
	defer ResetEnvKindForTest()

	var calls atomic.Int32
	detector := func() (EnvKind, EnvMarkers) {
		calls.Add(1)
		return EnvKindCI, EnvMarkers{HasCIEnv: true}
	}

	var wg sync.WaitGroup
	results := make([]EnvKind, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, _ := DetectEnvKindOnceWith(detector)
			results[i] = k
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("detector called %d times, want 1", got)
	}
	for _, r := range results {
		if r != EnvKindCI {
			t.Errorf("concurrent caller got %q, want %q", r, EnvKindCI)
		}
	}
}

// TestDetectEnvKind_EnvMarkersBooleans verifies the booleans reflect the
// observed environment exactly per data-model.md.
func TestDetectEnvKind_EnvMarkersBooleans(t *testing.T) {
	env := map[string]string{
		"CI":                  "true",
		"GITPOD_WORKSPACE_ID": "abc",
		"container":           "docker",
		"DISPLAY":             ":0",
	}
	_, markers := DetectEnvKind(env, newFakeFS("/.dockerenv"), "linux", fakeTTY(true))
	if !markers.HasCIEnv {
		t.Error("expected HasCIEnv=true")
	}
	if !markers.HasCloudIDEEnv {
		t.Error("expected HasCloudIDEEnv=true")
	}
	if !markers.IsContainer {
		t.Error("expected IsContainer=true")
	}
	if !markers.HasTTY {
		t.Error("expected HasTTY=true")
	}
	if !markers.HasDisplay {
		t.Error("expected HasDisplay=true")
	}
}
