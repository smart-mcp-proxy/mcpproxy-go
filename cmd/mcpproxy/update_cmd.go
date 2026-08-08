package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// update_cmd.go implements `mcpproxy update` (Spec 092 US3 / FR-020..FR-023):
// one command that knows how this binary was installed and either prints the
// exact package-manager command, points at the tray updater, or performs a
// verified self-replace — never corrupting a package manager's bookkeeping and
// never touching an app bundle.

// Actions the command can take. They are part of the -o json contract.
const (
	actionUpToDate   = "up-to-date"
	actionCommand    = "command"     // a package manager owns the install
	actionGuidance   = "guidance"    // no safe command exists
	actionTray       = "tray"        // macOS app bundle: the tray owns updates
	actionSelfUpdate = "self-update" // positively identified self-managed install
)

// Cosign identity pinning for checksums.txt (FR-021). These mirror the verify
// recipe documented next to the signing step in each workflow: releases are
// only ever cut from a tag, so the ref is pinned too — a workflow_dispatch run
// on a branch must not be able to sign an artifact this command will install.
//
// Two workflows, because RCs are cut by a separate pipeline (FR-014) and a
// user on the prerelease channel must be able to verify what it publishes.
// Both are tag-gated and both live in this repository, so accepting the second
// one widens the trusted set by nothing a tag push could not already do.
const (
	cosignIdentityRegexp = `^https://github\.com/smart-mcp-proxy/mcpproxy-go/\.github/workflows/(release|prerelease)\.yml@refs/tags/v`
	cosignOIDCIssuer     = "https://token.actions.githubusercontent.com"
)

const (
	checksumsAssetName = "checksums.txt"
	cosignBundleName   = "checksums.txt.cosign.bundle"

	downloadTimeout = 10 * time.Minute
	cosignTimeout   = 2 * time.Minute
)

type updateFlags struct {
	checkOnly       bool
	self            bool
	targetVersion   string
	force           bool
	allowUnverified bool
}

// releaseSource is the release-metadata seam. Tests point it at an httptest
// server; production uses internal/updatecheck's GitHub client.
type releaseSource interface {
	Latest(includePrereleases bool) (*updatecheck.GitHubRelease, error)
	ByTag(tag string) (*updatecheck.GitHubRelease, error)
}

type githubReleaseSource struct {
	client *updatecheck.GitHubClient
}

func (g *githubReleaseSource) Latest(includePrereleases bool) (*updatecheck.GitHubRelease, error) {
	return g.client.GetRelease(includePrereleases)
}

func (g *githubReleaseSource) ByTag(tag string) (*updatecheck.GitHubRelease, error) {
	return g.client.GetReleaseByTag(tag)
}

// updateReport is the machine-readable result of the command (-o json/yaml)
// and the source of the human-readable rendering.
type updateReport struct {
	CurrentVersion  string `json:"current_version" yaml:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty" yaml:"latest_version,omitempty"`
	Channel         string `json:"channel" yaml:"channel"`
	UpdateAvailable bool   `json:"update_available" yaml:"update_available"`
	Action          string `json:"action" yaml:"action"`
	Command         string `json:"command,omitempty" yaml:"command,omitempty"`
	Guidance        string `json:"guidance,omitempty" yaml:"guidance,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty" yaml:"release_url,omitempty"`
	IsPrerelease    bool   `json:"is_prerelease,omitempty" yaml:"is_prerelease,omitempty"`
	Applied         bool   `json:"applied" yaml:"applied"`
	Message         string `json:"message,omitempty" yaml:"message,omitempty"`
}

// updateRunner carries every dependency the command touches so the whole
// decision table is testable without a network, a real install, or cobra.
type updateRunner struct {
	out    io.Writer
	errOut io.Writer
	format string

	currentVersion     string
	channel            string
	execPath           string // symlink-resolved path of the binary to replace
	includePrereleases bool

	flags    updateFlags
	releases releaseSource

	httpClient      *http.Client
	cosignVerify    func(checksumsPath, bundlePath string) error
	verifyInstalled func(path, version string) error
	cosignAvailable func() bool

	// selfUpdateFn performs the download/verify/swap. Indirected so the
	// decision-table tests can assert "this branch would (not) have installed
	// something" without staging a real release.
	selfUpdateFn func(*updatecheck.GitHubRelease) error
}

// GetUpdateCommand returns the `update` subcommand.
func GetUpdateCommand() *cobra.Command {
	flags := updateFlags{}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update MCPProxy, or show how to update it for your install channel",
		Long: `Check for a newer MCPProxy release and update it the way your install expects.

MCPProxy detects how it was installed and acts accordingly:
  homebrew / deb / rpm / go-install   prints the exact upgrade command
  docker / windows-installer          prints channel-appropriate guidance
  dmg (macOS app bundle)              points at the tray updater; never touches the bundle
  tarball (official release archive)  downloads, verifies and swaps the binary in place
  unknown                             guidance only, unless you assert ownership with --self

Self-update verifies the release's signed checksum manifest before replacing
anything, keeps the previous binary until the new one runs, and never
escalates privileges.

Examples:
  mcpproxy update --check                     # report current/latest/channel, change nothing
  mcpproxy update                             # do the right thing for this install
  mcpproxy update --self                      # assert a self-managed install on an unknown channel
  mcpproxy update --version v0.54.0 --force   # deliberate downgrade`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := newUpdateRunner(cmd.OutOrStdout(), cmd.ErrOrStderr(), flags)
			if err != nil {
				return err
			}
			return runner.run()
		},
	}

	cmd.Flags().BoolVar(&flags.checkOnly, "check", false, "Report current version, latest version and install channel without changing anything")
	cmd.Flags().BoolVar(&flags.self, "self", false, "Assert that you manage this binary yourself (allows self-update on the unknown channel)")
	cmd.Flags().StringVar(&flags.targetVersion, "version", "", "Install this exact release instead of the latest (required for a downgrade)")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Allow installing a version that is not newer (requires --version)")
	cmd.Flags().BoolVar(&flags.allowUnverified, "allow-unverified-signature", false,
		"Proceed when the release signature cannot be verified (checksum verification still applies)")

	return cmd
}

// newUpdateRunner wires the production dependencies.
func newUpdateRunner(out, errOut io.Writer, flags updateFlags) (*updateRunner, error) {
	version := httpapi.GetBuildVersion()
	channel := updatecheck.DetectChannel(version)

	execPath, err := resolvedExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("cannot determine the running binary's path: %w", err)
	}

	client := updatecheck.NewGitHubClient(zap.NewNop())

	return &updateRunner{
		out:                out,
		errOut:             errOut,
		format:             clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput),
		currentVersion:     version,
		channel:            channel,
		execPath:           execPath,
		includePrereleases: prereleasePreference(),
		flags:              flags,
		releases:           &githubReleaseSource{client: client},
		httpClient:         &http.Client{Timeout: downloadTimeout},
		cosignVerify:       verifyWithCosignBinary,
		verifyInstalled:    verifyInstalledVersion,
		cosignAvailable:    cosignOnPath,
	}, nil
}

// prereleasePreference mirrors the checker's precedence (Spec 079 FR-014):
// MCPPROXY_ALLOW_PRERELEASE_UPDATES wins over update_check.channel, so
// `mcpproxy update` offers exactly what the daemon's nudge offered (FR-023).
func prereleasePreference() bool {
	if os.Getenv(updatecheck.EnvAllowPrereleaseUpdates) == "true" {
		return true
	}
	cfg, err := loadCLIConfig(configFile)
	if err != nil || cfg == nil {
		return false
	}
	return cfg.UpdateCheck.IncludePrereleases()
}

// resolvedExecutablePath returns the symlink-resolved path of the running
// binary. Resolving matters for FR-021: a symlinked launcher
// (~/.local/bin/mcpproxy -> ~/opt/mcpproxy/mcpproxy) must have its destination
// replaced, not the symlink itself.
func resolvedExecutablePath() (string, error) {
	raw, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(raw); err == nil && resolved != "" {
		return resolved, nil
	}
	return raw, nil
}

// insideAppBundle reports whether path lives in (or is a staged copy of) the
// macOS app bundle. FR-022 forbids modifying either, and the CLI must reach
// that conclusion from the path alone — the DMG heuristics in
// internal/updatecheck only run on darwin, while this guard must hold
// everywhere (a bundle copied onto another OS is still not ours to rewrite).
func insideAppBundle(path string) bool {
	if path == "" {
		return false
	}
	normalized := filepath.ToSlash(path)
	return strings.Contains(normalized, "MCPProxy.app/") ||
		strings.HasSuffix(normalized, "/Library/Application Support/mcpproxy/bin/mcpproxy")
}

// effectiveChannel applies the app-bundle override on top of the detected
// channel.
func (r *updateRunner) effectiveChannel() string {
	if insideAppBundle(r.execPath) {
		return updatecheck.ChannelDMG
	}
	return r.channel
}

// decideAction maps a channel to what the command will do (FR-020).
func decideAction(channel string, self bool) string {
	switch channel {
	case updatecheck.ChannelHomebrew, updatecheck.ChannelDeb, updatecheck.ChannelRPM, updatecheck.ChannelGoInstall:
		return actionCommand
	case updatecheck.ChannelDocker, updatecheck.ChannelWindowsInstaller:
		return actionGuidance
	case updatecheck.ChannelDMG:
		return actionTray
	case updatecheck.ChannelTarball:
		return actionSelfUpdate
	case updatecheck.ChannelUnknown:
		// FR-020: writability is not ownership. Only an explicit user
		// assertion turns unknown into a self-managed install.
		if self {
			return actionSelfUpdate
		}
		return actionGuidance
	default:
		return actionGuidance
	}
}

func (r *updateRunner) run() error {
	channel := r.effectiveChannel()

	release, err := r.resolveRelease()
	if err != nil {
		return err
	}

	latest := release.TagName
	newer := isNewer(r.currentVersion, latest)

	report := &updateReport{
		CurrentVersion:  normalizeVersion(r.currentVersion),
		LatestVersion:   normalizeVersion(latest),
		Channel:         channel,
		UpdateAvailable: newer,
		ReleaseURL:      release.HTMLURL,
		IsPrerelease:    release.Prerelease,
	}

	action := decideAction(channel, r.flags.self)
	r.annotateAction(report, action, release)

	// A development build has no place on the release timeline: comparing it
	// would either offer every release as an "update" or silently claim it is
	// current. Say so instead (FR-006: malformed versions are a logged
	// no-decision, not a guess).
	devBuild := !semver.IsValid(normalizeVersion(r.currentVersion))
	if devBuild {
		report.Message = fmt.Sprintf(
			"Running a development build (%s), which cannot be compared to released versions. "+
				"Install a specific release with --version <tag> --force.", report.CurrentVersion)
	}

	// --check never acts; it reports what would happen (FR-023).
	if r.flags.checkOnly {
		if !newer && !devBuild {
			report.Message = "Already up to date."
		}
		return r.emit(report)
	}

	if devBuild && r.flags.targetVersion == "" {
		return r.emit(report)
	}

	// Nothing newer and no explicit target: same output as --check (FR-023).
	if !newer && r.flags.targetVersion == "" {
		report.Action = actionUpToDate
		report.Message = "Already up to date."
		return r.emit(report)
	}

	// An explicit target that is not newer is a downgrade/reinstall and needs
	// --force as well (FR-022).
	if !newer && !r.flags.force {
		return fmt.Errorf("refusing to install %s: it is not newer than the running %s. "+
			"Pass --force together with --version to install it deliberately",
			normalizeVersion(latest), normalizeVersion(r.currentVersion))
	}

	switch action {
	case actionSelfUpdate:
		// FR-020: --self is the user asserting ownership of a binary mcpproxy
		// could NOT positively identify. Say plainly what that assertion means
		// before acting on it — an AUR/MacPorts/Nix-style layout is writable
		// and still owned by a package manager whose bookkeeping this would
		// silently invalidate. Positively identified tarball installs get no
		// warning; there is nothing uncertain about them.
		if channel == updatecheck.ChannelUnknown {
			fmt.Fprintf(r.errOut,
				"WARNING: --self: mcpproxy could not identify how %s was installed and is replacing it "+
					"because you asserted you manage it yourself. If a package manager owns this path, "+
					"its bookkeeping will no longer match what is on disk.\n",
				r.execPath)
		}
		apply := r.selfUpdateFn
		if apply == nil {
			apply = r.selfUpdate
		}
		if err := apply(release); err != nil {
			return err
		}
		report.Applied = true
		report.Message = fmt.Sprintf("Updated %s to %s. Restart any running mcpproxy process to pick it up.",
			r.execPath, normalizeVersion(latest))
		return r.emit(report)
	default:
		// Every other channel is guidance-only and exits 0 having changed
		// nothing (FR-020, SC-005).
		return r.emit(report)
	}
}

// annotateAction fills in the action-specific command/guidance text.
func (r *updateRunner) annotateAction(report *updateReport, action string, release *updatecheck.GitHubRelease) {
	report.Action = action
	switch action {
	case actionCommand:
		// A prerelease is only ever published to the GitHub prerelease
		// channel, so the generic package-manager command would install a
		// different (stable) version than the one advertised — mirror the
		// checker's rule and let it degrade to guidance instead (FR-009).
		if release.Prerelease {
			report.Command = updatecheck.PrereleaseUpdateCommand(report.Channel, release.TagName)
		} else {
			report.Command = updatecheck.UpdateCommand(report.Channel)
		}
		if report.Command == "" {
			// A prerelease on a package-manager channel has no command that
			// would actually deliver it; fall back to guidance.
			report.Action = actionGuidance
			report.Guidance = updatecheck.GuidanceLine(report.Channel, release.HTMLURL)
		}
	case actionGuidance:
		report.Guidance = updatecheck.GuidanceLine(report.Channel, release.HTMLURL)
		if report.Channel == updatecheck.ChannelUnknown && !r.flags.self {
			report.Guidance += ". If you manage this binary yourself (an extracted release archive), " +
				"re-run with --self to let mcpproxy replace it"
		}
	case actionTray:
		report.Guidance = "MCPProxy is installed as a macOS app bundle — use the menu bar app: " +
			"MCPProxy menu -> Check for Updates. `mcpproxy update` never modifies an app bundle " +
			"or its staged copies"
	case actionSelfUpdate:
		report.Command = "mcpproxy update"
	}
}

// resolveRelease picks the release to act on: the explicitly requested tag, or
// the newest one on the selected channel.
func (r *updateRunner) resolveRelease() (*updatecheck.GitHubRelease, error) {
	if r.flags.targetVersion != "" {
		tag := normalizeVersion(r.flags.targetVersion)
		release, err := r.releases.ByTag(tag)
		if err != nil {
			return nil, err
		}
		return release, nil
	}
	release, err := r.releases.Latest(r.includePrereleases)
	if err != nil {
		return nil, fmt.Errorf("could not determine the latest release: %w", err)
	}
	return release, nil
}

// selfUpdate downloads, verifies and installs the release (FR-021).
func (r *updateRunner) selfUpdate(release *updatecheck.GitHubRelease) error {
	target := r.execPath
	if insideAppBundle(target) {
		// Defence in depth: decideAction already routes bundles to the tray.
		return fmt.Errorf("refusing to modify %s: it is inside a macOS app bundle", target)
	}
	// Checked here as well as inside applyNewBinary so a concurrent update is
	// caught before ~90 MB is downloaded on its behalf.
	if aliasErr := refuseAliasedUpdateTarget(target); aliasErr != nil {
		return aliasErr
	}

	// Fail before spending a ~90 MB download on an install we cannot write.
	if err := ensureTargetWritable(target); err != nil {
		return err
	}

	version := normalizeVersion(release.TagName)
	assetName := archiveAssetName(version, runtime.GOOS, runtime.GOARCH)

	archiveURL, err := assetURL(release, assetName)
	if err != nil {
		return err
	}
	checksumsURL, err := assetURL(release, checksumsAssetName)
	if err != nil {
		return fmt.Errorf("release %s publishes no %s, so its artifacts cannot be verified: %w",
			version, checksumsAssetName, err)
	}
	bundleURL, bundleErr := assetURL(release, cosignBundleName)

	workDir, err := os.MkdirTemp("", "mcpproxy-update-*")
	if err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	checksumsPath := filepath.Join(workDir, checksumsAssetName)
	if err := r.download(checksumsURL, checksumsPath); err != nil {
		return err
	}

	// Signature verification of the checksum manifest (FR-021).
	if err := r.verifySignature(workDir, checksumsPath, bundleURL, bundleErr); err != nil {
		return err
	}

	manifest, err := openChecksums(checksumsPath)
	if err != nil {
		return err
	}
	wantDigest, ok := manifest[assetName]
	if !ok {
		return fmt.Errorf("%s is not listed in %s for release %s; refusing to install an unlisted artifact",
			assetName, checksumsAssetName, version)
	}

	archivePath := filepath.Join(workDir, assetName)
	if err := r.download(archiveURL, archivePath); err != nil {
		return err
	}
	if err := verifyFileSHA256(archivePath, wantDigest); err != nil {
		return err
	}

	// Stage inside the target directory: a rename across filesystems fails
	// (EXDEV), and FR-021 requires the swap itself to be a rename.
	stagedPath := filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.new-%d", filepath.Base(target), os.Getpid()))
	_ = os.Remove(stagedPath)
	defer os.Remove(stagedPath)

	if err := extractBinary(archivePath, coreBinaryName(), stagedPath); err != nil {
		return err
	}

	verify := r.verifyInstalled
	if verify == nil {
		verify = verifyInstalledVersion
	}
	return applyNewBinary(target, stagedPath, func(path string) error {
		return verify(path, version)
	})
}

// verifySignature verifies the cosign bundle over checksums.txt.
//
// COMPROMISE (see the report's open decision #5): this shells out to a
// user-installed cosign instead of verifying in-process with sigstore-go.
// sigstore-go v1.3.0 pulls 72 additional linked modules (+16 MB to a
// hello-world binary) and requires bumping go.mod's Go directive — a
// dependency decision the maintainer has not made. Until then: the sha256
// comparison against checksums.txt is unconditional, and signature
// verification is REQUIRED by default — a missing cosign aborts the update
// rather than silently downgrading the trust model. --allow-unverified-
// signature is the explicit, loudly warned opt-out.
func (r *updateRunner) verifySignature(workDir, checksumsPath, bundleURL string, bundleErr error) error {
	if r.flags.allowUnverified {
		fmt.Fprintf(r.errOut,
			"WARNING: --allow-unverified-signature: installing after checksum verification only. "+
				"The release signature was NOT checked, so a compromised %s would not be detected.\n",
			checksumsAssetName)
		return nil
	}

	if bundleErr != nil || bundleURL == "" {
		return fmt.Errorf("release publishes no %s, so its checksums cannot be authenticated. "+
			"Re-run with --allow-unverified-signature to accept checksum-only verification", cosignBundleName)
	}

	available := r.cosignAvailable
	if available == nil {
		available = cosignOnPath
	}
	if !available() {
		return fmt.Errorf("cosign is required to verify the release signature but was not found on PATH.\n" +
			"Install it (https://docs.sigstore.dev/cosign/installation/) and re-run, or accept " +
			"checksum-only verification with --allow-unverified-signature")
	}

	bundlePath := filepath.Join(workDir, cosignBundleName)
	if err := r.download(bundleURL, bundlePath); err != nil {
		return err
	}

	verify := r.cosignVerify
	if verify == nil {
		verify = verifyWithCosignBinary
	}
	if err := verify(checksumsPath, bundlePath); err != nil {
		return fmt.Errorf("release signature verification failed, nothing was installed: %w", err)
	}
	return nil
}

func cosignOnPath() bool {
	_, err := exec.LookPath("cosign")
	return err == nil
}

// verifyWithCosignBinary runs the same verification the release workflow
// documents next to its signing step, with the identity and issuer pinned.
func verifyWithCosignBinary(checksumsPath, bundlePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cosignTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "cosign", "verify-blob", // #nosec G204 -- fixed argv; only file paths this process created vary
		"--bundle", bundlePath,
		"--certificate-identity-regexp", cosignIdentityRegexp,
		"--certificate-oidc-issuer", cosignOIDCIssuer,
		checksumsPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func openChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path) // #nosec G304 -- file this process just downloaded into its own temp dir
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", checksumsAssetName, err)
	}
	defer f.Close()
	return parseChecksums(f)
}

// download fetches url into dest.
func (r *updateRunner) download(url, dest string) error {
	client := r.httpClient
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	resp, err := client.Get(url) // #nosec G107 -- url comes from the GitHub release metadata we just fetched
	if err != nil {
		return fmt.Errorf("download %s: %w", filepath.Base(dest), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: server returned status %d", filepath.Base(dest), resp.StatusCode)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- dest is inside our own temp dir
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	if _, err := io.Copy(out, io.LimitReader(resp.Body, maxArchiveMemberBytes+1)); err != nil {
		out.Close()
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return out.Close()
}

// assetURL returns the download URL of the named release asset.
func assetURL(release *updatecheck.GitHubRelease, name string) (string, error) {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return release.Assets[i].BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("release %s has no asset named %s (this platform may not be published for that release)",
		release.TagName, name)
}

// archiveAssetName builds the release archive filename the pipeline produces:
// mcpproxy-<version-without-v>-<goos>-<goarch>.<tar.gz|zip>.
func archiveAssetName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("mcpproxy-%s-%s-%s%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

// coreBinaryName is the archive member to install. Archives always store the
// canonical name, whatever the user renamed their local copy to. The server
// edition ships as mcpproxy-server, so a server binary must never extract the
// personal-edition member over itself.
func coreBinaryName() string {
	name := "mcpproxy"
	if Edition == "server" {
		name = "mcpproxy-server"
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// isNewer reports whether latest is strictly newer than current under SemVer
// precedence (FR-006: rc.10 > rc.2). A non-semver current version (a dev
// build) is never "older": offering a release against it would be a guess.
func isNewer(current, latest string) bool {
	c := normalizeVersion(current)
	l := normalizeVersion(latest)
	if !semver.IsValid(c) || !semver.IsValid(l) {
		return false
	}
	return semver.Compare(c, l) < 0
}

// normalizeVersion adds the "v" prefix semver.Compare needs, but only to
// something that actually looks like a version — "development" must stay
// "development" rather than being rendered as "vdevelopment".
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// emit renders the report in the requested format.
func (r *updateRunner) emit(report *updateReport) error {
	switch strings.ToLower(r.format) {
	case "json", "yaml":
		formatter, err := clioutput.NewFormatter(r.format)
		if err != nil {
			return err
		}
		out, err := formatter.Format(report)
		if err != nil {
			return err
		}
		fmt.Fprintln(r.out, out)
		return nil
	default:
		r.printReport(report)
		return nil
	}
}

func (r *updateRunner) printReport(report *updateReport) {
	fmt.Fprintln(r.out, "MCPProxy update")
	fmt.Fprintf(r.out, "  %-10s %s\n", "Current:", report.CurrentVersion)
	if report.LatestVersion != "" {
		label := report.LatestVersion
		if report.IsPrerelease {
			label += " (prerelease)"
		}
		fmt.Fprintf(r.out, "  %-10s %s\n", "Latest:", label)
	}
	fmt.Fprintf(r.out, "  %-10s %s\n", "Channel:", report.Channel)
	if report.ReleaseURL != "" {
		fmt.Fprintf(r.out, "  %-10s %s\n", "Release:", report.ReleaseURL)
	}

	fmt.Fprintln(r.out)
	switch {
	case report.Applied:
		fmt.Fprintln(r.out, report.Message)
	case report.Message != "" && !report.UpdateAvailable:
		fmt.Fprintln(r.out, report.Message)
	case report.Action == actionUpToDate || !report.UpdateAvailable:
		fmt.Fprintln(r.out, "Already up to date.")
	case report.Action == actionSelfUpdate && r.flags.checkOnly:
		fmt.Fprintln(r.out, "Run `mcpproxy update` to download, verify and install it.")
	case report.Command != "":
		fmt.Fprintf(r.out, "Run: %s\n", report.Command)
	case report.Guidance != "":
		fmt.Fprintln(r.out, report.Guidance)
	}
}
