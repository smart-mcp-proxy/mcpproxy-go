package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// fakeReleaseSource serves fixture release metadata without touching GitHub.
type fakeReleaseSource struct {
	latest    *updatecheck.GitHubRelease
	byTag     map[string]*updatecheck.GitHubRelease
	latestErr error
	tagErr    error

	latestCalls int
	tagCalls    []string
}

func (f *fakeReleaseSource) Latest(_ bool) (*updatecheck.GitHubRelease, error) {
	f.latestCalls++
	if f.latestErr != nil {
		return nil, f.latestErr
	}
	return f.latest, nil
}

func (f *fakeReleaseSource) ByTag(tag string) (*updatecheck.GitHubRelease, error) {
	f.tagCalls = append(f.tagCalls, tag)
	if f.tagErr != nil {
		return nil, f.tagErr
	}
	if rel, ok := f.byTag[tag]; ok {
		return rel, nil
	}
	return nil, errNotFound{tag}
}

type errNotFound struct{ tag string }

func (e errNotFound) Error() string { return "release " + e.tag + " not found" }

func fixtureRelease(tag string, prerelease bool) *updatecheck.GitHubRelease {
	return &updatecheck.GitHubRelease{
		TagName:    tag,
		Prerelease: prerelease,
		HTMLURL:    "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/" + tag,
	}
}

// newTestRunner builds a runner whose every side effect is either injected or
// confined to t.TempDir(). Self-update would fail loudly if a branch reached
// it unexpectedly, which is exactly what the guidance-only cases must prove.
func newTestRunner(t *testing.T, channel string, flags updateFlags, src releaseSource) (*updateRunner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	// Pin CI so the behavior does not differ between a laptop and CI.
	t.Setenv("CI", "")

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &updateRunner{
		out:             out,
		errOut:          errOut,
		format:          "json",
		currentVersion:  "v0.50.0",
		channel:         channel,
		execPath:        filepath.Join(t.TempDir(), "mcpproxy"),
		flags:           flags,
		releases:        src,
		cosignAvailable: func() bool { return true },
		cosignVerify:    func(_, _ string) error { return nil },
		verifyInstalled: func(_, _ string) error { return nil },
	}, out, errOut
}

func decodeReport(t *testing.T, out *bytes.Buffer) updateReport {
	t.Helper()
	var report updateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	return report
}

// Spec 092 FR-020: every channel gets exactly one correct behavior, and only a
// positively identified tarball install (or an explicit --self assertion on
// unknown) may self-replace the binary.
func TestUpdateCommand_ChannelBranches(t *testing.T) {
	tests := []struct {
		name        string
		channel     string
		self        bool
		wantAction  string
		wantCommand string
		guidanceHas string
	}{
		{
			name:        "homebrew prints the brew command",
			channel:     updatecheck.ChannelHomebrew,
			wantAction:  actionCommand,
			wantCommand: "brew upgrade mcpproxy",
		},
		{
			name:        "deb prints the apt command",
			channel:     updatecheck.ChannelDeb,
			wantAction:  actionCommand,
			wantCommand: "sudo apt update && sudo apt install --only-upgrade mcpproxy",
		},
		{
			name:        "rpm prints the dnf command",
			channel:     updatecheck.ChannelRPM,
			wantAction:  actionCommand,
			wantCommand: "sudo dnf upgrade mcpproxy",
		},
		{
			name:        "go-install prints the go install command",
			channel:     updatecheck.ChannelGoInstall,
			wantAction:  actionCommand,
			wantCommand: "go install github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy@latest",
		},
		{
			name:        "docker gets guidance only",
			channel:     updatecheck.ChannelDocker,
			wantAction:  actionGuidance,
			guidanceHas: "image",
		},
		{
			name:        "windows installer gets guidance only",
			channel:     updatecheck.ChannelWindowsInstaller,
			wantAction:  actionGuidance,
			guidanceHas: "Windows installer",
		},
		{
			name:        "dmg is delegated to the tray updater",
			channel:     updatecheck.ChannelDMG,
			wantAction:  actionTray,
			guidanceHas: "Check for Updates",
		},
		{
			name:        "tarball self-updates",
			channel:     updatecheck.ChannelTarball,
			wantAction:  actionSelfUpdate,
			wantCommand: "mcpproxy update",
		},
		{
			name:        "unknown stays guidance-only without --self",
			channel:     updatecheck.ChannelUnknown,
			wantAction:  actionGuidance,
			guidanceHas: "--self",
		},
		{
			name:        "unknown with --self becomes self-managed",
			channel:     updatecheck.ChannelUnknown,
			self:        true,
			wantAction:  actionSelfUpdate,
			wantCommand: "mcpproxy update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0", false)}
			// --check keeps the self-update branches side-effect free while
			// still exercising the full decision table.
			runner, out, _ := newTestRunner(t, tt.channel, updateFlags{checkOnly: true, self: tt.self}, src)

			if err := runner.run(); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			report := decodeReport(t, out)

			if report.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", report.Action, tt.wantAction)
			}
			if tt.wantCommand != "" && report.Command != tt.wantCommand {
				t.Errorf("command = %q, want %q", report.Command, tt.wantCommand)
			}
			if tt.guidanceHas != "" && !strings.Contains(report.Guidance, tt.guidanceHas) {
				t.Errorf("guidance %q does not mention %q", report.Guidance, tt.guidanceHas)
			}
			if !report.UpdateAvailable {
				t.Errorf("update_available = false, want true for v0.50.0 -> v0.60.0")
			}
			if report.Applied {
				t.Errorf("--check must never apply anything")
			}
		})
	}
}

// Guidance channels must exit 0 having changed nothing even without --check:
// a Homebrew user typing `mcpproxy update` gets the command, not a rewrite of
// a brew-owned binary (SC-005).
func TestUpdateCommand_GuidanceChannelsNeverSelfUpdate(t *testing.T) {
	for _, channel := range []string{
		updatecheck.ChannelHomebrew, updatecheck.ChannelDeb, updatecheck.ChannelRPM,
		updatecheck.ChannelGoInstall, updatecheck.ChannelDocker,
		updatecheck.ChannelWindowsInstaller, updatecheck.ChannelDMG, updatecheck.ChannelUnknown,
	} {
		t.Run(channel, func(t *testing.T) {
			src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0", false)}
			runner, out, _ := newTestRunner(t, channel, updateFlags{}, src)
			// Any attempt to download would panic on the nil http client.
			runner.httpClient = nil

			if err := runner.run(); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			report := decodeReport(t, out)
			if report.Applied {
				t.Errorf("channel %s must not modify anything", channel)
			}
			if report.Action == actionSelfUpdate {
				t.Errorf("channel %s must not resolve to self-update", channel)
			}
		})
	}
}

// FR-022: a bare "update to latest" has no downgrade to force; a downgrade
// needs BOTH --version and --force.
func TestUpdateCommand_DowngradeMatrix(t *testing.T) {
	tests := []struct {
		name          string
		targetVersion string
		force         bool
		latest        string
		wantErr       bool
		wantErrHas    string
		wantApplied   bool
	}{
		{
			name:   "latest is newer: applies",
			latest: "v0.60.0", wantApplied: true,
		},
		{
			name:   "latest equals current: reports up to date, no error",
			latest: "v0.50.0",
		},
		{
			name:   "latest is older: reports up to date, no error",
			latest: "v0.40.0",
		},
		{
			name: "explicit older version without --force is refused",
			// The user named a version, so silence would be wrong: this is a
			// deliberate downgrade attempt and must be rejected loudly.
			targetVersion: "v0.40.0", latest: "v0.60.0",
			wantErr: true, wantErrHas: "--force",
		},
		{
			name:          "explicit equal version without --force is refused",
			targetVersion: "v0.50.0", latest: "v0.60.0",
			wantErr: true, wantErrHas: "not newer",
		},
		{
			name:          "explicit older version with --force downgrades",
			targetVersion: "v0.40.0", force: true, latest: "v0.60.0",
			wantApplied: true,
		},
		{
			name:  "--force alone does not downgrade to latest",
			force: true, latest: "v0.40.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeReleaseSource{
				latest: fixtureRelease(tt.latest, false),
				byTag: map[string]*updatecheck.GitHubRelease{
					"v0.40.0": fixtureRelease("v0.40.0", false),
					"v0.50.0": fixtureRelease("v0.50.0", false),
					"v0.60.0": fixtureRelease("v0.60.0", false),
				},
			}
			flags := updateFlags{targetVersion: tt.targetVersion, force: tt.force}
			runner, out, _ := newTestRunner(t, updatecheck.ChannelTarball, flags, src)

			applied := false
			runner.selfUpdateFn = func(_ *updatecheck.GitHubRelease) error {
				applied = true
				return nil
			}

			err := runner.run()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none (output: %s)", out.String())
				}
				if tt.wantErrHas != "" && !strings.Contains(err.Error(), tt.wantErrHas) {
					t.Errorf("error %q does not mention %q", err, tt.wantErrHas)
				}
				if applied {
					t.Errorf("a refused downgrade must not install anything")
				}
				return
			}
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if applied != tt.wantApplied {
				t.Errorf("applied = %v, want %v (output: %s)", applied, tt.wantApplied, out.String())
			}
		})
	}
}

// FR-022: nothing inside a macOS app bundle (or its staged copy) is ever
// modified, even when the build marker claims a tarball install.
func TestUpdateCommand_RefusesAppBundlePaths(t *testing.T) {
	paths := []string{
		"/Applications/MCPProxy.app/Contents/Resources/bin/mcpproxy",
		"/Applications/MCPProxy.app/Contents/MacOS/mcpproxy",
		"/Users/someone/Library/Application Support/mcpproxy/bin/mcpproxy",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0", false)}
			runner, out, _ := newTestRunner(t, updatecheck.ChannelTarball, updateFlags{}, src)
			runner.execPath = path
			runner.selfUpdateFn = func(_ *updatecheck.GitHubRelease) error {
				t.Fatalf("self-update must never run for %s", path)
				return nil
			}

			if err := runner.run(); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			report := decodeReport(t, out)
			if report.Channel != updatecheck.ChannelDMG {
				t.Errorf("channel = %q, want %q for an app-bundle path", report.Channel, updatecheck.ChannelDMG)
			}
			if report.Action != actionTray {
				t.Errorf("action = %q, want %q", report.Action, actionTray)
			}
		})
	}
}

// FR-020: `--self` lets a user assert ownership of an install mcpproxy could
// not identify, and the requirement attaches "with a clear warning" to that
// override. The warning goes to stderr so it never contaminates `-o json`.
// A positively identified tarball install is not an assertion and must stay
// silent.
func TestUpdateCommand_SelfOverrideWarnsOnUnknownChannel(t *testing.T) {
	tests := []struct {
		name     string
		channel  string
		self     bool
		wantWarn bool
	}{
		{name: "unknown with --self warns", channel: updatecheck.ChannelUnknown, self: true, wantWarn: true},
		{name: "tarball does not warn", channel: updatecheck.ChannelTarball, wantWarn: false},
		{name: "tarball with --self does not warn", channel: updatecheck.ChannelTarball, self: true, wantWarn: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0", false)}
			runner, out, errOut := newTestRunner(t, tt.channel, updateFlags{self: tt.self}, src)
			applied := false
			runner.selfUpdateFn = func(_ *updatecheck.GitHubRelease) error {
				applied = true
				return nil
			}

			if err := runner.run(); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if !applied {
				t.Fatalf("expected the self-update branch to run for channel %q (self=%v)", tt.channel, tt.self)
			}

			warned := strings.Contains(errOut.String(), "WARNING: --self")
			if warned != tt.wantWarn {
				t.Errorf("stderr warning = %v, want %v (stderr: %q)", warned, tt.wantWarn, errOut.String())
			}
			// The warning must never leak into the machine-readable report.
			if strings.Contains(out.String(), "WARNING") {
				t.Errorf("the JSON report must stay free of the warning: %s", out.String())
			}
		})
	}
}

// Even if the decision table were bypassed, selfUpdate itself refuses bundle
// paths (defence in depth for FR-022).
func TestSelfUpdate_RefusesBundlePathDirectly(t *testing.T) {
	src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0", false)}
	runner, _, _ := newTestRunner(t, updatecheck.ChannelTarball, updateFlags{}, src)
	runner.execPath = "/Applications/MCPProxy.app/Contents/Resources/bin/mcpproxy"

	err := runner.selfUpdate(fixtureRelease("v0.60.0", false))
	if err == nil || !strings.Contains(err.Error(), "app bundle") {
		t.Fatalf("selfUpdate error = %v, want a refusal mentioning the app bundle", err)
	}
}

func TestInsideAppBundle(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Applications/MCPProxy.app/Contents/MacOS/mcpproxy", true},
		{"/Applications/MCPProxy.app/Contents/Resources/bin/mcpproxy", true},
		{"/Users/u/Library/Application Support/mcpproxy/bin/mcpproxy", true},
		{"/usr/local/bin/mcpproxy", false},
		{"/home/u/.local/bin/mcpproxy", false},
		// A directory that merely mentions the name is not a bundle.
		{"/home/u/MCPProxy.app-notes/mcpproxy", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := insideAppBundle(tt.path); got != tt.want {
			t.Errorf("insideAppBundle(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// A prerelease offered on a package-manager channel has no command that would
// actually deliver it (brew/apt/dnf serve stable), so the command degrades to
// guidance rather than printing something that would not work.
func TestUpdateCommand_PrereleaseOnPackageChannelFallsBackToGuidance(t *testing.T) {
	src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0-rc.1", true)}
	runner, out, _ := newTestRunner(t, updatecheck.ChannelHomebrew, updateFlags{checkOnly: true}, src)

	if err := runner.run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	report := decodeReport(t, out)
	if report.Action != actionGuidance {
		t.Errorf("action = %q, want %q", report.Action, actionGuidance)
	}
	if report.Command != "" {
		t.Errorf("command = %q, want empty for a prerelease on homebrew", report.Command)
	}
}

// go-install can pin the exact prerelease, so it keeps a command.
func TestUpdateCommand_PrereleaseOnGoInstallPinsVersion(t *testing.T) {
	src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0-rc.1", true)}
	runner, out, _ := newTestRunner(t, updatecheck.ChannelGoInstall, updateFlags{checkOnly: true}, src)

	if err := runner.run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	report := decodeReport(t, out)
	if !strings.HasSuffix(report.Command, "@v0.60.0-rc.1") {
		t.Errorf("command = %q, want a version-pinned go install", report.Command)
	}
}

// FR-006: SemVer precedence, including numeric prerelease identifiers.
func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.50.0", "v0.51.0", true},
		{"v0.50.0", "v0.50.0", false},
		{"v0.51.0", "v0.50.0", false},
		{"0.50.0", "0.51.0", true},
		// Lexicographic comparison would get this backwards.
		{"v0.50.0-rc.2", "v0.50.0-rc.10", true},
		{"v0.50.0-rc.10", "v0.50.0-rc.2", false},
		{"v0.50.0-rc.1", "v0.50.0", true},
		// A dev build is never "older": offering a release would be a guess.
		{"development", "v0.51.0", false},
		{"v0.50.0", "not-a-version", false},
	}
	for _, tt := range tests {
		if got := isNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := map[string]string{
		"v1.2.3":      "v1.2.3",
		"1.2.3":       "v1.2.3",
		" 1.2.3 ":     "v1.2.3",
		"development": "development", // must not become "vdevelopment"
		"":            "",
	}
	for in, want := range tests {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArchiveAssetName(t *testing.T) {
	tests := []struct {
		version, goos, goarch, want string
	}{
		{"v0.55.0", "darwin", "arm64", "mcpproxy-0.55.0-darwin-arm64.tar.gz"},
		{"0.55.0", "linux", "amd64", "mcpproxy-0.55.0-linux-amd64.tar.gz"},
		{"v0.55.0", "windows", "amd64", "mcpproxy-0.55.0-windows-amd64.zip"},
	}
	for _, tt := range tests {
		if got := archiveAssetName(tt.version, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("archiveAssetName(%q,%q,%q) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestCoreBinaryName(t *testing.T) {
	want := "mcpproxy"
	if Edition == "server" {
		want = "mcpproxy-server"
	}
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got := coreBinaryName(); got != want {
		t.Errorf("coreBinaryName() = %q, want %q", got, want)
	}
}

// A development build must not be reported as "already up to date" — that
// reads as "you are current" when in fact no comparison was possible.
func TestUpdateCommand_DevBuildIsExplicit(t *testing.T) {
	src := &fakeReleaseSource{latest: fixtureRelease("v0.60.0", false)}
	runner, out, _ := newTestRunner(t, updatecheck.ChannelUnknown, updateFlags{}, src)
	runner.currentVersion = "development"
	runner.selfUpdateFn = func(_ *updatecheck.GitHubRelease) error {
		t.Fatal("a dev build must not trigger self-update")
		return nil
	}

	if err := runner.run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	report := decodeReport(t, out)
	if report.CurrentVersion != "development" {
		t.Errorf("current_version = %q, want %q", report.CurrentVersion, "development")
	}
	if !strings.Contains(report.Message, "development build") {
		t.Errorf("message %q should explain the dev build", report.Message)
	}
	if report.UpdateAvailable {
		t.Errorf("a dev build must not be reported as having an update available")
	}
}

// --version resolves the exact tag rather than the latest release.
func TestUpdateCommand_ExplicitVersionUsesTagLookup(t *testing.T) {
	src := &fakeReleaseSource{
		latest: fixtureRelease("v0.60.0", false),
		byTag:  map[string]*updatecheck.GitHubRelease{"v0.55.0": fixtureRelease("v0.55.0", false)},
	}
	runner, out, _ := newTestRunner(t, updatecheck.ChannelTarball, updateFlags{targetVersion: "0.55.0", checkOnly: true}, src)

	if err := runner.run(); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if src.latestCalls != 0 {
		t.Errorf("latest release must not be queried when --version is given")
	}
	if len(src.tagCalls) != 1 || src.tagCalls[0] != "v0.55.0" {
		t.Errorf("tag lookups = %v, want [v0.55.0] (the v prefix is added)", src.tagCalls)
	}
	report := decodeReport(t, out)
	if report.LatestVersion != "v0.55.0" {
		t.Errorf("latest_version = %q, want v0.55.0", report.LatestVersion)
	}
}
